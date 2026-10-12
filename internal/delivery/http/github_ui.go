package http

import (
	"server/internal/domain"
	"server/internal/usecase"
)

type repoGroup struct {
	accountID int64
	Label     string
	Repos     []domain.GitHubRepo
}

// groupReposByAccount ожидает репозитории, отсортированные по аккаунту (как отдаёт ListSelectedRepos).
func groupReposByAccount(repos []domain.GitHubRepo) []repoGroup {
	var groups []repoGroup
	for _, r := range repos {
		if n := len(groups); n == 0 || groups[n-1].accountID != r.AccountID {
			label := r.AccountName
			if r.AccountLogin != "" {
				label += " (" + r.AccountLogin + ")"
			}
			groups = append(groups, repoGroup{accountID: r.AccountID, Label: label})
		}
		groups[len(groups)-1].Repos = append(groups[len(groups)-1].Repos, r)
	}
	return groups
}

func (h *UIHandler) SetGitHubAccounts(repo domain.GitHubAccountRepository, svc *usecase.GitHubAccounts) {
	h.githubAccounts = repo
	h.githubService = svc
}
