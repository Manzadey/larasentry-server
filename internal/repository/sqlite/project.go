package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"

	"server/internal/domain"
)

type ProjectRepo struct {
	db *sql.DB
}

func NewProjectRepo(db *sql.DB) domain.ProjectRepository {
	return &ProjectRepo{db: db}
}

const projectSelect = `
	SELECT p.id, p.name, p.api_key,
	       COALESCE(p.github_repo_id, 0),
	       COALESCE(r.full_name, ''),
	       COALESCE(a.token, ''),
	       COALESCE(p.github_branch, 'main'),
	       COALESCE(p.path_prefix, '')
	FROM projects p
	LEFT JOIN github_repos r ON r.id = p.github_repo_id
	LEFT JOIN github_accounts a ON a.id = r.account_id`

func scanProject(s rowScanner) (domain.Project, error) {
	var p domain.Project
	err := s.Scan(&p.ID, &p.Name, &p.APIKey, &p.GitHubRepoID, &p.GitHubRepo, &p.GitHubToken, &p.GitHubBranch, &p.PathPrefix)
	return p, err
}

func (r *ProjectRepo) getOne(ctx context.Context, where string, args ...any) (*domain.Project, error) {
	p, err := scanProject(r.db.QueryRowContext(ctx, projectSelect+" WHERE "+where, args...))
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *ProjectRepo) GetIDByKey(ctx context.Context, apiKey string) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx, "SELECT id FROM projects WHERE api_key = ?", apiKey).Scan(&id)
	return id, err
}

func (r *ProjectRepo) GetByKey(ctx context.Context, apiKey string) (*domain.Project, error) {
	return r.getOne(ctx, "p.api_key = ?", apiKey)
}

func (r *ProjectRepo) GetByName(ctx context.Context, name string) (*domain.Project, error) {
	return r.getOne(ctx, "unicode_lower(p.name) = unicode_lower(?) LIMIT 1", strings.TrimSpace(name))
}

func (r *ProjectRepo) GetByID(ctx context.Context, id int64) (*domain.Project, error) {
	return r.getOne(ctx, "p.id = ?", id)
}

func (r *ProjectRepo) GetOrCreateByName(ctx context.Context, name string) (*domain.Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Default Project"
	}

	p, err := r.GetByName(ctx, name)
	if err == nil && p != nil {
		return p, nil
	}

	apiKey := uuid.New().String()
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO projects (name, api_key) VALUES (?, ?)
	`, name, apiKey)
	if err != nil {
		pRetry, errRetry := r.GetByName(ctx, name)
		if errRetry == nil && pRetry != nil {
			return pRetry, nil
		}
		return nil, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}

	return &domain.Project{
		ID:           id,
		Name:         name,
		APIKey:       apiKey,
		GitHubBranch: "main",
	}, nil
}

func (r *ProjectRepo) GetAllProjects(ctx context.Context) ([]domain.Project, error) {
	rows, err := r.db.QueryContext(ctx, projectSelect+" ORDER BY p.name ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []domain.Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func (r *ProjectRepo) UpdateGitHubSettings(ctx context.Context, id, repoID int64, branch, pathPrefix string) error {
	if branch == "" {
		branch = "main"
	}

	var repoRef any
	if repoID != 0 {
		var selected bool
		err := r.db.QueryRowContext(ctx, "SELECT selected FROM github_repos WHERE id = ?", repoID).Scan(&selected)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !selected) {
			return domain.ErrGitHubRepoNotSelected
		}
		if err != nil {
			return err
		}
		repoRef = repoID
	}

	_, err := r.db.ExecContext(ctx, `
		UPDATE projects
		SET github_repo_id = ?, github_branch = ?, path_prefix = ?
		WHERE id = ?
	`, repoRef, branch, pathPrefix, id)
	return err
}
