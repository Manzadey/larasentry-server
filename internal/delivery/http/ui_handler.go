package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"server/internal/delivery/sse"
	"server/internal/domain"
	"server/internal/pkg/github"
	"server/internal/usecase"
	"server/web"
)

type UIHandler struct {
	projectRepo  domain.ProjectRepository
	incidentRepo domain.IncidentRepository
	releaseRepo  domain.ReleaseRepository
	sseBroker    *sse.Broker
	githubClient *github.Client
	tmpl         *template.Template

	githubAccounts domain.GitHubAccountRepository
	githubService  *usecase.GitHubAccounts
}

func NewUIHandler(
	projectRepo domain.ProjectRepository,
	incidentRepo domain.IncidentRepository,
	releaseRepo domain.ReleaseRepository,
	sseBroker *sse.Broker,
) (*UIHandler, error) {
	// Parse all templates from embedded FS with helper functions
	funcMap := template.FuncMap{
		"trim":         strings.TrimSpace,
		"filterVendor": domain.FilterVendorStackTrace,
		"host": func(rawURL string) string {
			rawURL = strings.TrimSpace(rawURL)
			if rawURL == "" {
				return ""
			}
			if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
				rawURL = "http://" + rawURL
			}
			u, err := url.Parse(rawURL)
			if err != nil || u.Host == "" {
				return rawURL
			}
			return u.Host
		},
		"formatBytes": func(b int64) string {
			if b <= 0 {
				return "0 B"
			}
			const unit = 1024
			if b < unit {
				return fmt.Sprintf("%d B", b)
			}
			div, exp := int64(unit), 0
			for n := b / unit; n >= unit; n /= unit {
				div *= unit
				exp++
			}
			return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
		},
		"formatDuration": func(ms float64) string {
			if ms <= 0 {
				return "0 ms"
			}
			if ms < 1000 {
				return fmt.Sprintf("%.1f ms", ms)
			}
			return fmt.Sprintf("%.2f s", ms/1000)
		},
		"jsonPretty": func(v any) string {
			if v == nil {
				return ""
			}
			b, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				return fmt.Sprintf("%v", v)
			}
			return string(b)
		},
		"formatTimestamp": func(ts float64) string {
			if ts <= 0 {
				return ""
			}
			sec := int64(ts)
			nsec := int64((ts - float64(sec)) * 1e9)
			t := time.Unix(sec, nsec)
			return t.Format("15:04:05.000")
		},
	}
	tmpl, err := template.New("").Funcs(funcMap).ParseFS(
		web.TemplatesFS,
		"templates/layout.html",
		"templates/index.html",
		"templates/partials/incident_rows.html",
		"templates/partials/incident_row.html",
		"templates/partials/incident_detail.html",
		"templates/github.html",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to parse templates: %w", err)
	}

	h := &UIHandler{
		projectRepo:  projectRepo,
		incidentRepo: incidentRepo,
		releaseRepo:  releaseRepo,
		sseBroker:    sseBroker,
		tmpl:         tmpl,
	}

	return h, nil
}

// SetGitHubClient sets the GitHub API client for fetching file snippets
func (h *UIHandler) SetGitHubClient(client *github.Client) {
	h.githubClient = client
}

// GetRowTemplate returns the parsed single row template for SSE broadcasting
func (h *UIHandler) GetRowTemplate() *template.Template {
	return h.tmpl.Lookup("incident_row.html")
}

