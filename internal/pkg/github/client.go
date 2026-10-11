package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"server/internal/domain"
)

type cacheEntry struct {
	content   string
	expiresAt time.Time
}

// Client interacts with GitHub REST API and caches file contents.
type Client struct {
	httpClient *http.Client
	baseURL    string
	defaultTTL time.Duration
	mu         sync.RWMutex
	cache      map[string]cacheEntry
}

// NewClient initializes a new GitHub API client.
func NewClient(httpClient *http.Client, baseURL string, defaultTTL time.Duration) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if defaultTTL <= 0 {
		defaultTTL = 15 * time.Minute
	}

	return &Client{
		httpClient: httpClient,
		baseURL:    baseURL,
		defaultTTL: defaultTTL,
		cache:      make(map[string]cacheEntry),
	}
}

// FetchFileContent retrieves the raw text of a file from a repository at a given ref/commit.
func (c *Client) FetchFileContent(ctx context.Context, repo, token, ref, path string) (string, error) {
	cleanRepo := CleanRepoOwnerName(repo)
	if cleanRepo == "" {
		return "", fmt.Errorf("empty or invalid github repository: %s", repo)
	}

	r := strings.TrimSpace(ref)
	if r == "" {
		r = "main"
	}

	cleanPath := strings.TrimPrefix(strings.TrimSpace(path), "/")
	if cleanPath == "" {
		return "", fmt.Errorf("empty file path")
	}

	cacheKey := fmt.Sprintf("%s:%s:%s", cleanRepo, r, cleanPath)

	// Check cache
	c.mu.RLock()
	entry, found := c.cache[cacheKey]
	c.mu.RUnlock()

	if found && time.Now().Before(entry.expiresAt) {
		return entry.content, nil
	}

	// Prepare request to GitHub Contents API
	// GET /repos/{owner}/{repo}/contents/{path}?ref={ref}
	reqURL := fmt.Sprintf("%s/repos/%s/contents/%s?ref=%s", c.baseURL, cleanRepo, cleanPath, url.QueryEscape(r))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create github request: %w", err)
	}

	// Request raw file content directly
	req.Header.Set("Accept", "application/vnd.github.v3.raw")
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to execute github request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("file not found on github: %s (ref %s)", cleanPath, r)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("github api unauthorized (check github_token)")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github api error with status %d", resp.StatusCode)
	}

	// Limit read to 2MB to prevent OOM
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	content := string(body)

	// Calculate cache duration: commit SHAs are immutable, cache for 24h
	ttl := c.defaultTTL
	if len(r) == 40 || strings.HasPrefix(r, "v") {
		ttl = 24 * time.Hour
	}

	c.mu.Lock()
	c.cache[cacheKey] = cacheEntry{
		content:   content,
		expiresAt: time.Now().Add(ttl),
	}
	c.mu.Unlock()

	return content, nil
}

// ResolveCodeSnippet resolves code snippet using hybrid priority:
// 1. If Incident already contains CodeSnippet from client, return it (enriching with GitHub link if repo configured).
// 2. If Project has GitHub repository configured, fetch file from GitHub and extract context lines.
// 3. Otherwise return nil.
func ResolveCodeSnippet(
	ctx context.Context,
	client *Client,
	proj *domain.Project,
	inc *domain.Incident,
	radius int,
) (*domain.CodeSnippet, error) {
	if inc == nil {
		return nil, nil
	}

	// 1. Priority 1: Client snippet already exists
	if inc.CodeSnippet != nil && len(inc.CodeSnippet.Lines) > 0 {
		snippet := *inc.CodeSnippet
		if snippet.Source == "" {
			snippet.Source = "client"
		}
		if snippet.File == "" {
			snippet.File = inc.File
		}
		if snippet.Line == 0 {
			snippet.Line = inc.Line
		}
		if proj != nil && strings.TrimSpace(proj.GitHubRepo) != "" && snippet.GitHubURL == "" {
			ref := strings.TrimSpace(inc.LastRelease)
			if ref == "" {
				ref = strings.TrimSpace(proj.GitHubBranch)
			}
			if ref == "" {
				ref = "main"
			}
			relPath := NormalizePath(snippet.File, proj.PathPrefix)
			snippet.GitHubURL = BuildBlobURL(proj.GitHubRepo, ref, relPath, inc.Line)
		}
		return &snippet, nil
	}

	// 2. Priority 2: GitHub API fetch
	if proj == nil || strings.TrimSpace(proj.GitHubRepo) == "" {
		return nil, nil
	}

	ref := strings.TrimSpace(inc.LastRelease)
	if ref == "" {
		ref = strings.TrimSpace(proj.GitHubBranch)
	}
	if ref == "" {
		ref = "main"
	}

	relPath := NormalizePath(inc.File, proj.PathPrefix)
	blobURL := BuildBlobURL(proj.GitHubRepo, ref, relPath, inc.Line)

	if client == nil {
		return &domain.CodeSnippet{
			File:      relPath,
			Line:      inc.Line,
			GitHubURL: blobURL,
			Source:    "github",
		}, nil
	}

	content, err := client.FetchFileContent(ctx, proj.GitHubRepo, proj.GitHubToken, ref, relPath)
	if err != nil {
		// Even if fetching content fails, we still return the snippet shell with the direct GitHub URL
		// so the developer can click "Open in GitHub"
		return &domain.CodeSnippet{
			File:      relPath,
			Line:      inc.Line,
			GitHubURL: blobURL,
			Source:    "github",
		}, nil
	}

	lines := ExtractSnippet(content, inc.Line, radius)
	return &domain.CodeSnippet{
		File:      relPath,
		Line:      inc.Line,
		Lines:     lines,
		GitHubURL: blobURL,
		Source:    "github",
	}, nil
}
