package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"server/internal/domain"
	"strings"
	"time"
)

type ReleaseRepo struct {
	db *sql.DB
}

func NewReleaseRepo(db *sql.DB) domain.ReleaseRepository {
	return &ReleaseRepo{db: db}
}

func (r *ReleaseRepo) UpsertRelease(ctx context.Context, projectID int64, version string) (*domain.Release, error) {
	version = strings.TrimSpace(version)
	if version == "" {
		return nil, nil
	}

	now := time.Now()
	query := `
	INSERT INTO releases (project_id, version, created_at)
	VALUES (?, ?, ?)
	ON CONFLICT(project_id, version) DO NOTHING;
	`
	_, err := r.db.ExecContext(ctx, query, projectID, version, now)
	if err != nil {
		return nil, err
	}

	return r.GetRelease(ctx, projectID, version)
}

func (r *ReleaseRepo) CreateRelease(ctx context.Context, projectID int64, version, ref, commit string) (*domain.Release, error) {
	version = strings.TrimSpace(version)
	ref = strings.TrimSpace(ref)
	commit = strings.TrimSpace(commit)
	if version == "" {
		return nil, fmt.Errorf("version cannot be empty")
	}

	now := time.Now()
	query := `
	INSERT INTO releases (project_id, version, ref, commit_sha, created_at)
	VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(project_id, version) DO UPDATE SET
		ref = CASE WHEN excluded.ref != '' THEN excluded.ref ELSE releases.ref END,
		commit_sha = CASE WHEN excluded.commit_sha != '' THEN excluded.commit_sha ELSE releases.commit_sha END;
	`
	_, err := r.db.ExecContext(ctx, query, projectID, version, ref, commit, now)
	if err != nil {
		return nil, err
	}
	return r.GetRelease(ctx, projectID, version)
}

func (r *ReleaseRepo) GetLatestRelease(ctx context.Context, projectID int64) (*domain.Release, error) {
	query := `
	SELECT id, project_id, version, COALESCE(ref, ''), COALESCE(commit_sha, ''), created_at
	FROM releases
	WHERE project_id = ?
	ORDER BY created_at DESC
	LIMIT 1;
	`
	var rel domain.Release
	err := r.db.QueryRowContext(ctx, query, projectID).Scan(
		&rel.ID, &rel.ProjectID, &rel.Version, &rel.Ref, &rel.Commit, &rel.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &rel, nil
}

func (r *ReleaseRepo) GetRelease(ctx context.Context, projectID int64, version string) (*domain.Release, error) {
	version = strings.TrimSpace(version)
	query := `
	SELECT id, project_id, version, COALESCE(ref, ''), COALESCE(commit_sha, ''), created_at
	FROM releases
	WHERE project_id = ? AND version = ?
	LIMIT 1;
	`
	var rel domain.Release
	err := r.db.QueryRowContext(ctx, query, projectID, version).Scan(
		&rel.ID, &rel.ProjectID, &rel.Version, &rel.Ref, &rel.Commit, &rel.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &rel, nil
}

func (r *ReleaseRepo) GetReleasesWithStats(ctx context.Context, projectID int64, limit int) ([]domain.ReleaseSummary, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	query := `
	SELECT 
		r.id, r.project_id, r.version, COALESCE(r.ref, ''), COALESCE(r.commit_sha, ''), r.created_at,
		(SELECT COUNT(*) FROM incidents i WHERE i.project_id = r.project_id AND (i.first_release = r.version OR i.last_release = r.version)) AS total_incidents,
		(SELECT COUNT(*) FROM incidents i WHERE i.project_id = r.project_id AND i.first_release = r.version) AS new_incidents,
		(SELECT COUNT(*) FROM incidents i WHERE i.project_id = r.project_id AND i.last_release = r.version AND i.is_regression = 1) AS regressions_count
	FROM releases r
	WHERE r.project_id = ?
	ORDER BY r.created_at DESC
	LIMIT ?;
	`
	rows, err := r.db.QueryContext(ctx, query, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []domain.ReleaseSummary
	for rows.Next() {
		var s domain.ReleaseSummary
		if err := rows.Scan(
			&s.ID, &s.ProjectID, &s.Version, &s.Ref, &s.Commit, &s.CreatedAt,
			&s.TotalIncidents, &s.NewIncidents, &s.RegressionsCount,
		); err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, nil
}

func (r *ReleaseRepo) GetIncidentsByRelease(ctx context.Context, projectID int64, version string, newOnly bool) ([]domain.Incident, error) {
	version = strings.TrimSpace(version)
	query := `
		SELECT 
			i.id, i.project_id, COALESCE(p.name, 'Default Project') as project_name, 
			i.hash, i.exception_class, i.message, i.file, i.line, i.app_url, i.request_url, 
			i.events_count, i.first_seen_at, i.last_seen_at,
			i.environment, COALESCE(i.first_release, ''), COALESCE(i.last_release, ''),
			i.status, COALESCE(i.resolved_in_release, ''), i.resolved_at, i.is_regression,
			COALESCE(i.php_version, ''), COALESCE(i.laravel_version, '')
		FROM incidents i
		LEFT JOIN projects p ON i.project_id = p.id
		WHERE i.project_id = ?
	`
	var args []interface{}
	args = append(args, projectID)

	if newOnly {
		query += " AND i.first_release = ?"
		args = append(args, version)
	} else {
		query += " AND (i.first_release = ? OR i.last_release = ?)"
		args = append(args, version, version)
	}
	query += " ORDER BY i.last_seen_at DESC LIMIT 100"

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
			&inc.PHPVersion, &inc.LaravelVersion,
		); err != nil {
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

