package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"server/internal/delivery/sse"
	"server/internal/domain"
	"server/internal/pkg/github"
	"server/internal/repository/sqlite"
	"server/internal/usecase"
)

type ghRoundTrip func(*http.Request) (*http.Response, error)

func (f ghRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newGitHubUITest(t *testing.T, ghStatus int) (*UIHandler, domain.GitHubAccountRepository) {
	t.Helper()
	db, err := sqlite.InitDB(filepath.Join(t.TempDir(), "ui.db"))
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	transport := ghRoundTrip(func(r *http.Request) (*http.Response, error) {
		body := `{}`
		if ghStatus == http.StatusOK && r.URL.Path == "/user" {
			body = `{"login":"octocat"}`
		}
		if ghStatus == http.StatusOK && r.URL.Path == "/user/repos" {
			body = `[{"full_name":"org/app","default_branch":"develop","private":true}]`
		}
		return &http.Response{StatusCode: ghStatus, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	client := github.NewClient(&http.Client{Transport: transport}, "https://api.github.com", time.Minute)

	repo := sqlite.NewGitHubAccountRepo(db)
	incident := &domain.Incident{ID: 1, ProjectID: 1, ProjectName: "Test Project", Status: domain.StatusUnresolved, FirstSeenAt: time.Now(), LastSeenAt: time.Now()}
	h, err := NewUIHandler(&mockProjectRepo{}, &mockIncidentRepo{incident: incident}, &mockReleaseRepo{}, sse.NewBroker())
	if err != nil {
		t.Fatalf("failed to create UIHandler: %v", err)
	}
	h.SetGitHubAccounts(repo, usecase.NewGitHubAccounts(repo, client))
	return h, repo
}

func TestHandleIndex_RendersSelectedRepoOptions(t *testing.T) {
	h, _ := newGitHubUITest(t, http.StatusOK)
	if _, err := h.githubService.Create(context.Background(), "Work", "ghp_supersecret"); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	rec := httptest.NewRecorder()
	h.HandleIndex(rec, httptest.NewRequest(http.MethodGet, "/ui", nil))

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	for _, want := range []string{`<optgroup label="Work (octocat)">`, `data-branch="develop"`, "org/app", `name="github_repo_id"`} {
		if !strings.Contains(body, want) {
			t.Errorf("expected index to contain %q", want)
		}
	}
	if strings.Contains(body, "ghp_supersecret") {
		t.Error("token must never be rendered")
	}
}

func TestGroupReposByAccount(t *testing.T) {
	groups := groupReposByAccount([]domain.GitHubRepo{
		{ID: 1, AccountID: 1, AccountName: "A", AccountLogin: "a-login", FullName: "a/one"},
		{ID: 2, AccountID: 1, AccountName: "A", AccountLogin: "a-login", FullName: "a/two"},
		{ID: 3, AccountID: 2, AccountName: "B", FullName: "b/one"},
	})

	if len(groups) != 2 || len(groups[0].Repos) != 2 || len(groups[1].Repos) != 1 {
		t.Fatalf("unexpected groups: %+v", groups)
	}
	if groups[0].Label != "A (a-login)" || groups[1].Label != "B" {
		t.Errorf("unexpected labels: %q, %q", groups[0].Label, groups[1].Label)
	}
}
