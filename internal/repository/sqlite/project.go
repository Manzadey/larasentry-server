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

func (r *ProjectRepo) GetByKey(ctx context.Context, apiKey string) (*domain.Project, error) {
	var p domain.Project
	err := r.db.QueryRowContext(ctx, "SELECT id, name, api_key FROM projects WHERE api_key = ?", apiKey).Scan(&p.ID, &p.Name, &p.APIKey)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *ProjectRepo) GetAllProjects(ctx context.Context) ([]domain.Project, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT id, name, api_key FROM projects ORDER BY name ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []domain.Project
	for rows.Next() {
		var p domain.Project
		if err := rows.Scan(&p.ID, &p.Name, &p.APIKey); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, nil
}
