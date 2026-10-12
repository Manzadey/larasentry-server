package usecase

import (
	"context"
	"errors"
	"log"
	"strings"

	"server/internal/domain"
	"server/internal/pkg/github"
)

type GitHubAccounts struct {
	repo   domain.GitHubAccountRepository
	client *github.Client
}

func NewGitHubAccounts(repo domain.GitHubAccountRepository, client *github.Client) *GitHubAccounts {
	return &GitHubAccounts{repo: repo, client: client}
}

// Create сохраняет аккаунт только с рабочим токеном, иначе в списке копились бы мёртвые записи.
func (s *GitHubAccounts) Create(ctx context.Context, name, token string) (int64, error) {
	name, token = strings.TrimSpace(name), strings.TrimSpace(token)
	if name == "" || token == "" {
		return 0, errors.New("Укажите имя аккаунта и токен")
	}
	if _, err := s.client.GetUser(ctx, token); err != nil {
		return 0, err
	}
	id, err := s.repo.Create(ctx, name, token)
	if err != nil {
		return 0, err
	}
	// Ошибку списка репозиториев не возвращаем: она уже в sync_error и видна в карточке аккаунта
	_ = s.Sync(ctx, id)
	return id, nil
}

func (s *GitHubAccounts) Sync(ctx context.Context, id int64) error {
	acc, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}

	login, repos, err := s.fetch(ctx, acc.Token)
	if err != nil {
		log.Printf("[github] sync account %d: %v", id, err)
		if saveErr := s.repo.SetSyncError(ctx, id, err.Error()); saveErr != nil {
			log.Printf("[github] save sync error for account %d: %v", id, saveErr)
		}
		return err
	}
	return s.repo.ApplySync(ctx, id, login, repos)
}

func (s *GitHubAccounts) fetch(ctx context.Context, token string) (string, []domain.GitHubRepo, error) {
	login, err := s.client.GetUser(ctx, token)
	if err != nil {
		return "", nil, err
	}
	repos, err := s.client.ListUserRepos(ctx, token)
	return login, repos, err
}
