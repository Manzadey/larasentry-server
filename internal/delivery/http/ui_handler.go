package http

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"server/internal/delivery/sse"
	"server/internal/domain"
	"server/web"
)

type UIHandler struct {
	projectRepo  domain.ProjectRepository
	incidentRepo domain.IncidentRepository
	releaseRepo  domain.ReleaseRepository
	sseBroker    *sse.Broker
	tmpl         *template.Template
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
	}
	tmpl, err := template.New("").Funcs(funcMap).ParseFS(
		web.TemplatesFS,
		"templates/layout.html",
		"templates/index.html",
		"templates/partials/incident_rows.html",
		"templates/partials/incident_row.html",
		"templates/partials/incident_detail.html",
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

	data := map[string]interface{}{
		"Projects":          projects,
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

	data := map[string]interface{}{
		"Incident":      incident,
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
