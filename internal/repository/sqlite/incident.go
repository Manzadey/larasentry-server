package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"server/internal/domain"
	"time"
)

type IncidentRepo struct {
	db *sql.DB
}

func NewIncidentRepo(db *sql.DB) domain.IncidentRepository {
	return &IncidentRepo{db: db}
}

func (r *IncidentRepo) UpsertIncident(ctx context.Context, event domain.IncomingEvent, hash string) (int64, int, error) {
	now := time.Now()
	query := `
	INSERT INTO incidents (
		project_id, hash, exception_class, message, file, line, 
		stack_trace, app_url, request_url, events_count, first_seen_at, last_seen_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
	ON CONFLICT(hash) DO UPDATE SET 
		events_count = events_count + 1,
		last_seen_at = excluded.last_seen_at,
		message = excluded.message
	RETURNING id, events_count;
	`
	
	var incidentID int64
	var totalCount int
	err := r.db.QueryRowContext(ctx, query, 
		event.ProjectID, hash, event.Payload.ExceptionClass, event.Payload.Message, 
		event.Payload.File, event.Payload.Line, event.Payload.StackTrace, 
		event.Payload.AppURL, event.Payload.RequestURL, now, now,
	).Scan(&incidentID, &totalCount)

	return incidentID, totalCount, err
}

func (r *IncidentRepo) GetLatestIncidents(ctx context.Context, projectID int64, limit int) ([]domain.Incident, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, hash, exception_class, message, file, line, app_url, request_url, events_count, first_seen_at, last_seen_at 
		FROM incidents 
		WHERE project_id = ? 
		ORDER BY last_seen_at DESC 
		LIMIT ?`, projectID, limit)
	
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var incidents []domain.Incident
	for rows.Next() {
		var inc domain.Incident
		if err := rows.Scan(
			&inc.ID, &inc.Hash, &inc.ExceptionClass, &inc.Message, 
			&inc.File, &inc.Line, &inc.AppURL, &inc.RequestURL, 
			&inc.EventsCount, &inc.FirstSeenAt, &inc.LastSeenAt,
		); err != nil {
			log.Printf("Error scanning row: %v", err)
			continue
		}
		inc.ProjectID = projectID
		incidents = append(incidents, inc)
	}
	return incidents, nil
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
