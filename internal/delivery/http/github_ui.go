package http

import (
	"context"
	"log"
	"net/http"
	"strconv"

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

type githubAccountView struct {
	domain.GitHubAccount
	Repos []domain.GitHubRepo
}

func (h *UIHandler) HandleGitHubPage(w http.ResponseWriter, r *http.Request) {
	h.renderGitHubPage(w, r, http.StatusOK, "", "")
}

func (h *UIHandler) renderGitHubPage(w http.ResponseWriter, r *http.Request, status int, errMsg, formName string) {
	ctx := r.Context()
	accounts, err := h.githubAccounts.List(ctx)
	if err != nil {
		log.Printf("Error listing GitHub accounts: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	views := make([]githubAccountView, 0, len(accounts))
	for _, acc := range accounts {
		repos, err := h.githubAccounts.ListRepos(ctx, acc.ID)
		if err != nil {
			log.Printf("Error listing repos of GitHub account %d: %v", acc.ID, err)
		}
		views = append(views, githubAccountView{GitHubAccount: acc, Repos: repos})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	projects, err := h.projectRepo.GetAllProjects(ctx)
	if err != nil {
		log.Printf("Error getting projects: %v", err)
	}
	data := map[string]any{
		"Accounts":         views,
		"Error":            errMsg,
		"FormName":         formName,
		"GitHubPage":       true,
		"Projects":         projects,
		"GitHubRepoGroups": h.selectedRepoGroups(ctx),
	}
	if err := h.tmpl.ExecuteTemplate(w, "github.html", data); err != nil {
		log.Printf("Error rendering github template: %v", err)
	}
}

func (h *UIHandler) HandleCreateGitHubAccount(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}
	name := r.FormValue("name")
	if _, err := h.githubService.Create(r.Context(), name, r.FormValue("token")); err != nil {
		h.renderGitHubPage(w, r, http.StatusUnprocessableEntity, err.Error(), name)
		return
	}
	http.Redirect(w, r, "/ui/github", http.StatusSeeOther)
}

func (h *UIHandler) HandleSyncGitHubAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := accountIDFromPath(w, r)
	if !ok {
		return
	}
	// Ошибка уже записана в sync_error и залогирована в usecase, страница её покажет
	_ = h.githubService.Sync(r.Context(), id)
	http.Redirect(w, r, "/ui/github", http.StatusSeeOther)
}

func (h *UIHandler) HandleDeleteGitHubAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := accountIDFromPath(w, r)
	if !ok {
		return
	}
	if err := h.githubAccounts.Delete(r.Context(), id); err != nil {
		log.Printf("Error deleting GitHub account %d: %v", id, err)
		http.Error(w, "Failed to delete account", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/ui/github", http.StatusSeeOther)
}

func (h *UIHandler) HandleSaveGitHubRepos(w http.ResponseWriter, r *http.Request) {
	id, ok := accountIDFromPath(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	var selected []int64
	for _, v := range r.Form["selected"] {
		if repoID, err := strconv.ParseInt(v, 10, 64); err == nil {
			selected = append(selected, repoID)
		}
	}
	importAll := r.FormValue("import_all") == "1"

	if err := h.githubAccounts.SetSelection(r.Context(), id, importAll, selected); err != nil {
		log.Printf("Error saving repo selection for GitHub account %d: %v", id, err)
		http.Error(w, "Failed to save selection", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/ui/github", http.StatusSeeOther)
}

func accountIDFromPath(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid account ID", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

// selectedRepoGroups готовит данные для модалки привязки репозитория, общей для страниц инцидентов и аккаунтов.
func (h *UIHandler) selectedRepoGroups(ctx context.Context) []repoGroup {
	if h.githubAccounts == nil {
		return nil
	}
	repos, err := h.githubAccounts.ListSelectedRepos(ctx)
	if err != nil {
		log.Printf("Error getting GitHub repos: %v", err)
	}
	return groupReposByAccount(repos)
}
