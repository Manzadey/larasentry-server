package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"server/internal/domain"
	"server/internal/pkg/version"
)

type IncidentRepo struct {
	db          *sql.DB
	releaseRepo domain.ReleaseRepository
}

func NewIncidentRepo(db *sql.DB, releaseRepo domain.ReleaseRepository) domain.IncidentRepository {
	return &IncidentRepo{
		db:          db,
		releaseRepo: releaseRepo,
	}
}

func (r *IncidentRepo) UpsertIncident(ctx context.Context, event domain.IncomingEvent, hash string) (*domain.UpsertResult, error) {
	now := time.Now()
	incomingRel := strings.TrimSpace(event.Payload.Release)
	incomingEnv := strings.TrimSpace(event.Payload.Environment)
	if incomingEnv == "" {
		incomingEnv = "production"
	}

	// 1. Auto-register release if provided
	if incomingRel != "" && r.releaseRepo != nil {
		_, _ = r.releaseRepo.UpsertRelease(ctx, event.ProjectID, incomingRel)
	}

	// 2. Check if incident already exists
	var existing struct {
		id                int64
		eventsCount       int
		status            domain.IncidentStatus
		resolvedInRelease string
		resolvedAt        sql.NullTime
		lastRelease       string
	}

	checkQuery := `
	SELECT id, events_count, status, COALESCE(resolved_in_release, ''), resolved_at, COALESCE(last_release, '')
	FROM incidents
	WHERE hash = ?
	LIMIT 1;
	`
	err := r.db.QueryRowContext(ctx, checkQuery, hash).Scan(
		&existing.id, &existing.eventsCount, &existing.status,
		&existing.resolvedInRelease, &existing.resolvedAt, &existing.lastRelease,
	)

	// Case A: New incident
	if err == sql.ErrNoRows {
		insertQuery := `
		INSERT INTO incidents (
			project_id, hash, exception_class, message, file, line, 
			stack_trace, app_url, request_url, events_count, first_seen_at, last_seen_at,
			environment, first_release, last_release, status, is_regression
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, 'unresolved', 0)
		RETURNING id;
		`
		var newID int64
		err = r.db.QueryRowContext(ctx, insertQuery,
			event.ProjectID, hash, event.Payload.ExceptionClass, event.Payload.Message,
			event.Payload.File, event.Payload.Line, event.Payload.StackTrace,
			event.Payload.AppURL, event.Payload.RequestURL, now, now,
			incomingEnv, incomingRel, incomingRel,
		).Scan(&newID)
		if err != nil {
			return nil, err
		}

		inc, _ := r.GetIncidentByID(ctx, newID)
		var incidentObj domain.Incident
		if inc != nil {
			incidentObj = *inc
		}

		return &domain.UpsertResult{
			IncidentID:   newID,
			TotalCount:   1,
			IsRegression: false,
			IsNew:        true,
			Incident:     incidentObj,
			Ignored:      false,
		}, nil
	} else if err != nil {
		return nil, err
	}

	// Case B: Incident already exists
	// Check if this is an already resolved issue
	if existing.status == domain.StatusResolved {
		isRegression := false
		resolvedIn := existing.resolvedInRelease

		if resolvedIn == "" || resolvedIn == "immediate" {
			// Immediate resolve re-opens on any new occurrence
			isRegression = true
		} else if resolvedIn == "next" {
			// Resolved in next release: check if incoming release is different from the release at resolution time
			if incomingRel != "" && incomingRel != existing.lastRelease {
				isRegression = true
			}
		} else {
			// Resolved in specific version (e.g. "1.2.0")
			if incomingRel != "" {
				cmp := version.Compare(incomingRel, resolvedIn, func(v1, v2 string) int {
					// Fallback: compare chronological created_at in releases table
					if r.releaseRepo == nil {
						return strings.Compare(v1, v2)
					}
					r1, _ := r.releaseRepo.GetRelease(ctx, event.ProjectID, v1)
					r2, _ := r.releaseRepo.GetRelease(ctx, event.ProjectID, v2)
					if r1 != nil && r2 != nil {
						if r1.CreatedAt.After(r2.CreatedAt) {
							return 1
						} else if r1.CreatedAt.Before(r2.CreatedAt) {
							return -1
						}
						return 0
					}
					return strings.Compare(v1, v2)
				})
				if cmp >= 0 {
					// Event is from resolved release or newer -> Regression!
					isRegression = true
				}
			}
		}

		if isRegression {
			// Re-open incident and mark as regression
			updateQuery := `
			UPDATE incidents SET
				events_count = events_count + 1,
				last_seen_at = ?,
				message = ?,
				last_release = CASE WHEN ? != '' THEN ? ELSE last_release END,
				status = 'unresolved',
				is_regression = 1
			WHERE id = ?;
			`
			_, err = r.db.ExecContext(ctx, updateQuery, now, event.Payload.Message, incomingRel, incomingRel, existing.id)
			if err != nil {
				return nil, err
			}

			inc, _ := r.GetIncidentByID(ctx, existing.id)
			var incidentObj domain.Incident
			if inc != nil {
				incidentObj = *inc
			}

			return &domain.UpsertResult{
				IncidentID:   existing.id,
				TotalCount:   existing.eventsCount + 1,
				IsRegression: true,
				IsNew:        false,
				Incident:     incidentObj,
				Ignored:      false,
			}, nil
		}

		// Event from an older release: update counters quietly, keep status resolved
		quietQuery := `
		UPDATE incidents SET
			events_count = events_count + 1,
			last_seen_at = ?
		WHERE id = ?;
		`
		_, err = r.db.ExecContext(ctx, quietQuery, now, existing.id)
		if err != nil {
			return nil, err
		}

		inc, _ := r.GetIncidentByID(ctx, existing.id)
		var incidentObj domain.Incident
		if inc != nil {
			incidentObj = *inc
		}

		return &domain.UpsertResult{
			IncidentID:   existing.id,
			TotalCount:   existing.eventsCount + 1,
			IsRegression: false,
			IsNew:        false,
			Incident:     incidentObj,
			Ignored:      true, // Ignored for alerts and active list
		}, nil
	}

	// Normal active incident update (unresolved or ignored)
	updateQuery := `
	UPDATE incidents SET
		events_count = events_count + 1,
		last_seen_at = ?,
		message = ?,
		last_release = CASE WHEN ? != '' THEN ? ELSE last_release END
	WHERE id = ?;
	`
	_, err = r.db.ExecContext(ctx, updateQuery, now, event.Payload.Message, incomingRel, incomingRel, existing.id)
	if err != nil {
		return nil, err
	}

	inc, _ := r.GetIncidentByID(ctx, existing.id)
	var incidentObj domain.Incident
	if inc != nil {
		incidentObj = *inc
	}

	isIgnored := existing.status == domain.StatusIgnored
	return &domain.UpsertResult{
		IncidentID:   existing.id,
		TotalCount:   existing.eventsCount + 1,
		IsRegression: false,
		IsNew:        false,
		Incident:     incidentObj,
		Ignored:      isIgnored,
	}, nil
}

