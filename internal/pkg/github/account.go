package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"server/internal/domain"
)

const (
	reposPerPage = 100
	// Обрезать список нельзя: синхронизация удалила бы «пропавшие» репозитории и отвязала проекты.
	maxRepoPages = 50
)

// GetUser проверяет токен и возвращает логин его владельца.
func (c *Client) GetUser(ctx context.Context, token string) (string, error) {
	var user struct {
		Login string `json:"login"`
	}
	if err := c.getJSON(ctx, token, "/user", &user); err != nil {
		return "", err
	}
	return user.Login, nil
}

func (c *Client) ListUserRepos(ctx context.Context, token string) ([]domain.GitHubRepo, error) {
	var repos []domain.GitHubRepo
	for page := 1; page <= maxRepoPages; page++ {
		// Отдельная структура: поле id из ответа GitHub не должно попасть в наш GitHubRepo.ID
		var batch []struct {
			FullName      string `json:"full_name"`
			DefaultBranch string `json:"default_branch"`
			Private       bool   `json:"private"`
		}
		path := fmt.Sprintf("/user/repos?per_page=%d&page=%d", reposPerPage, page)
		if err := c.getJSON(ctx, token, path, &batch); err != nil {
			return nil, err
		}
		for _, r := range batch {
			repos = append(repos, domain.GitHubRepo{FullName: r.FullName, DefaultBranch: r.DefaultBranch, Private: r.Private})
		}
		if len(batch) < reposPerPage {
			return repos, nil
		}
	}
	return nil, fmt.Errorf("У аккаунта больше %d репозиториев, синхронизация не поддерживается", maxRepoPages*reposPerPage)
}

func (c *Client) getJSON(ctx context.Context, token, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("failed to create github request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("Запрос к GitHub не удался: %w", err)
	}
	defer resp.Body.Close()

	if err := statusError(resp); err != nil {
		return err
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(out)
}

// statusError возвращает текст, который показывается пользователю в карточке аккаунта.
func statusError(resp *http.Response) error {
	code := resp.StatusCode
	switch {
	case code >= 200 && code < 300:
		return nil
	case code == http.StatusUnauthorized:
		return errors.New("Токен недействителен или истёк")
	case (code == http.StatusForbidden || code == http.StatusTooManyRequests) && resp.Header.Get("X-RateLimit-Remaining") == "0":
		reset, _ := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64)
		return fmt.Errorf("Лимит GitHub API исчерпан, повторите после %s", time.Unix(reset, 0).Format("15:04"))
	case code == http.StatusForbidden || code == http.StatusNotFound:
		return errors.New("У токена нет прав на чтение списка репозиториев")
	default:
		return fmt.Errorf("GitHub API вернул статус %d", code)
	}
}
