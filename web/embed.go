package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
)

// TemplatesFS embeds all html templates for the UI
//
//go:embed templates/*
var TemplatesFS embed.FS

//go:embed static
var staticFS embed.FS

var staticVersions = hashStatic()

func hashStatic() map[string]string {
	versions := map[string]string{}
	entries, err := fs.ReadDir(staticFS, "static")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		data, err := staticFS.ReadFile("static/" + e.Name())
		if err != nil {
			panic(err)
		}
		sum := sha256.Sum256(data)
		versions[e.Name()] = hex.EncodeToString(sum[:4])
	}
	return versions
}

// StaticURL добавляет к URL хеш содержимого: после деплоя меняется адрес, и браузер не берёт старый файл из кэша.
// Ошибка на неизвестное имя нужна, чтобы опечатка в шаблоне падала при рендере, а не давала тихий 404.
func StaticURL(name string) (string, error) {
	v, ok := staticVersions[name]
	if !ok {
		return "", fmt.Errorf("static file %q not found", name)
	}
	return "/ui/static/" + name + "?v=" + v, nil
}

// StaticHandler раздаёт /ui/static/. Кэш на год безопасен, потому что StaticURL меняет адрес при каждом изменении файла.
func StaticHandler() http.Handler {
	files := http.StripPrefix("/ui", http.FileServerFS(staticFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		files.ServeHTTP(w, r)
	})
}
