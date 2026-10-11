package sqlite

import (
	"context"
	"os"
	"testing"
)

func TestProjectRepo_GitHubSettings(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test-proj-*.db")
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

	// Default project is created in InitDB with ID 1
	p, err := repo.GetByID(ctx, 1)
	if err != nil {
		t.Fatalf("failed to get project by id: %v", err)
	}
	if p.Name != "Default Project" {
		t.Errorf("expected name 'Default Project', got '%s'", p.Name)
	}
	if p.GitHubRepo != "" {
		t.Errorf("expected empty github_repo initially, got '%s'", p.GitHubRepo)
	}

	// Update GitHub settings
	err = repo.UpdateGitHubSettings(ctx, 1, "Manzadey/larasentry-server", "ghp_secrettoken123", "main", "/var/www/html/")
	if err != nil {
		t.Fatalf("failed to update github settings: %v", err)
	}

	// Fetch again and verify
	updated, err := repo.GetByID(ctx, 1)
	if err != nil {
		t.Fatalf("failed to get updated project: %v", err)
	}
	if updated.GitHubRepo != "Manzadey/larasentry-server" {
		t.Errorf("expected github_repo 'Manzadey/larasentry-server', got '%s'", updated.GitHubRepo)
	}
	if updated.GitHubToken != "ghp_secrettoken123" {
		t.Errorf("expected github_token 'ghp_secrettoken123', got '%s'", updated.GitHubToken)
	}
	if updated.GitHubBranch != "main" {
		t.Errorf("expected github_branch 'main', got '%s'", updated.GitHubBranch)
	}
	if updated.PathPrefix != "/var/www/html/" {
		t.Errorf("expected path_prefix '/var/www/html/', got '%s'", updated.PathPrefix)
	}

	// Check GetAllProjects contains the updated fields
	all, err := repo.GetAllProjects(ctx)
	if err != nil {
		t.Fatalf("failed to get all projects: %v", err)
	}
	if len(all) == 0 || all[0].GitHubRepo != "Manzadey/larasentry-server" {
		t.Errorf("GetAllProjects did not populate github_repo properly")
	}
}
