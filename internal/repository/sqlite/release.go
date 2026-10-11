package sqlite

import (
	"context"
	"database/sql"
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

func (r *ReleaseRepo) GetLatestRelease(ctx context.Context, projectID int64) (*domain.Release, error) {
	query := `
	SELECT id, project_id, version, created_at
	FROM releases
	WHERE project_id = ?
	ORDER BY created_at DESC
	LIMIT 1;
	`
	var rel domain.Release
	err := r.db.QueryRowContext(ctx, query, projectID).Scan(
		&rel.ID, &rel.ProjectID, &rel.Version, &rel.CreatedAt,
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
	SELECT id, project_id, version, created_at
	FROM releases
	WHERE project_id = ? AND version = ?
	LIMIT 1;
	`
	var rel domain.Release
	err := r.db.QueryRowContext(ctx, query, projectID, version).Scan(
		&rel.ID, &rel.ProjectID, &rel.Version, &rel.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &rel, nil
}
