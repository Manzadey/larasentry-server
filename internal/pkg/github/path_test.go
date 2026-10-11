package github

import (
	"strings"
	"testing"
)

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name       string
		filePath   string
		pathPrefix string
		expected   string
	}{
		{
			name:       "Explicit prefix match",
			filePath:   "/var/www/html/app/Http/Controllers/OrderController.php",
			pathPrefix: "/var/www/html/",
			expected:   "app/Http/Controllers/OrderController.php",
		},
		{
			name:       "Explicit prefix match without trailing slash",
			filePath:   "/var/www/html/app/Http/Controllers/OrderController.php",
			pathPrefix: "/var/www/html",
			expected:   "app/Http/Controllers/OrderController.php",
		},
		{
			name:       "Windows backslashes with prefix",
			filePath:   `C:\laragon\www\my-app\app\Models\User.php`,
			pathPrefix: `C:\laragon\www\my-app`,
			expected:   "app/Models/User.php",
		},
		{
			name:       "Heuristic auto-detect app directory without prefix",
			filePath:   "/home/forge/site.com/app/Services/PaymentService.php",
			pathPrefix: "",
			expected:   "app/Services/PaymentService.php",
		},
		{
			name:       "Heuristic auto-detect routes directory without prefix",
			filePath:   "/var/task/routes/api.php",
			pathPrefix: "",
			expected:   "routes/api.php",
		},
		{
			name:       "Heuristic auto-detect config directory without prefix",
			filePath:   "/opt/app/config/database.php",
			pathPrefix: "",
			expected:   "config/database.php",
		},
		{
			name:       "Already relative path",
			filePath:   "app/Providers/RouteServiceProvider.php",
			pathPrefix: "",
			expected:   "app/Providers/RouteServiceProvider.php",
		},
		{
			name:       "Fallback to stripped leading slash if no marker matches",
			filePath:   "/src/custom/file.php",
			pathPrefix: "",
			expected:   "src/custom/file.php",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizePath(tt.filePath, tt.pathPrefix)
			if got != tt.expected {
				t.Errorf("NormalizePath(%q, %q) = %q; want %q", tt.filePath, tt.pathPrefix, got, tt.expected)
			}
		})
	}
}

func TestBuildBlobURL(t *testing.T) {
	tests := []struct {
		name     string
		repo     string
		ref      string
		relPath  string
		line     int
		expected string
	}{
		{
			name:     "Standard blob url with line",
			repo:     "Manzadey/larasentry-server",
			ref:      "v1.0.0",
			relPath:  "app/Http/Controllers/OrderController.php",
			line:     42,
			expected: "https://github.com/Manzadey/larasentry-server/blob/v1.0.0/app/Http/Controllers/OrderController.php#L42",
		},
		{
			name:     "Blob url with commit sha",
			repo:     "owner/repo",
			ref:      "a1b2c3d4e5",
			relPath:  "routes/web.php",
			line:     15,
			expected: "https://github.com/owner/repo/blob/a1b2c3d4e5/routes/web.php#L15",
		},
		{
			name:     "Empty ref defaults to main",
			repo:     "owner/repo",
			ref:      "",
			relPath:  "app/Models/User.php",
			line:     10,
			expected: "https://github.com/owner/repo/blob/main/app/Models/User.php#L10",
		},
		{
			name:     "Line 0 or negative omits line anchor",
			repo:     "owner/repo",
			ref:      "main",
			relPath:  "composer.json",
			line:     0,
			expected: "https://github.com/owner/repo/blob/main/composer.json",
		},
		{
			name:     "Handles full github url as repo gracefully",
			repo:     "https://github.com/owner/repo.git",
			ref:      "main",
			relPath:  "app/User.php",
			line:     5,
			expected: "https://github.com/owner/repo/blob/main/app/User.php#L5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildBlobURL(tt.repo, tt.ref, tt.relPath, tt.line)
			if got != tt.expected {
				t.Errorf("BuildBlobURL() = %q; want %q", got, tt.expected)
			}
		})
	}
}

func TestBuildIssueURL(t *testing.T) {
	title := "Error in OrderController"
	body := "Stack trace details here:\n#0 app/Order.php"
	got := BuildIssueURL("owner/repo", title, body)

	if !strings.HasPrefix(got, "https://github.com/owner/repo/issues/new?") {
		t.Errorf("unexpected issue URL prefix: %s", got)
	}
	if !strings.Contains(got, "title=Error+in+OrderController") && !strings.Contains(got, "title=Error%20in%20OrderController") {
		t.Errorf("issue URL missing encoded title: %s", got)
	}
}
