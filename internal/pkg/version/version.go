package version

import (
	"strings"

	"golang.org/x/mod/semver"
)

// NormalizeSemver добавляет к версии префикс 'v', нужный для сравнения через semver.
func NormalizeSemver(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", false
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if semver.IsValid(v) {
		return v, true
	}
	return "", false
}

// Compare возвращает:
//
//	-1, если v1 < v2
//	 0, если v1 == v2
//	+1, если v1 > v2
//
// Если обе версии — валидный SemVer, используется semver.Compare.
// Иначе вызывается fallback, если он передан.
func Compare(v1, v2 string, fallback func(v1, v2 string) int) int {
	v1 = strings.TrimSpace(v1)
	v2 = strings.TrimSpace(v2)

	if v1 == v2 {
		return 0
	}

	norm1, ok1 := NormalizeSemver(v1)
	norm2, ok2 := NormalizeSemver(v2)

	if ok1 && ok2 {
		return semver.Compare(norm1, norm2)
	}

	if fallback != nil {
		return fallback(v1, v2)
	}

	return strings.Compare(v1, v2)
}
