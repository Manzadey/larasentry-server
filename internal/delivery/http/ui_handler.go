package http

import (
	"bytes"
	"context"
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
		"trim":     strings.TrimSpace,
		"join":     strings.Join,
		"ctxValue": ctxValue,
		"yesNo": func(b *bool) string {
			if b == nil {
				return "—"
			}
			if *b {
				return "Да"
			}
			return "Нет"
		},
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
		"urlPath": func(rawURL string) string {
			u, err := url.Parse(strings.TrimSpace(rawURL))
			if err != nil || u.Path == "" {
				return rawURL
			}
			return u.Path
		},
		"baseName": baseName,
		"plural":   plural,
		"dict":     dict,
		"namespace": func(s string) string {
			return strings.TrimSuffix(s, baseName(s))
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
		"jsonPretty": jsonPretty,
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

// parseListFilter reads list filters shared by the page and the HTMX partial.
func parseListFilter(r *http.Request) domain.IncidentFilter {
	q := r.URL.Query()
	filter := domain.IncidentFilter{
		Query:  strings.TrimSpace(q.Get("q")),
		Status: q.Get("status"),
		Sort:   q.Get("sort"),
		Limit:  100,
	}
	if filter.Status == "" {
		filter.Status = "unresolved"
	}
	if filter.Sort == "" {
		filter.Sort = "recent"
	}
	filter.ProjectID, _ = strconv.ParseInt(q.Get("project_id"), 10, 64)
	return filter
}

// listData collects rows and status segment counters for the selected project.
func (h *UIHandler) listData(ctx context.Context, filter domain.IncidentFilter) (map[string]interface{}, error) {
	incidents, err := h.incidentRepo.GetIncidents(ctx, filter)
	if err != nil {
		return nil, err
	}
	totalIncidents, _ := h.incidentRepo.CountIncidents(ctx, filter.ProjectID, "all")
	totalUnresolved, _ := h.incidentRepo.CountIncidents(ctx, filter.ProjectID, "unresolved")
	totalResolved, _ := h.incidentRepo.CountIncidents(ctx, filter.ProjectID, "resolved")
	return map[string]interface{}{
		"Incidents":       incidents,
		"TotalIncidents":  totalIncidents,
		"TotalUnresolved": totalUnresolved,
		"TotalResolved":   totalResolved,
		"SelectedStatus":  filter.Status,
	}, nil
}

// HandleIndex renders the main dashboard page (/ui)
func (h *UIHandler) HandleIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	filter := parseListFilter(r)

	data, err := h.listData(ctx, filter)
	if err != nil {
		log.Printf("Error getting incidents: %v", err)
		data = map[string]interface{}{"SelectedStatus": filter.Status}
	}

	projects, err := h.projectRepo.GetAllProjects(ctx)
	if err != nil {
		log.Printf("Error getting projects: %v", err)
	}
	projectCounts := make(map[int64]int, len(projects))
	latestReleases := make(map[int64]string, len(projects))
	for _, p := range projects {
		projectCounts[p.ID], _ = h.incidentRepo.CountIncidents(ctx, p.ID, "all")
		if h.releaseRepo != nil {
			if rel, _ := h.releaseRepo.GetLatestRelease(ctx, p.ID); rel != nil {
				latestReleases[p.ID] = rel.Version
			}
		}
	}

	var repoGroups []repoGroup
	if h.githubAccounts != nil {
		repos, err := h.githubAccounts.ListSelectedRepos(ctx)
		if err != nil {
			log.Printf("Error getting GitHub repos: %v", err)
		}
		repoGroups = groupReposByAccount(repos)
	}

	data["Projects"] = projects
	data["ProjectCounts"] = projectCounts
	data["LatestReleases"] = latestReleases
	data["GitHubRepoGroups"] = repoGroups
	data["SelectedProjectID"] = filter.ProjectID
	data["SelectedSort"] = filter.Sort
	data["Query"] = filter.Query

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		log.Printf("Error rendering index template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// HandleIncidents returns filtered incident rows plus out-of-band segment counters (HTMX partial)
func (h *UIHandler) HandleIncidents(w http.ResponseWriter, r *http.Request) {
	data, err := h.listData(r.Context(), parseListFilter(r))
	if err != nil {
		log.Printf("Error searching incidents: %v", err)
		http.Error(w, "Failed to load incidents", http.StatusInternalServerError)
		return
	}
	data["OOB"] = true

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

	if len(pathParts) >= 4 {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var actionErr error
		switch action := pathParts[3]; action {
		case "resolve":
			resType := r.FormValue("type")
			if resType == "" {
				resType = "immediate"
			}
			actionErr = h.incidentRepo.ResolveIncident(ctx, id, resType, r.FormValue("version"))
		case "reopen":
			actionErr = h.incidentRepo.ReopenIncident(ctx, id)
		case "restore":
			state, err := parseIncidentState(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			actionErr = h.incidentRepo.RestoreIncidentState(ctx, id, state)
		default:
			http.NotFound(w, r)
			return
		}
		if actionErr != nil {
			log.Printf("Error applying %s to incident %d: %v", pathParts[3], id, actionErr)
			http.Error(w, "Failed to update incident", http.StatusInternalServerError)
			return
		}
		// incident-updated перезагружает список, счётчики и открытую шторку
		w.Header().Set("HX-Trigger", "incident-updated")
		w.WriteHeader(http.StatusNoContent)
		return
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

	frames := domain.ParseFrames(incident.StackTrace)
	vendorFrames := 0
	for _, f := range frames {
		if f.Vendor {
			vendorFrames++
		}
	}

	data := map[string]interface{}{
		"Incident":      incident,
		"Project":       project,
		"CodeSnippet":   codeSnippet,
		"IssueURL":      issueURL,
		"LatestRelease": latestRelease,
		"StackGroups":   domain.GroupFrames(frames),
		"CallGraph":     domain.BuildCallGraph(frames),
		"VendorFrames":  vendorFrames,
		"SnippetNote":   snippetNote(incident, frames),
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

// baseName returns the last segment of a PHP class name or a file path.
func baseName(s string) string {
	return s[strings.LastIndexAny(s, `\/`)+1:]
}

// plural picks the Russian word form for n: plural(n, "ошибка", "ошибки", "ошибок").
func plural(n int, one, few, many string) string {
	switch m10, m100 := n%10, n%100; {
	case m10 == 1 && m100 != 11:
		return one
	case m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14):
		return few
	default:
		return many
	}
}

// parseIncidentState validates the state the UI captured before an action it now undoes.
func parseIncidentState(r *http.Request) (domain.IncidentState, error) {
	state := domain.IncidentState{
		Status:            domain.IncidentStatus(r.FormValue("status")),
		IsRegression:      r.FormValue("is_regression") == "true",
		ResolvedInRelease: strings.TrimSpace(r.FormValue("resolved_in_release")),
	}
	switch state.Status {
	case domain.StatusUnresolved, domain.StatusResolved, domain.StatusIgnored:
	default:
		return state, fmt.Errorf("unknown status %q", state.Status)
	}
	if raw := r.FormValue("resolved_at"); raw != "" {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return state, fmt.Errorf("invalid resolved_at: %w", err)
		}
		state.ResolvedAt = &t
	}
	return state, nil
}

// snippetNote explains why the "Место ошибки" block has no source code.
func snippetNote(inc *domain.Incident, frames []domain.StackFrame) string {
	if strings.Contains(inc.File, "eval()'d code") {
		return "Код выполнен в tinker через eval(), исходного файла нет."
	}
	if domain.IsVendorTraceLine(inc.File) {
		for _, f := range frames {
			if !f.Vendor && f.Line > 0 {
				return fmt.Sprintf("Ошибка возникла внутри vendor. Ближайший кадр вашего кода: %s:%d.", f.FileName(), f.Line)
			}
		}
		return "Ошибка возникла внутри vendor, кадров вашего кода в стеке нет."
	}
	return "Исходный код недоступен: клиент не прислал фрагмент, а репозиторий GitHub для проекта не привязан."
}

// ctxValue renders an application context value: strings as is, everything else as indented JSON.
func ctxValue(v any) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case string:
		return val
	case map[string]any, []any:
		return jsonPretty(val)
	default:
		return fmt.Sprint(val)
	}
}

// jsonPretty renders v as JSON indented by two spaces.
func jsonPretty(v any) string {
	if v == nil {
		return ""
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// html/template экранирует вывод сам; иначе & в URL показывается как &
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Sprintf("%v", v)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// dict builds a map from key/value pairs to pass several values into a nested template.
func dict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, errors.New("dict: odd number of arguments")
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: key %v is not a string", pairs[i])
		}
		m[key] = pairs[i+1]
	}
	return m, nil
}
