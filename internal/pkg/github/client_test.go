package github

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"server/internal/domain"
)

type roundTripperFunc func(req *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestClient_FetchFileContent(t *testing.T) {
	var requestCount int32

	transport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&requestCount, 1)

		auth := r.Header.Get("Authorization")
		if auth != "Bearer secret-token-xyz" {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Body:       io.NopCloser(strings.NewReader("Unauthorized")),
			}, nil
		}

		accept := r.Header.Get("Accept")
		if accept != "application/vnd.github.v3.raw" {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(strings.NewReader("Invalid accept header")),
			}, nil
		}

		if r.URL.Path == "/repos/owner/repo/contents/app/Models/User.php" && r.URL.Query().Get("ref") == "v1.0.0" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("<?php\n\nnamespace App\\Models;\n\nclass User {}")),
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("Not Found")),
		}, nil
	})

	httpClient := &http.Client{Transport: transport}
	client := NewClient(httpClient, "https://api.github.com", 5*time.Minute)
	ctx := context.Background()

	t.Run("Successful fetch and verify caching", func(t *testing.T) {
		atomic.StoreInt32(&requestCount, 0)

		content, err := client.FetchFileContent(ctx, "owner/repo", "secret-token-xyz", "v1.0.0", "app/Models/User.php")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if content != "<?php\n\nnamespace App\\Models;\n\nclass User {}" {
			t.Errorf("unexpected content: %s", content)
		}
		if atomic.LoadInt32(&requestCount) != 1 {
			t.Errorf("expected 1 HTTP request, got %d", atomic.LoadInt32(&requestCount))
		}

		cachedContent, err := client.FetchFileContent(ctx, "owner/repo", "secret-token-xyz", "v1.0.0", "app/Models/User.php")
		if err != nil {
			t.Fatalf("unexpected error on cached call: %v", err)
		}
		if cachedContent != content {
			t.Errorf("expected same content from cache")
		}
		if atomic.LoadInt32(&requestCount) != 1 {
			t.Errorf("expected request count to remain 1 due to cache, got %d", atomic.LoadInt32(&requestCount))
		}
	})

	t.Run("Unauthorized error", func(t *testing.T) {
		_, err := client.FetchFileContent(ctx, "owner/repo", "wrong-token", "v1.0.0", "app/Models/Admin.php")
		if err == nil {
			t.Fatalf("expected error for wrong token")
		}
	})

	t.Run("File not found error", func(t *testing.T) {
		_, err := client.FetchFileContent(ctx, "owner/repo", "secret-token-xyz", "v1.0.0", "app/NonExistent.php")
		if err == nil {
			t.Fatalf("expected error for non-existent file")
		}
	})
}

func TestResolveCodeSnippet_Hybrid(t *testing.T) {
	transport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10")),
		}, nil
	})

	httpClient := &http.Client{Transport: transport}
	client := NewClient(httpClient, "https://api.github.com", 5*time.Minute)
	ctx := context.Background()

	t.Run("Priority 1: Client snippet takes precedence", func(t *testing.T) {
		clientSnippet := &domain.CodeSnippet{
			File: "app/Services/Local.php",
			Line: 2,
			Lines: []domain.CodeLine{
				{Number: 1, Code: "local line 1", IsError: false},
				{Number: 2, Code: "local line 2", IsError: true},
			},
			Source: "client",
		}

		proj := &domain.Project{
			GitHubRepo:  "owner/repo",
			GitHubToken: "token",
		}
		inc := &domain.Incident{
			File:        "/var/www/html/app/Services/Local.php",
			Line:        2,
			CodeSnippet: clientSnippet,
		}

		snippet, err := ResolveCodeSnippet(ctx, client, proj, inc, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if snippet == nil {
			t.Fatalf("expected snippet to not be nil")
		}
		if snippet.Source != "client" {
			t.Errorf("expected source 'client', got %s", snippet.Source)
		}
		if snippet.GitHubURL != "https://github.com/owner/repo/blob/main/app/Services/Local.php#L2" {
			t.Errorf("expected enriched GitHubURL, got %s", snippet.GitHubURL)
		}
	})

	t.Run("Priority 2: GitHub API fetch when no client snippet", func(t *testing.T) {
		proj := &domain.Project{
			GitHubRepo:  "owner/repo",
			GitHubToken: "token",
			PathPrefix:  "/var/www/html",
		}
		inc := &domain.Incident{
			File:        "/var/www/html/app/Services/Payment.php",
			Line:        5,
			LastRelease: "v2.0.0",
		}

		snippet, err := ResolveCodeSnippet(ctx, client, proj, inc, 2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if snippet == nil {
			t.Fatalf("expected snippet from GitHub")
		}
		if snippet.Source != "github" {
			t.Errorf("expected source 'github', got %s", snippet.Source)
		}
		if snippet.GitHubURL != "https://github.com/owner/repo/blob/v2.0.0/app/Services/Payment.php#L5" {
			t.Errorf("unexpected GitHubURL: %s", snippet.GitHubURL)
		}
		if len(snippet.Lines) == 0 {
			t.Errorf("expected extracted code lines")
		}
	})

	t.Run("No GitHub repo and no client snippet returns nil safely", func(t *testing.T) {
		proj := &domain.Project{}
		inc := &domain.Incident{
			File: "/var/www/html/app/Services/Payment.php",
			Line: 5,
		}

		snippet, err := ResolveCodeSnippet(ctx, client, proj, inc, 2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if snippet != nil {
			t.Errorf("expected nil snippet when no repo configured")
		}
	})
}