func (r *IncidentRepo) ResolveIncident(ctx context.Context, id int64, resolutionType string, targetVersion string) error {
	now := time.Now()
	resolutionType = strings.TrimSpace(resolutionType)
	targetVersion = strings.TrimSpace(targetVersion)

	resolvedIn := targetVersion
	if resolutionType == "immediate" {
		resolvedIn = "immediate"
	} else if resolutionType == "next" {
		resolvedIn = "next"
	} else if resolutionType == "current" && resolvedIn == "" {
		// Look up latest release of project
		var projectID int64
		_ = r.db.QueryRowContext(ctx, "SELECT project_id FROM incidents WHERE id = ?", id).Scan(&projectID)
		if r.releaseRepo != nil && projectID > 0 {
			rel, _ := r.releaseRepo.GetLatestRelease(ctx, projectID)
			if rel != nil {
				resolvedIn = rel.Version
			}
		}
		if resolvedIn == "" {
			resolvedIn = "current"
		}
	}

	query := `
	UPDATE incidents SET
		status = 'resolved',
		resolved_in_release = ?,
		resolved_at = ?,
		is_regression = 0
	WHERE id = ?;
	`
	_, err := r.db.ExecContext(ctx, query, resolvedIn, now, id)
	return err
}

func (r *IncidentRepo) ReopenIncident(ctx context.Context, id int64) error {
	query := `
	UPDATE incidents SET
		status = 'unresolved',
		resolved_in_release = NULL,
		resolved_at = NULL,
		is_regression = 0
	WHERE id = ?;
	`
	_, err := r.db.ExecContext(ctx, query, id)
	return err
}

func (r *IncidentRepo) GetLatestIncidents(ctx context.Context, projectID int64, limit int) ([]domain.Incident, error) {
	return r.GetIncidents(ctx, domain.IncidentFilter{
		ProjectID: projectID,
		Status:    "all",
		Limit:     limit,
		Sort:      "recent",
	})
}

