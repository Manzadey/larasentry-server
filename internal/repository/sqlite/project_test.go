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

	// 1. Get existing Default Project
	p1, err := repo.GetOrCreateByName(ctx, "Default Project")
	if err != nil {
		t.Fatalf("failed to get default project: %v", err)
	}
	if p1.Name != "Default Project" || p1.ID != 1 {
		t.Errorf("expected ID 1 Default Project, got %+v", p1)
	}

	// 2. Empty string should also return Default Project
	pEmpty, err := repo.GetOrCreateByName(ctx, "")
	if err != nil {
		t.Fatalf("failed with empty name: %v", err)
	}
	if pEmpty.ID != 1 {
		t.Errorf("expected ID 1 for empty name, got %d", pEmpty.ID)
	}

	// 3. Create new project "crm-service"
	pNew, err := repo.GetOrCreateByName(ctx, "crm-service")
	if err != nil {
		t.Fatalf("failed to create new project: %v", err)
	}
	if pNew.Name != "crm-service" || pNew.ID <= 1 || pNew.APIKey == "" {
		t.Errorf("expected valid new project, got %+v", pNew)
	}

	// 4. Getting "CRM-SERVICE" (different case) should return the same project
	pSame, err := repo.GetOrCreateByName(ctx, "CRM-SERVICE")
	if err != nil {
		t.Fatalf("failed to get existing project: %v", err)
	}
	if pSame.ID != pNew.ID {
		t.Errorf("expected same project ID %d, got %d", pNew.ID, pSame.ID)
	}
}