// HandleIndex renders the main dashboard page (/ui)
func (h *UIHandler) HandleIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	projects, err := h.projectRepo.GetAllProjects(ctx)
	if err != nil {
		log.Printf("Error getting projects: %v", err)
	}

	status := r.URL.Query().Get("status")
	if status == "" {
		status = "unresolved"
	}

	filter := domain.IncidentFilter{
		Limit:  100,
		Sort:   "recent",
		Status: status,
	}

	incidents, err := h.incidentRepo.GetIncidents(ctx, filter)
	if err != nil {
		log.Printf("Error getting incidents: %v", err)
	}

	totalIncidents, _ := h.incidentRepo.CountIncidents(ctx, 0, "all")
	totalUnresolved, _ := h.incidentRepo.CountIncidents(ctx, 0, "unresolved")
	totalResolved, _ := h.incidentRepo.CountIncidents(ctx, 0, "resolved")

	var repoGroups []repoGroup
	if h.githubAccounts != nil {
		repos, err := h.githubAccounts.ListSelectedRepos(ctx)
		if err != nil {
			log.Printf("Error getting GitHub repos: %v", err)
		}
		repoGroups = groupReposByAccount(repos)
	}

	data := map[string]interface{}{
		"Projects":          projects,
		"GitHubRepoGroups":  repoGroups,
		"Incidents":         incidents,
		"TotalIncidents":    totalIncidents,
		"TotalUnresolved":   totalUnresolved,
		"TotalResolved":     totalResolved,
		"SelectedProjectID": int64(0),
		"SelectedStatus":    status,
		"Query":             "",
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		log.Printf("Error rendering index template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// HandleIncidents returns filtered incident rows (HTMX partial)
func (h *UIHandler) HandleIncidents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	sort := r.URL.Query().Get("sort")
	if sort == "" {
		sort = "recent"
	}

	status := r.URL.Query().Get("status")
	if status == "" {
		status = "unresolved"
	}

	var projectID int64
	if projStr := r.URL.Query().Get("project_id"); projStr != "" {
		projectID, _ = strconv.ParseInt(projStr, 10, 64)
	}

	filter := domain.IncidentFilter{
		ProjectID: projectID,
		Query:     q,
		Status:    status,
		Sort:      sort,
		Limit:     100,
	}

	incidents, err := h.incidentRepo.GetIncidents(ctx, filter)
	if err != nil {
		log.Printf("Error searching incidents: %v", err)
		http.Error(w, "Failed to load incidents", http.StatusInternalServerError)
		return
	}

	data := map[string]interface{}{
		"Incidents": incidents,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "incident_rows.html", data); err != nil {
		log.Printf("Error rendering incident rows: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// HandleIncidentDetail returns drawer content or handles resolve/reopen actions
func (h *UIHandler) HandleIncidentDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 3 {
		http.Error(w, "Incident ID required", http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(pathParts[2], 10, 64)
	if err != nil {
		http.Error(w, "Invalid incident ID", http.StatusBadRequest)
		return
	}

	// Handle action subpaths: /ui/incidents/{id}/resolve or /ui/incidents/{id}/reopen
	if len(pathParts) >= 4 {
		action := pathParts[3]
		if action == "resolve" && r.Method == http.MethodPost {
			resType := r.FormValue("type")
			if resType == "" {
				resType = "immediate"
			}
			version := r.FormValue("version")
			if err := h.incidentRepo.ResolveIncident(ctx, id, resType, version); err != nil {
				log.Printf("Error resolving incident %d: %v", id, err)
				http.Error(w, "Failed to resolve incident", http.StatusInternalServerError)
				return
			}
			// Add HX-Trigger header so HTMX refreshes the main table counters
			w.Header().Set("HX-Trigger", "incident-updated")
		} else if action == "reopen" && r.Method == http.MethodPost {
			if err := h.incidentRepo.ReopenIncident(ctx, id); err != nil {
				log.Printf("Error reopening incident %d: %v", id, err)
				http.Error(w, "Failed to reopen incident", http.StatusInternalServerError)
				return
			}
			w.Header().Set("HX-Trigger", "incident-updated")
		}

		if r.URL.Query().Get("view") == "row" {
			updatedInc, err := h.incidentRepo.GetIncidentByID(ctx, id)
			if err != nil {
				http.Error(w, "Incident not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if err := h.tmpl.ExecuteTemplate(w, "incident_row.html", updatedInc); err != nil {
				log.Printf("Error rendering incident row: %v", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
			return
		}
	}

	incident, err := h.incidentRepo.GetIncidentByID(ctx, id)
	if err != nil {
		log.Printf("Incident not found: %v", err)
		http.Error(w, "Incident not found", http.StatusNotFound)
		return
	}

	var latestRelease string
	if h.releaseRepo != nil && incident.ProjectID > 0 {
		rel, _ := h.releaseRepo.GetLatestRelease(ctx, incident.ProjectID)
		if rel != nil {
			latestRelease = rel.Version
		}
	}

	var project *domain.Project
	if h.projectRepo != nil && incident.ProjectID > 0 {
		project, _ = h.projectRepo.GetByID(ctx, incident.ProjectID)
	}

	codeSnippet, _ := github.ResolveCodeSnippet(ctx, h.githubClient, project, incident, 5)

	var issueURL string
	if project != nil && project.GitHubRepo != "" {
		title := fmt.Sprintf("[%s] %s", incident.ExceptionClass, incident.Message)
		body := fmt.Sprintf("### Exception\n**Class:** `%s`\n**File:** `%s:%d`\n\n```\n%s\n```\n\n*Reported by Larasentry*", incident.ExceptionClass, incident.File, incident.Line, incident.StackTrace)
		issueURL = github.BuildIssueURL(project.GitHubRepo, title, body)
	}

	data := map[string]interface{}{
		"Incident":      incident,
		"Project":       project,
		"CodeSnippet":   codeSnippet,
		"IssueURL":      issueURL,
		"LatestRelease": latestRelease,
		"AppStackTrace": incident.AppStackTrace(),
		"Stats":         incident.StackTraceStats(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "incident_detail.html", data); err != nil {
		log.Printf("Error rendering incident detail: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// HandleEventsStream handles SSE connections from HTMX
func (h *UIHandler) HandleEventsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	clientChan := make(chan []byte, 10)
	h.sseBroker.Register(clientChan)
	defer h.sseBroker.Unregister(clientChan)

	// Send initial comment to establish SSE connection immediately
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
		case msg, ok := <-clientChan:
			if !ok {
				return
			}
			w.Write(msg)
			flusher.Flush()
		}
	}
}

// HandleUpdateProjectGitHubSettings handles POST /ui/projects/github-settings
func (h *UIHandler) HandleUpdateProjectGitHubSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	projectID, err := strconv.ParseInt(r.FormValue("project_id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	repoID, err := strconv.ParseInt(r.FormValue("github_repo_id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid repository ID", http.StatusBadRequest)
		return
	}
	branch := strings.TrimSpace(r.FormValue("github_branch"))
	prefix := strings.TrimSpace(r.FormValue("path_prefix"))

	err = h.projectRepo.UpdateGitHubSettings(r.Context(), projectID, repoID, branch, prefix)
	if errors.Is(err, domain.ErrGitHubRepoNotSelected) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		log.Printf("Error updating github settings for project %d: %v", projectID, err)
		http.Error(w, "Failed to update settings", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", "github-settings-updated")
	http.Redirect(w, r, "/ui", http.StatusSeeOther)
}
