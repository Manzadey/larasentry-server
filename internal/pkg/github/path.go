package github

import (
	"fmt"
	"net/url"
	"strings"
)

var defaultLaravelMarkers = []string{
	"app/Http/",
	"app/Models/",
	"app/Services/",
	"app/Console/",
	"app/Providers/",
	"app/Exceptions/",
	"app/Jobs/",
	"app/Listeners/",
	"app/Events/",
	"app/Mail/",
	"routes/",
	"config/",
	"database/",
	"resources/",
	"bootstrap/",
	"public/",
	"tests/",
	"app/",
}

// CleanRepoOwnerName normalizes a repo string like "https://github.com/owner/repo.git" or "owner/repo"
// into clean "owner/repo".
func CleanRepoOwnerName(rawRepo string) string {
	repo := strings.TrimSpace(rawRepo)
	repo = strings.TrimPrefix(repo, "https://github.com/")
	repo = strings.TrimPrefix(repo, "http://github.com/")
	repo = strings.TrimPrefix(repo, "git@github.com:")
	repo = strings.TrimSuffix(repo, ".git")
	repo = strings.Trim(repo, "/")
	return repo
}

// NormalizePath strips custom prefix or auto-detects Laravel project root
// to produce a repository-relative path (e.g. app/Http/Controllers/OrderController.php).
func NormalizePath(filePath, pathPrefix string) string {
	// Normalize backslashes (Windows) to forward slashes
	p := strings.ReplaceAll(strings.TrimSpace(filePath), "\\", "/")
	prefix := strings.ReplaceAll(strings.TrimSpace(pathPrefix), "\\", "/")

	// 1. If explicit pathPrefix is configured, try stripping it
	if prefix != "" {
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		if strings.HasPrefix(p, prefix) {
			return strings.TrimPrefix(p, prefix)
		}
		// Also try without trailing slash just in case
		prefixNoSlash := strings.TrimSuffix(prefix, "/")
		if strings.HasPrefix(p, prefixNoSlash) {
			res := strings.TrimPrefix(p, prefixNoSlash)
			return strings.TrimPrefix(res, "/")
		}
	}

	// 2. Heuristic: search for known Laravel root directory markers
	for _, marker := range defaultLaravelMarkers {
		if idx := strings.Index(p, marker); idx != -1 {
			return p[idx:]
		}
		// Also check leading marker without slash, e.g. "app/"
		if strings.HasPrefix(p, marker) {
			return p
		}
	}

	// 3. Fallback: just strip leading slashes
	return strings.TrimPrefix(p, "/")
}

// BuildBlobURL constructs the full direct GitHub URL to a file and line number.
func BuildBlobURL(rawRepo, ref, relativePath string, line int) string {
	repo := CleanRepoOwnerName(rawRepo)
	if repo == "" {
		return ""
	}

	r := strings.TrimSpace(ref)
	if r == "" {
		r = "main"
	}

	rel := strings.TrimPrefix(strings.TrimSpace(relativePath), "/")
	urlStr := fmt.Sprintf("https://github.com/%s/blob/%s/%s", repo, r, rel)
	if line > 0 {
		urlStr = fmt.Sprintf("%s#L%d", urlStr, line)
	}
	return urlStr
}

// BuildIssueURL constructs a pre-filled GitHub new issue URL with title and body.
func BuildIssueURL(rawRepo, title, body string) string {
	repo := CleanRepoOwnerName(rawRepo)
	if repo == "" {
		return ""
	}

	base := fmt.Sprintf("https://github.com/%s/issues/new", repo)
	params := url.Values{}
	if title != "" {
		params.Set("title", title)
	}
	if body != "" {
		params.Set("body", body)
	}

	return fmt.Sprintf("%s?%s", base, params.Encode())
}
