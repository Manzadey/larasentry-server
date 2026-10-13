package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fakeGitHub(handle func(r *http.Request) *http.Response) *Client {
	transport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return handle(r), nil
	})
	return NewClient(&http.Client{Transport: transport}, "https://api.github.com", time.Minute)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func reposPage(count, offset int) string {
	items := make([]string, count)
	for i := range items {
		n := offset + i
		items[i] = fmt.Sprintf(`{"id":%d,"full_name":"org/repo-%d","default_branch":"main","private":%t}`, 1000+n, n, n%2 == 0)
	}
	return "[" + strings.Join(items, ",") + "]"
}

func TestClient_GetUser(t *testing.T) {
	client := fakeGitHub(func(r *http.Request) *http.Response {
		if r.URL.Path != "/user" || r.Header.Get("Authorization") != "Bearer tok" {
			return jsonResponse(http.StatusUnauthorized, `{}`)
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			return jsonResponse(http.StatusBadRequest, `{}`)
		}
		return jsonResponse(http.StatusOK, `{"login":"octocat"}`)
	})

	login, err := client.GetUser(context.Background(), "tok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if login != "octocat" {
		t.Errorf("expected login octocat, got %q", login)
	}
}

func TestClient_GetUser_Unauthorized(t *testing.T) {
	client := fakeGitHub(func(r *http.Request) *http.Response {
		return jsonResponse(http.StatusUnauthorized, `{"message":"Bad credentials"}`)
	})

	_, err := client.GetUser(context.Background(), "bad")
	if err == nil || !strings.Contains(err.Error(), "Токен недействителен") {
		t.Fatalf("expected invalid token error, got %v", err)
	}
	if strings.Contains(err.Error(), "bad") {
		t.Errorf("error must not contain the token: %v", err)
	}
}

func TestClient_GetUser_RateLimited(t *testing.T) {
	client := fakeGitHub(func(r *http.Request) *http.Response {
		resp := jsonResponse(http.StatusForbidden, `{}`)
		resp.Header.Set("X-RateLimit-Remaining", "0")
		resp.Header.Set("X-RateLimit-Reset", "1700000000")
		return resp
	})

	_, err := client.GetUser(context.Background(), "tok")
	if err == nil || !strings.Contains(err.Error(), "Лимит GitHub API исчерпан") {
		t.Fatalf("expected rate limit error, got %v", err)
	}
}

func TestClient_GetUser_Forbidden(t *testing.T) {
	client := fakeGitHub(func(r *http.Request) *http.Response {
		return jsonResponse(http.StatusForbidden, `{}`)
	})

	_, err := client.GetUser(context.Background(), "tok")
	if err == nil || !strings.Contains(err.Error(), "нет прав") {
		t.Fatalf("expected forbidden error, got %v", err)
	}
}

func TestClient_ListUserRepos_Paginates(t *testing.T) {
	var calls int32
	client := fakeGitHub(func(r *http.Request) *http.Response {
		atomic.AddInt32(&calls, 1)
		if r.URL.Path != "/user/repos" || r.URL.Query().Get("per_page") != "100" {
			return jsonResponse(http.StatusNotFound, `{}`)
		}
		switch r.URL.Query().Get("page") {
		case "1":
			return jsonResponse(http.StatusOK, reposPage(100, 0))
		case "2":
			return jsonResponse(http.StatusOK, reposPage(1, 100))
		}
		return jsonResponse(http.StatusInternalServerError, `{}`)
	})

	repos, err := client.ListUserRepos(context.Background(), "tok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 101 {
		t.Fatalf("expected 101 repos, got %d", len(repos))
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 requests, got %d", calls)
	}
	if repos[100].FullName != "org/repo-100" || repos[100].DefaultBranch != "main" {
		t.Errorf("unexpected last repo: %+v", repos[100])
	}
	if !repos[0].Private || repos[1].Private {
		t.Errorf("private flag not decoded: %+v %+v", repos[0], repos[1])
	}
	if repos[0].ID != 0 {
		t.Errorf("GitHub id must not leak into our ID, got %d", repos[0].ID)
	}
}

func TestClient_ListUserRepos_TooManyRepos(t *testing.T) {
	client := fakeGitHub(func(r *http.Request) *http.Response {
		return jsonResponse(http.StatusOK, reposPage(100, 0))
	})

	repos, err := client.ListUserRepos(context.Background(), "tok")
	if err == nil || !strings.Contains(err.Error(), "больше 5000") {
		t.Fatalf("expected too-many-repos error, got %v", err)
	}
	if repos != nil {
		t.Errorf("expected nil repos on error, got %d", len(repos))
	}
}
