package usecase

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"server/internal/domain"
	"server/internal/pkg/github"
	"server/internal/repository/sqlite"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeGitHub отвечает status на всё, кроме 200: тогда отдаёт логин и два репозитория.
func fakeGitHub(status *int) *github.Client {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{}`
		if *status == http.StatusOK {
			switch r.URL.Path {
			case "/user":
				body = `{"login":"octocat"}`
			case "/user/repos":
				body = `[{"full_name":"org/a","default_branch":"main"},{"full_name":"org/b","default_branch":"dev"}]`
			}
		}
		return &http.Response{StatusCode: *status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	return github.NewClient(&http.Client{Transport: transport}, "https://api.github.com", time.Minute)
}

func newGitHubAccountsTest(t *testing.T, status *int) (*GitHubAccounts, domain.GitHubAccountRepository) {
	t.Helper()
	db, err := sqlite.InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	repo := sqlite.NewGitHubAccountRepo(db)
	return NewGitHubAccounts(repo, fakeGitHub(status)), repo
}

func TestGitHubAccounts_Create_SyncsRepos(t *testing.T) {
	status := http.StatusOK
	svc, repo := newGitHubAccountsTest(t, &status)
	ctx := context.Background()

	id, err := svc.Create(ctx, "  Work  ", "  ghp_tok  ")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	acc, _ := repo.Get(ctx, id)
	if acc.Name != "Work" || acc.Token != "ghp_tok" || acc.Login != "octocat" || acc.ReposTotal != 2 {
		t.Errorf("unexpected account: %+v", acc)
	}
}

func TestGitHubAccounts_Create_InvalidTokenNotSaved(t *testing.T) {
	status := http.StatusUnauthorized
	svc, repo := newGitHubAccountsTest(t, &status)
	ctx := context.Background()

	_, err := svc.Create(ctx, "Work", "bad")
	if err == nil || !strings.Contains(err.Error(), "Токен недействителен") {
		t.Fatalf("expected invalid token error, got %v", err)
	}
	if accounts, _ := repo.List(ctx); len(accounts) != 0 {
		t.Errorf("account with invalid token must not be saved: %+v", accounts)
	}
}

func TestGitHubAccounts_Create_RequiresNameAndToken(t *testing.T) {
	status := http.StatusOK
	svc, _ := newGitHubAccountsTest(t, &status)

	if _, err := svc.Create(context.Background(), " ", "tok"); err == nil {
		t.Error("expected error for empty name")
	}
	if _, err := svc.Create(context.Background(), "Work", " "); err == nil {
		t.Error("expected error for empty token")
	}
}

func TestGitHubAccounts_Sync_FailureKeepsRepos(t *testing.T) {
	status := http.StatusOK
	svc, repo := newGitHubAccountsTest(t, &status)
	ctx := context.Background()

	id, err := svc.Create(ctx, "Work", "tok")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	status = http.StatusInternalServerError
	if err := svc.Sync(ctx, id); err == nil {
		t.Fatal("expected sync error")
	}

	acc, _ := repo.Get(ctx, id)
	if !strings.Contains(acc.SyncError, "500") {
		t.Errorf("expected sync_error with status, got %q", acc.SyncError)
	}
	if acc.ReposTotal != 2 {
		t.Errorf("repos must survive failed sync, got %d", acc.ReposTotal)
	}

	status = http.StatusOK
	if err := svc.Sync(ctx, id); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
	acc, _ = repo.Get(ctx, id)
	if acc.SyncError != "" {
		t.Errorf("successful sync must clear sync_error, got %q", acc.SyncError)
	}
}

type failingApplyRepo struct {
	domain.GitHubAccountRepository
}

func (f failingApplyRepo) ApplySync(ctx context.Context, accountID int64, login string, repos []domain.GitHubRepo) error {
	return errors.New("database is locked")
}

func TestGitHubAccounts_Sync_StoreFailureRecorded(t *testing.T) {
	status := http.StatusOK
	svc, repo := newGitHubAccountsTest(t, &status)
	ctx := context.Background()
	id, err := svc.Create(ctx, "Work", "tok")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	failing := NewGitHubAccounts(failingApplyRepo{repo}, svc.client)
	if err := failing.Sync(ctx, id); err == nil {
		t.Fatal("expected sync error")
	}

	acc, _ := repo.Get(ctx, id)
	if !strings.Contains(acc.SyncError, "database is locked") {
		t.Errorf("store failure must be visible in sync_error, got %q", acc.SyncError)
	}
}
