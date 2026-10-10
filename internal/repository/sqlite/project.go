package sqlite

import (
	"context"
	"database/sql"
	"server/internal/domain"
)

type ProjectRepo struct {
	db *sql.DB
}

func NewProjectRepo(db *sql.DB) domain.ProjectRepository {
	return &ProjectRepo{db: db}
}

func (r *ProjectRepo) GetIDByKey(ctx context.Context, apiKey string) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx, "SELECT id FROM projects WHERE api_key = ?", apiKey).Scan(&id)
	return id, err
}
