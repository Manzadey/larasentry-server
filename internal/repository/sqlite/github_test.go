package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"server/internal/domain"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func bindProject(t *testing.T, db *sql.DB, projectID, repoID int64) {
	t.Helper()
	if _, err := db.Exec("UPDATE projects SET github_repo_id = ? WHERE id = ?", repoID, projectID); err != nil {
		t.Fatalf("failed to bind project: %v", err)
	}
}

func projectRepoID(t *testing.T, db *sql.DB, projectID int64) sql.NullInt64 {
	t.Helper()
	var id sql.NullInt64
	if err := db.QueryRow("SELECT github_repo_id FROM projects WHERE id = ?", projectID).Scan(&id); err != nil {
		t.Fatalf("failed to read project: %v", err)
	}
	return id
}

func repoByName(t *testing.T, repos []domain.GitHubRepo, name string) domain.GitHubRepo {
	t.Helper()
	for _, r := range repos {
		if r.FullName == name {
			return r
		}
	}
	t.Fatalf("repo %s not found in %+v", name, repos)
	return domain.GitHubRepo{}
}

func syncRepos(t *testing.T, repo domain.GitHubAccountRepository, accountID int64, names ...string) []domain.GitHubRepo {
	t.Helper()
	ctx := context.Background()
	input := make([]domain.GitHubRepo, len(names))
	for i, n := range names {
		input[i] = domain.GitHubRepo{FullName: n, DefaultBranch: "main"}
	}
	if err := repo.ApplySync(ctx, accountID, "octocat", input); err != nil {
		t.Fatalf("ApplySync failed: %v", err)
	}
	repos, err := repo.ListRepos(ctx, accountID)
	if err != nil {
		t.Fatalf("ListRepos failed: %v", err)
	}
	return repos
}

func TestGitHubAccountRepo_ApplySync_ImportAll(t *testing.T) {
	db := newTestDB(t)
	repo := NewGitHubAccountRepo(db)
	ctx := context.Background()

	id, err := repo.Create(ctx, "Work", "ghp_secret")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	repos := syncRepos(t, repo, id, "org/b", "org/a")

	if len(repos) != 2 || repos[0].FullName != "org/a" {
		t.Fatalf("expected repos sorted by name, got %+v", repos)
	}
	for _, r := range repos {
		if !r.Selected || r.AccountName != "Work" || r.AccountLogin != "octocat" {
			t.Errorf("unexpected repo: %+v", r)
		}
	}

	acc, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if acc.Login != "octocat" || acc.Token != "ghp_secret" || !acc.ImportAll || acc.SyncedAt == nil {
		t.Errorf("unexpected account: %+v", acc)
	}
	if acc.ReposTotal != 2 || acc.ReposSelected != 2 {
		t.Errorf("expected 2/2 repos, got %d/%d", acc.ReposSelected, acc.ReposTotal)
	}
}

func TestGitHubAccountRepo_ApplySync_SelectedModeAddsUnselected(t *testing.T) {
	db := newTestDB(t)
	repo := NewGitHubAccountRepo(db)
	ctx := context.Background()

	id, _ := repo.Create(ctx, "Work", "tok")
	if err := repo.SetSelection(ctx, id, false, nil); err != nil {
		t.Fatalf("SetSelection failed: %v", err)
	}
	repos := syncRepos(t, repo, id, "org/new")

	if repos[0].Selected {
		t.Errorf("new repo must not be selected when import_all is off: %+v", repos[0])
	}
}

func TestGitHubAccountRepo_ApplySync_RemovesStaleAndUnlinks(t *testing.T) {
	db := newTestDB(t)
	repo := NewGitHubAccountRepo(db)
	ctx := context.Background()

	id, _ := repo.Create(ctx, "Work", "tok")
	repos := syncRepos(t, repo, id, "org/a", "org/b")
	bindProject(t, db, 1, repoByName(t, repos, "org/b").ID)

	err := repo.ApplySync(ctx, id, "octocat", []domain.GitHubRepo{{FullName: "org/a", DefaultBranch: "develop", Private: true}})
	if err != nil {
		t.Fatalf("ApplySync failed: %v", err)
	}
	repos, _ = repo.ListRepos(ctx, id)

	if len(repos) != 1 || repos[0].DefaultBranch != "develop" || !repos[0].Private {
		t.Fatalf("expected only updated org/a, got %+v", repos)
	}
	if projectRepoID(t, db, 1).Valid {
		t.Errorf("project bound to removed repo must be unlinked")
	}
}

