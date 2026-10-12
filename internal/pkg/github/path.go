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

// CleanRepoOwnerName приводит строку репозитория вида "https://github.com/owner/repo.git" или "owner/repo"
// к чистому "owner/repo".
func CleanRepoOwnerName(rawRepo string) string {
	repo := strings.TrimSpace(rawRepo)
	repo = strings.TrimPrefix(repo, "https://github.com/")
	repo = strings.TrimPrefix(repo, "http://github.com/")
	repo = strings.TrimPrefix(repo, "git@github.com:")
	repo = strings.TrimSuffix(repo, ".git")
	repo = strings.Trim(repo, "/")
	return repo
}

// NormalizePath срезает заданный префикс или сам находит корень Laravel-проекта,
// чтобы получить путь относительно репозитория (например, app/Http/Controllers/OrderController.php).
func NormalizePath(filePath, pathPrefix string) string {
	// Пути из Windows приходят с обратными слэшами
	p := strings.ReplaceAll(strings.TrimSpace(filePath), "\\", "/")
	prefix := strings.ReplaceAll(strings.TrimSpace(pathPrefix), "\\", "/")

	if prefix != "" {
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		if strings.HasPrefix(p, prefix) {
			return strings.TrimPrefix(p, prefix)
		}
		// Префикс может совпасть и без завершающего слэша
		prefixNoSlash := strings.TrimSuffix(prefix, "/")
		if strings.HasPrefix(p, prefixNoSlash) {
			res := strings.TrimPrefix(p, prefixNoSlash)
			return strings.TrimPrefix(res, "/")
		}
	}

	// Эвристика: ищем известные маркеры корневых каталогов Laravel
	for _, marker := range defaultLaravelMarkers {
		if idx := strings.Index(p, marker); idx != -1 {
			return p[idx:]
		}
		// Путь может начинаться с маркера без ведущего слэша, например "app/"
		if strings.HasPrefix(p, marker) {
			return p
		}
	}

	return strings.TrimPrefix(p, "/")
}

// BuildBlobURL собирает прямую ссылку на файл и строку в GitHub.
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

// BuildIssueURL собирает ссылку на создание issue в GitHub с заполненными заголовком и текстом.
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