func buildIncidentWhere(filter domain.IncidentFilter) (string, []interface{}) {
	where := " WHERE 1=1"
	var args []interface{}

	if filter.ProjectID > 0 {
		where += " AND i.project_id = ?"
		args = append(args, filter.ProjectID)
	}

	if filter.Status != "" && filter.Status != "all" {
		where += " AND i.status = ?"
		args = append(args, filter.Status)
	}

	if filter.Environment != "" {
		where += " AND i.environment = ?"
		args = append(args, filter.Environment)
	}

	if filter.Release != "" {
		where += " AND (i.first_release = ? OR i.last_release = ?)"
		args = append(args, filter.Release, filter.Release)
	}

	if filter.IsRegression != nil {
		if *filter.IsRegression {
			where += " AND i.is_regression = 1"
		} else {
			where += " AND i.is_regression = 0"
		}
	}

	if filter.Query != "" {
		where += " AND (i.message LIKE ? OR i.exception_class LIKE ? OR i.file LIKE ? OR i.request_url LIKE ? OR p.name LIKE ? OR i.last_release LIKE ?)"
		pattern := "%" + filter.Query + "%"
		args = append(args, pattern, pattern, pattern, pattern, pattern, pattern)
	}

	return where, args
}

func (r *IncidentRepo) GetIncidents(ctx context.Context, filter domain.IncidentFilter) ([]domain.Incident, error) {
	query := `
		SELECT 
			i.id, i.project_id, COALESCE(p.name, 'Default Project') as project_name, 
			i.hash, i.exception_class, i.message, i.file, i.line, i.app_url, i.request_url, 
			i.events_count, i.first_seen_at, i.last_seen_at,
			i.environment, COALESCE(i.first_release, ''), COALESCE(i.last_release, ''),
			i.status, COALESCE(i.resolved_in_release, ''), i.resolved_at, i.is_regression
		FROM incidents i
		LEFT JOIN projects p ON i.project_id = p.id
	`
	where, args := buildIncidentWhere(filter)
	query += where

	switch filter.Sort {
	case "count":
		query += " ORDER BY i.events_count DESC, i.last_seen_at DESC"
	case "first_seen":
		query += " ORDER BY i.first_seen_at DESC"
	default:
		query += " ORDER BY i.last_seen_at DESC"
	}

	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query += " LIMIT ?"
	args = append(args, limit)

	if filter.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, filter.Offset)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var incidents []domain.Incident
	for rows.Next() {
		var inc domain.Incident
		var resolvedAt sql.NullTime
		var isRegrInt int
		if err := rows.Scan(
			&inc.ID, &inc.ProjectID, &inc.ProjectName, &inc.Hash, &inc.ExceptionClass, &inc.Message,
			&inc.File, &inc.Line, &inc.AppURL, &inc.RequestURL,
			&inc.EventsCount, &inc.FirstSeenAt, &inc.LastSeenAt,
			&inc.Environment, &inc.FirstRelease, &inc.LastRelease,
			&inc.Status, &inc.ResolvedInRelease, &resolvedAt, &isRegrInt,
		); err != nil {
			log.Printf("Error scanning row: %v", err)
			continue
		}
		if resolvedAt.Valid {
			inc.ResolvedAt = &resolvedAt.Time
		}
		inc.IsRegression = isRegrInt == 1
		incidents = append(incidents, inc)
	}
	return incidents, nil
}

func (r *IncidentRepo) GetIncidentsCount(ctx context.Context, filter domain.IncidentFilter) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM incidents i
		LEFT JOIN projects p ON i.project_id = p.id
	`
	where, args := buildIncidentWhere(filter)
	query += where

	var count int
	err := r.db.QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}

func (r *IncidentRepo) GetIncidentByID(ctx context.Context, id int64) (*domain.Incident, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT 
			i.id, i.project_id, COALESCE(p.name, 'Default Project') as project_name, 
			i.hash, i.exception_class, i.message, i.file, i.line, i.stack_trace, 
			i.app_url, i.request_url, i.events_count, i.first_seen_at, i.last_seen_at,
			i.environment, COALESCE(i.first_release, ''), COALESCE(i.last_release, ''),
			i.status, COALESCE(i.resolved_in_release, ''), i.resolved_at, i.is_regression
		FROM incidents i
		LEFT JOIN projects p ON i.project_id = p.id
		WHERE i.id = ?`, id)

	var inc domain.Incident
	var resolvedAt sql.NullTime
	var isRegrInt int
	err := row.Scan(
		&inc.ID, &inc.ProjectID, &inc.ProjectName, &inc.Hash, &inc.ExceptionClass, &inc.Message,
		&inc.File, &inc.Line, &inc.StackTrace, &inc.AppURL, &inc.RequestURL,
		&inc.EventsCount, &inc.FirstSeenAt, &inc.LastSeenAt,
		&inc.Environment, &inc.FirstRelease, &inc.LastRelease,
		&inc.Status, &inc.ResolvedInRelease, &resolvedAt, &isRegrInt,
	)
	if err != nil {
		return nil, err
	}
	if resolvedAt.Valid {
		inc.ResolvedAt = &resolvedAt.Time
	}
	inc.IsRegression = isRegrInt == 1
	return &inc, nil
}

