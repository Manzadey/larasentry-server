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
	err := r.db.QueryRowContext(ctx, `
		SELECT id, name, api_key, 
		       COALESCE(github_repo, ''), 
		       COALESCE(github_token, ''), 
		       COALESCE(github_branch, 'main'), 
		       COALESCE(path_prefix, '') 
		FROM projects WHERE api_key = ?
	`, apiKey).Scan(&p.ID, &p.Name, &p.APIKey, &p.GitHubRepo, &p.GitHubToken, &p.GitHubBranch, &p.PathPrefix)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *ProjectRepo) GetByID(ctx context.Context, id int64) (*domain.Project, error) {
	var p domain.Project
	err := r.db.QueryRowContext(ctx, `
		SELECT id, name, api_key, 
		       COALESCE(github_repo, ''), 
		       COALESCE(github_token, ''), 
		       COALESCE(github_branch, 'main'), 
		       COALESCE(path_prefix, '') 
		FROM projects WHERE id = ?
	`, id).Scan(&p.ID, &p.Name, &p.APIKey, &p.GitHubRepo, &p.GitHubToken, &p.GitHubBranch, &p.PathPrefix)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *ProjectRepo) GetAllProjects(ctx context.Context) ([]domain.Project, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, api_key, 
		       COALESCE(github_repo, ''), 
		       COALESCE(github_token, ''), 
		       COALESCE(github_branch, 'main'), 
		       COALESCE(path_prefix, '') 
		FROM projects ORDER BY name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []domain.Project
	for rows.Next() {
		var p domain.Project
		if err := rows.Scan(&p.ID, &p.Name, &p.APIKey, &p.GitHubRepo, &p.GitHubToken, &p.GitHubBranch, &p.PathPrefix); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, nil
}

func (r *ProjectRepo) UpdateGitHubSettings(ctx context.Context, id int64, repo, token, branch, pathPrefix string) error {
	if branch == "" {
		branch = "main"
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE projects 
		SET github_repo = ?, github_token = ?, github_branch = ?, path_prefix = ? 
		WHERE id = ?
	`, repo, token, branch, pathPrefix, id)
	return err
}
