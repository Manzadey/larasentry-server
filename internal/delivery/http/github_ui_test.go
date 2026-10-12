package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
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

func postForm(h http.HandlerFunc, path, id string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if id != "" {
		req.SetPathValue("id", id)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestGitHubPage_NeverRendersToken(t *testing.T) {
	h, _ := newGitHubUITest(t, http.StatusOK)
	if _, err := h.githubService.Create(context.Background(), "Work", "ghp_supersecret"); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	rec := httptest.NewRecorder()
	h.HandleGitHubPage(rec, httptest.NewRequest(http.MethodGet, "/ui/github", nil))

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	for _, want := range []string{"Work", "@octocat", "org/app", "Выбрано 1 из 1"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected page to contain %q", want)
		}
	}
	if strings.Contains(body, "ghp_supersecret") {
		t.Error("token must never be rendered")
	}
}

func TestCreateGitHubAccount_InvalidTokenShowsError(t *testing.T) {
	h, repo := newGitHubUITest(t, http.StatusUnauthorized)

	rec := postForm(h.HandleCreateGitHubAccount, "/ui/github/accounts", "", url.Values{"name": {"Work"}, "token": {"bad"}})

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Токен недействителен") || !strings.Contains(body, `value="Work"`) {
		t.Errorf("expected error and preserved name in body")
	}
	if accounts, _ := repo.List(context.Background()); len(accounts) != 0 {
		t.Errorf("account must not be saved: %+v", accounts)
	}
}

func TestCreateGitHubAccount_Success(t *testing.T) {
	h, repo := newGitHubUITest(t, http.StatusOK)

	rec := postForm(h.HandleCreateGitHubAccount, "/ui/github/accounts", "", url.Values{"name": {"Work"}, "token": {"tok"}})

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/ui/github" {
		t.Fatalf("expected redirect to /ui/github, got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if accounts, _ := repo.List(context.Background()); len(accounts) != 1 || accounts[0].ReposTotal != 1 {
		t.Errorf("expected one synced account, got %+v", accounts)
	}
}

func TestSaveGitHubRepos_UpdatesSelection(t *testing.T) {
	h, repo := newGitHubUITest(t, http.StatusOK)
	ctx := context.Background()
	id, _ := h.githubService.Create(ctx, "Work", "tok")
	idStr := strconv.FormatInt(id, 10)

	rec := postForm(h.HandleSaveGitHubRepos, "/ui/github/accounts/"+idStr+"/repos", idStr, url.Values{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", rec.Code)
	}
	acc, _ := repo.Get(ctx, id)
	repos, _ := repo.ListRepos(ctx, id)
	if acc.ImportAll || repos[0].Selected {
		t.Errorf("expected import_all off and nothing selected, got %+v / %+v", acc, repos)
	}

	form := url.Values{"selected": {strconv.FormatInt(repos[0].ID, 10)}}
	postForm(h.HandleSaveGitHubRepos, "/ui/github/accounts/"+idStr+"/repos", idStr, form)
	repos, _ = repo.ListRepos(ctx, id)
	if !repos[0].Selected {
		t.Errorf("expected repo to be selected")
	}
}

func TestSyncAndDeleteGitHubAccount(t *testing.T) {
	h, repo := newGitHubUITest(t, http.StatusOK)
	ctx := context.Background()
	id, _ := h.githubService.Create(ctx, "Work", "tok")
	idStr := strconv.FormatInt(id, 10)

	if rec := postForm(h.HandleSyncGitHubAccount, "/ui/github/accounts/"+idStr+"/sync", idStr, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("sync: expected 303, got %d", rec.Code)
	}
	if rec := postForm(h.HandleDeleteGitHubAccount, "/ui/github/accounts/"+idStr+"/delete", idStr, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("delete: expected 303, got %d", rec.Code)
	}
	if accounts, _ := repo.List(ctx); len(accounts) != 0 {
		t.Errorf("expected no accounts after delete, got %+v", accounts)
	}
}

func TestGitHubAccountHandlers_InvalidID(t *testing.T) {
	h, _ := newGitHubUITest(t, http.StatusOK)

	if rec := postForm(h.HandleDeleteGitHubAccount, "/ui/github/accounts/abc/delete", "abc", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid id, got %d", rec.Code)
	}
}