func (r *IncidentRepo) CountIncidents(ctx context.Context, projectID int64, status string) (int, error) {
	query := "SELECT COUNT(*) FROM incidents WHERE 1=1"
	var args []interface{}

	if projectID > 0 {
		query += " AND project_id = ?"
		args = append(args, projectID)
	}

	if status != "" && status != "all" {
		query += " AND status = ?"
		args = append(args, status)
	}

	var count int
	err := r.db.QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}

func (r *IncidentRepo) CleanOldIncidents(ctx context.Context, daysToKeep int) (int64, error) {
	query := `DELETE FROM incidents WHERE last_seen_at < datetime('now', ?)`
	modifier := fmt.Sprintf("-%d days", daysToKeep)

	result, err := r.db.ExecContext(ctx, query, modifier)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
}

func (r *IncidentRepo) IgnoreIncident(ctx context.Context, id int64, ignored bool) error {
	targetStatus := domain.StatusIgnored
	if !ignored {
		targetStatus = domain.StatusUnresolved
	}
	_, err := r.db.ExecContext(ctx, "UPDATE incidents SET status = ? WHERE id = ?", string(targetStatus), id)
	return err
}

func (r *IncidentRepo) DeleteIncident(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM incidents WHERE id = ?", id)
	return err
}

func (r *IncidentRepo) BatchAction(ctx context.Context, action string, ids []int64, params *domain.ResolveRequest) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	inClause := strings.Join(placeholders, ",")

	var affected int64
	switch action {
	case "delete":
		query := fmt.Sprintf("DELETE FROM incidents WHERE id IN (%s)", inClause)
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return 0, err
		}
		affected, _ = res.RowsAffected()

	case "reopen":
		query := fmt.Sprintf("UPDATE incidents SET status = 'unresolved', resolved_in_release = NULL, resolved_at = NULL, is_regression = 0 WHERE id IN (%s)", inClause)
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return 0, err
		}
		affected, _ = res.RowsAffected()

	case "ignore":
		query := fmt.Sprintf("UPDATE incidents SET status = 'ignored' WHERE id IN (%s)", inClause)
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return 0, err
		}
		affected, _ = res.RowsAffected()

	case "resolve":
		resType := "immediate"
		var targetVersion string
		if params != nil {
			if params.Type != "" {
				resType = params.Type
			}
			targetVersion = params.Version
		}

		resolvedIn := targetVersion
		if resType == "immediate" || resType == "next" {
			resolvedIn = resType
		} else if resType == "current" && resolvedIn == "" {
			resolvedIn = "current"
		}

		now := time.Now()
		query := fmt.Sprintf("UPDATE incidents SET status = 'resolved', resolved_in_release = ?, resolved_at = ?, is_regression = 0 WHERE id IN (%s)", inClause)
		execArgs := append([]interface{}{resolvedIn, now}, args...)
		res, err := tx.ExecContext(ctx, query, execArgs...)
		if err != nil {
			return 0, err
		}
		affected, _ = res.RowsAffected()

	default:
		return 0, fmt.Errorf("unsupported batch action: %s", action)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return affected, nil
}

func (r *IncidentRepo) GetStatsSummary(ctx context.Context, projectID int64) (*domain.StatsSummary, error) {
	summary := &domain.StatsSummary{}

	countQuery := `
		SELECT 
			COUNT(*),
			COALESCE(SUM(CASE WHEN status = 'unresolved' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'resolved' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'ignored' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN is_regression = 1 THEN 1 ELSE 0 END), 0)
		FROM incidents
		WHERE (? = 0 OR project_id = ?)
	`
	err := r.db.QueryRowContext(ctx, countQuery, projectID, projectID).Scan(
		&summary.TotalIncidents,
		&summary.Unresolved,
		&summary.Resolved,
		&summary.Ignored,
		&summary.Regressions,
	)
	if err != nil {
		return nil, err
	}

	eventsQuery := `
		SELECT COALESCE(SUM(events_count), 0)
		FROM incidents
		WHERE (? = 0 OR project_id = ?) AND last_seen_at >= datetime('now', '-24 hours')
	`
	_ = r.db.QueryRowContext(ctx, eventsQuery, projectID, projectID).Scan(&summary.EventsLast24H)

	releasesQuery := `
		SELECT COUNT(DISTINCT version)
		FROM releases
		WHERE (? = 0 OR project_id = ?)
	`
	_ = r.db.QueryRowContext(ctx, releasesQuery, projectID, projectID).Scan(&summary.ActiveReleasesCount)

	return summary, nil
}

