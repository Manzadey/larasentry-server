package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"server/internal/domain"
)

func TestProjectRepo_GitHubSettings(t *testing.T) {
	db := newTestDB(t)
	accounts := NewGitHubAccountRepo(db)
	repo := NewProjectRepo(db)
	ctx := context.Background()

	accID, _ := accounts.Create(ctx, "Work", "ghp_secret")
	if err := accounts.ApplySync(ctx, accID, "octocat", []domain.GitHubRepo{{FullName: "org/app", DefaultBranch: "develop"}}); err != nil {
		t.Fatalf("ApplySync failed: %v", err)
	}
	ghRepos, _ := accounts.ListRepos(ctx, accID)

	p, err := repo.GetByID(ctx, 1)
	if err != nil {
		t.Fatalf("failed to get project by id: %v", err)
	}
	if p.GitHubRepoID != 0 || p.GitHubRepo != "" || p.GitHubToken != "" {
		t.Errorf("expected unbound project, got %+v", p)
	}

	if err := repo.UpdateGitHubSettings(ctx, 1, ghRepos[0].ID, "develop", "/var/www/html/"); err != nil {
		t.Fatalf("failed to update github settings: %v", err)
	}
	updated, _ := repo.GetByID(ctx, 1)
	if updated.GitHubRepoID != ghRepos[0].ID || updated.GitHubRepo != "org/app" || updated.GitHubToken != "ghp_secret" {
		t.Errorf("expected repo and token from account, got %+v", updated)
	}
	if updated.GitHubBranch != "develop" || updated.PathPrefix != "/var/www/html/" {
		t.Errorf("unexpected branch/prefix: %+v", updated)
	}

	all, err := repo.GetAllProjects(ctx)
	if err != nil || len(all) == 0 || all[0].GitHubRepo != "org/app" {
		t.Errorf("GetAllProjects did not join github repo: %+v, err %v", all, err)
	}
	byName, err := repo.GetByName(ctx, "default project")
	if err != nil || byName.GitHubToken != "ghp_secret" {
		t.Errorf("GetByName did not join github token: %+v, err %v", byName, err)
	}

	if err := repo.UpdateGitHubSettings(ctx, 1, 0, "", ""); err != nil {
		t.Fatalf("failed to unbind: %v", err)
	}
	unbound, _ := repo.GetByID(ctx, 1)
	if unbound.GitHubRepoID != 0 || unbound.GitHubRepo != "" || unbound.GitHubBranch != "main" {
		t.Errorf("expected unbound project with default branch, got %+v", unbound)
	}
}

func TestProjectRepo_UpdateGitHubSettings_RejectsUnselectedRepo(t *testing.T) {
	db := newTestDB(t)
	accounts := NewGitHubAccountRepo(db)
	repo := NewProjectRepo(db)
	ctx := context.Background()

	accID, _ := accounts.Create(ctx, "Work", "tok")
	_ = accounts.ApplySync(ctx, accID, "octocat", []domain.GitHubRepo{{FullName: "org/app", DefaultBranch: "main"}})
	ghRepos, _ := accounts.ListRepos(ctx, accID)
	_ = accounts.SetSelection(ctx, accID, false, nil)

	for _, repoID := range []int64{ghRepos[0].ID, 9999} {
		err := repo.UpdateGitHubSettings(ctx, 1, repoID, "main", "")
		if !errors.Is(err, domain.ErrGitHubRepoNotSelected) {
			t.Errorf("repo %d: expected ErrGitHubRepoNotSelected, got %v", repoID, err)
		}
	}
	if p, _ := repo.GetByID(ctx, 1); p.GitHubRepoID != 0 {
		t.Errorf("rejected binding must not be saved, got %d", p.GitHubRepoID)
	}
}

func TestInitDB_DropsLegacyGitHubColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("failed to open legacy db: %v", err)
	}
	_, err = legacy.Exec(`
		CREATE TABLE projects (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			api_key TEXT NOT NULL UNIQUE,
			github_repo TEXT,
			github_token TEXT,
			github_branch TEXT DEFAULT 'main',
			path_prefix TEXT
		);
		INSERT INTO projects (name, api_key, github_repo, github_token) VALUES ('Legacy', 'k', 'org/app', 'ghp_old');
	`)
	legacy.Close()
	if err != nil {
		t.Fatalf("failed to create legacy schema: %v", err)
	}

	db, err := InitDB(path)
	if err != nil {
		t.Fatalf("InitDB failed on legacy db: %v", err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT name FROM pragma_table_info('projects')")
	if err != nil {
		t.Fatalf("failed to read columns: %v", err)
	}
	cols := map[string]bool{}
	for rows.Next() {
		var name string
		_ = rows.Scan(&name)
		cols[name] = true
	}
	rows.Close()
	if cols["github_repo"] || cols["github_token"] || !cols["github_repo_id"] {
		t.Errorf("unexpected projects columns: %v", cols)
	}

	p, err := NewProjectRepo(db).GetByName(context.Background(), "Legacy")
	if err != nil {
		t.Fatalf("failed to read legacy project: %v", err)
	}
	if p.GitHubRepo != "" || p.GitHubToken != "" || p.GitHubRepoID != 0 {
		t.Errorf("legacy binding must be reset, got %+v", p)
	}
}

func TestProjectRepo_GetOrCreateByName(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test-proj-create-*.db")
	if err != nil {
		t.Fatalf("failed to create temp db: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	defer os.Remove(tmpFile.Name() + "-shm")
	defer os.Remove(tmpFile.Name() + "-wal")
	tmpFile.Close()

	db, err := InitDB(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	repo := NewProjectRepo(db)
	ctx := context.Background()

	p1, err := repo.GetOrCreateByName(ctx, "Default Project")
	if err != nil {
		t.Fatalf("failed to get default project: %v", err)
	}
	if p1.Name != "Default Project" || p1.ID != 1 {
		t.Errorf("expected ID 1 Default Project, got %+v", p1)
	}

	// Пустое имя тоже ведёт в Default Project
	pEmpty, err := repo.GetOrCreateByName(ctx, "")
	if err != nil {
		t.Fatalf("failed with empty name: %v", err)
	}
	if pEmpty.ID != 1 {
		t.Errorf("expected ID 1 for empty name, got %d", pEmpty.ID)
	}

	pNew, err := repo.GetOrCreateByName(ctx, "crm-service")
	if err != nil {
		t.Fatalf("failed to create new project: %v", err)
	}
	if pNew.Name != "crm-service" || pNew.ID <= 1 || pNew.APIKey == "" {
		t.Errorf("expected valid new project, got %+v", pNew)
	}

	// Имя проекта ищется без учёта регистра
	pSame, err := repo.GetOrCreateByName(ctx, "CRM-SERVICE")
	if err != nil {
		t.Fatalf("failed to get existing project: %v", err)
	}
	if pSame.ID != pNew.ID {
		t.Errorf("expected same project ID %d, got %d", pNew.ID, pSame.ID)
	}
}