func TestGitHubAccountRepo_SetSelection_UnlinksDeselected(t *testing.T) {
	db := newTestDB(t)
	repo := NewGitHubAccountRepo(db)
	ctx := context.Background()

	id, _ := repo.Create(ctx, "Work", "tok")
	repos := syncRepos(t, repo, id, "org/a", "org/b")
	a, b := repoByName(t, repos, "org/a"), repoByName(t, repos, "org/b")
	bindProject(t, db, 1, b.ID)

	if err := repo.SetSelection(ctx, id, false, []int64{a.ID}); err != nil {
		t.Fatalf("SetSelection failed: %v", err)
	}
	repos, _ = repo.ListRepos(ctx, id)

	if !repoByName(t, repos, "org/a").Selected || repoByName(t, repos, "org/b").Selected {
		t.Errorf("unexpected selection: %+v", repos)
	}
	if projectRepoID(t, db, 1).Valid {
		t.Errorf("project bound to deselected repo must be unlinked")
	}
	acc, _ := repo.Get(ctx, id)
	if acc.ImportAll {
		t.Errorf("import_all must be off")
	}

	if err := repo.SetSelection(ctx, id, true, nil); err != nil {
		t.Fatalf("SetSelection failed: %v", err)
	}
	repos, _ = repo.ListRepos(ctx, id)
	if !repoByName(t, repos, "org/b").Selected {
		t.Errorf("import_all must select every repo")
	}
}

func TestGitHubAccountRepo_SetSelection_IgnoresForeignRepoIDs(t *testing.T) {
	db := newTestDB(t)
	repo := NewGitHubAccountRepo(db)
	ctx := context.Background()

	first, _ := repo.Create(ctx, "First", "tok1")
	second, _ := repo.Create(ctx, "Second", "tok2")
	_ = repo.SetSelection(ctx, second, false, nil)
	foreign := syncRepos(t, repo, second, "other/secret")[0]

	if err := repo.SetSelection(ctx, first, false, []int64{foreign.ID}); err != nil {
		t.Fatalf("SetSelection failed: %v", err)
	}
	repos, _ := repo.ListRepos(ctx, second)
	if repos[0].Selected {
		t.Errorf("repo of another account must not be selected via forged form")
	}
}

func TestGitHubAccountRepo_Delete_KeepsOtherAccountLinks(t *testing.T) {
	db := newTestDB(t)
	repo := NewGitHubAccountRepo(db)
	ctx := context.Background()

	if _, err := db.Exec("INSERT INTO projects (name, api_key) VALUES ('Second', 'key-2')"); err != nil {
		t.Fatalf("failed to insert project: %v", err)
	}
	a, _ := repo.Create(ctx, "A", "tok-a")
	b, _ := repo.Create(ctx, "B", "tok-b")
	sharedA := syncRepos(t, repo, a, "org/shared")[0]
	sharedB := syncRepos(t, repo, b, "org/shared")[0]
	bindProject(t, db, 1, sharedA.ID)
	bindProject(t, db, 2, sharedB.ID)

	if err := repo.Delete(ctx, a); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if projectRepoID(t, db, 1).Valid {
		t.Errorf("project of deleted account must be unlinked")
	}
	if got := projectRepoID(t, db, 2); !got.Valid || got.Int64 != sharedB.ID {
		t.Errorf("project of other account must stay linked, got %+v", got)
	}
	if repos, _ := repo.ListRepos(ctx, a); len(repos) != 0 {
		t.Errorf("repos of deleted account must be removed, got %+v", repos)
	}
	if accounts, _ := repo.List(ctx); len(accounts) != 1 || accounts[0].ID != b {
		t.Errorf("expected only account B, got %+v", accounts)
	}
}

func TestGitHubAccountRepo_SetSyncError_KeepsRepos(t *testing.T) {
	db := newTestDB(t)
	repo := NewGitHubAccountRepo(db)
	ctx := context.Background()

	id, _ := repo.Create(ctx, "Work", "tok")
	syncRepos(t, repo, id, "org/a")

	if err := repo.SetSyncError(ctx, id, "Токен недействителен или истёк"); err != nil {
		t.Fatalf("SetSyncError failed: %v", err)
	}
	acc, _ := repo.Get(ctx, id)
	if acc.SyncError != "Токен недействителен или истёк" || acc.ReposTotal != 1 {
		t.Errorf("unexpected account after sync error: %+v", acc)
	}

	selected, err := repo.ListSelectedRepos(ctx)
	if err != nil || len(selected) != 1 || selected[0].FullName != "org/a" {
		t.Errorf("unexpected selected repos: %+v, err %v", selected, err)
	}
}
