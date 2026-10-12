package http

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"server/internal/delivery/sse"
	"server/internal/domain"
	"server/internal/usecase"
)

type APIHandler struct {
	projectRepo  domain.ProjectRepository
	incidentRepo domain.IncidentRepository
	releaseRepo  domain.ReleaseRepository
	sseBroker    *sse.Broker
	eventChan    chan domain.IncomingEvent
	allowedIPs   []string
	startTime    time.Time
}

func NewAPIHandler(
	projectRepo domain.ProjectRepository,
	incidentRepo domain.IncidentRepository,
	eventChan chan domain.IncomingEvent,
	allowedIPs []string,
) *APIHandler {
	return &APIHandler{
		projectRepo:  projectRepo,
		incidentRepo: incidentRepo,
		eventChan:    eventChan,
		allowedIPs:   allowedIPs,
		startTime:    time.Now(),
	}
}

func (h *APIHandler) SetReleaseRepo(repo domain.ReleaseRepository) {
	h.releaseRepo = repo
}

func (h *APIHandler) SetSSEBroker(broker *sse.Broker) {
	h.sseBroker = broker
}

func getClientIP(r *http.Request) string {
	ip := r.Header.Get("X-Real-IP")
	if ip == "" {
		ip = r.Header.Get("X-Forwarded-For")
	}
	if ip == "" {
		ip, _, _ = net.SplitHostPort(r.RemoteAddr)
	} else {
		ips := strings.Split(ip, ",")
		ip = strings.TrimSpace(ips[0])
	}
	return ip
}

func (h *APIHandler) checkIPAllowed(r *http.Request) bool {
	if len(h.allowedIPs) == 0 {
		return true
	}
	clientIP := getClientIP(r)
	for _, ip := range h.allowedIPs {
		if ip == clientIP {
			return true
		}
	}
	return false
}

func (h *APIHandler) resolveProject(r *http.Request, explicitName string) (*domain.Project, error) {
	ctx := r.Context()
	projectName := strings.TrimSpace(explicitName)

	if projectName == "" {
		for _, hdr := range []string{"X-Project", "X-Project-Name", "X-App-Name"} {
			if val := strings.TrimSpace(r.Header.Get(hdr)); val != "" {
				projectName = val
				break
			}
		}
	}

	if projectName == "" {
		for _, qParam := range []string{"project", "project_name", "app"} {
			if val := strings.TrimSpace(r.URL.Query().Get(qParam)); val != "" {
				projectName = val
				break
			}
		}
	}

	if projectName == "" {
		if pidStr := strings.TrimSpace(r.URL.Query().Get("project_id")); pidStr != "" {
			if pid, err := strconv.ParseInt(pidStr, 10, 64); err == nil && pid > 0 {
				p, err := h.projectRepo.GetByID(ctx, pid)
				if err == nil && p != nil {
					return p, nil
				}
			}
		}
	}

	// Backwards compatibility for Bearer tokens or query param token
	authHeader := r.Header.Get("Authorization")
	var apiKey string
	if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
		apiKey = strings.TrimPrefix(authHeader, "Bearer ")
	} else {
		apiKey = r.URL.Query().Get("token")
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey != "" {
		p, err := h.projectRepo.GetByKey(ctx, apiKey)
		if err == nil && p != nil {
			return p, nil
		}
	}

	if projectName != "" {
		return h.projectRepo.GetOrCreateByName(ctx, projectName)
	}

	return h.projectRepo.GetOrCreateByName(ctx, "Default Project")
}

func (h *APIHandler) authenticate(r *http.Request) (*domain.Project, error) {
	return h.resolveProject(r, "")
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, errMessage string) {
	writeJSON(w, status, map[string]string{"error": errMessage})
}

func getPathParam(r *http.Request, key string) string {
	if val := r.PathValue(key); val != "" {
		return val
	}
	// Fallback path segment parsing
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if key == "id" && len(parts) >= 3 && parts[1] == "incidents" {
		return parts[2]
	}
	if key == "version" && len(parts) >= 3 && parts[1] == "releases" {
		return parts[2]
	}
	return ""
}

// =============================================================================
// 1. EVENT INGESTION
// =============================================================================

// HandleEvent handles POST /api/events
func (h *APIHandler) HandleEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	if !h.checkIPAllowed(r) {
		writeError(w, http.StatusForbidden, "Forbidden - IP not allowed")
		return
	}

	var payload domain.EventPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "Bad Request - Invalid JSON")
		return
	}

	projName := payload.Project
	if projName == "" {
		projName = payload.ProjectName
	}
	if projName == "" && payload.Tags != nil {
		if val, ok := payload.Tags["project"]; ok {
			projName = val
		} else if val, ok := payload.Tags["project_name"]; ok {
			projName = val
		}
	}

	project, err := h.resolveProject(r, projName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to resolve project: "+err.Error())
		return
	}

	payload.Release = strings.TrimSpace(payload.Release)
	payload.Environment = strings.TrimSpace(payload.Environment)
	payload.Message = strings.TrimSpace(payload.Message)
	payload.ExceptionClass = strings.TrimSpace(payload.ExceptionClass)
	payload.File = strings.TrimSpace(payload.File)
	payload.AppURL = strings.TrimSpace(payload.AppURL)
	payload.RequestURL = strings.TrimSpace(payload.RequestURL)
	payload.StackTrace = strings.TrimSpace(payload.StackTrace)
	payload.PHPVersion = strings.TrimSpace(payload.PHPVersion)
	if payload.PHPVersion == "" && payload.System != nil && payload.System.PHPVersion != "" {
		payload.PHPVersion = strings.TrimSpace(payload.System.PHPVersion)
	}
	if payload.PHPVersion == "" && payload.Tags != nil {
		if v, ok := payload.Tags["php_version"]; ok {
			payload.PHPVersion = strings.TrimSpace(v)
		} else if v, ok := payload.Tags["php"]; ok {
			payload.PHPVersion = strings.TrimSpace(v)
		}
	}
	payload.LaravelVersion = strings.TrimSpace(payload.LaravelVersion)
	if payload.LaravelVersion == "" && payload.System != nil && payload.System.LaravelVersion != "" {
		payload.LaravelVersion = strings.TrimSpace(payload.System.LaravelVersion)
	}
	if payload.LaravelVersion == "" && payload.Tags != nil {
		if v, ok := payload.Tags["laravel_version"]; ok {
			payload.LaravelVersion = strings.TrimSpace(v)
		} else if v, ok := payload.Tags["laravel"]; ok {
			payload.LaravelVersion = strings.TrimSpace(v)
		}
	}

	if payload.ExceptionClass == "" || payload.Message == "" {
		writeError(w, http.StatusBadRequest, "Bad Request - Missing exception_class or message")
		return
	}

	eventID := uuid.New().String()
	hash := usecase.CalculateHash(project.APIKey, payload)

	h.eventChan <- domain.IncomingEvent{
		EventID:     eventID,
		ProjectID:   project.ID,
		ProjectName: project.Name,
		ProjectKey:  project.APIKey,
		Payload:     payload,
	}

	log.Printf("[Event] Received event %s for project %s (%d): %s\n", eventID, project.Name, project.ID, payload.ExceptionClass)

	writeJSON(w, http.StatusAccepted, map[string]string{
		"status":   "accepted",
		"event_id": eventID,
		"hash":     hash,
	})
}

// HandleBatchEvents handles POST /api/events/batch
func (h *APIHandler) HandleBatchEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	if !h.checkIPAllowed(r) {
		writeError(w, http.StatusForbidden, "Forbidden - IP not allowed")
		return
	}

	var req struct {
		Project string                `json:"project"`
		Events  []domain.EventPayload `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Bad Request - Invalid JSON")
		return
	}

	defaultBatchProject, err := h.resolveProject(r, req.Project)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to resolve project: "+err.Error())
		return
	}

	if len(req.Events) == 0 {
		writeError(w, http.StatusBadRequest, "Bad Request - No events provided")
		return
	}
	if len(req.Events) > 50 {
		writeError(w, http.StatusBadRequest, "Bad Request - Batch exceeds maximum size of 50")
		return
	}

	acceptedCount := 0
	for _, payload := range req.Events {
		eventProj := defaultBatchProject
		eventProjName := payload.Project
		if eventProjName == "" {
			eventProjName = payload.ProjectName
		}
		if eventProjName != "" && eventProjName != defaultBatchProject.Name {
			if resolved, err := h.resolveProject(r, eventProjName); err == nil && resolved != nil {
				eventProj = resolved
			}
		}

		payload.Release = strings.TrimSpace(payload.Release)
		payload.Environment = strings.TrimSpace(payload.Environment)
		payload.Message = strings.TrimSpace(payload.Message)
		payload.ExceptionClass = strings.TrimSpace(payload.ExceptionClass)
		payload.File = strings.TrimSpace(payload.File)
		payload.AppURL = strings.TrimSpace(payload.AppURL)
		payload.RequestURL = strings.TrimSpace(payload.RequestURL)
		payload.StackTrace = strings.TrimSpace(payload.StackTrace)
		payload.PHPVersion = strings.TrimSpace(payload.PHPVersion)
		if payload.PHPVersion == "" && payload.System != nil && payload.System.PHPVersion != "" {
			payload.PHPVersion = strings.TrimSpace(payload.System.PHPVersion)
		}
		if payload.PHPVersion == "" && payload.Tags != nil {
			if v, ok := payload.Tags["php_version"]; ok {
				payload.PHPVersion = strings.TrimSpace(v)
			} else if v, ok := payload.Tags["php"]; ok {
				payload.PHPVersion = strings.TrimSpace(v)
			}
		}
		payload.LaravelVersion = strings.TrimSpace(payload.LaravelVersion)
		if payload.LaravelVersion == "" && payload.System != nil && payload.System.LaravelVersion != "" {
			payload.LaravelVersion = strings.TrimSpace(payload.System.LaravelVersion)
		}
		if payload.LaravelVersion == "" && payload.Tags != nil {
			if v, ok := payload.Tags["laravel_version"]; ok {
				payload.LaravelVersion = strings.TrimSpace(v)
			} else if v, ok := payload.Tags["laravel"]; ok {
				payload.LaravelVersion = strings.TrimSpace(v)
			}
		}

		if payload.ExceptionClass == "" || payload.Message == "" {
			continue
		}

		eventID := uuid.New().String()
		h.eventChan <- domain.IncomingEvent{
			EventID:     eventID,
			ProjectID:   eventProj.ID,
			ProjectName: eventProj.Name,
			ProjectKey:  eventProj.APIKey,
			Payload:     payload,
		}
		acceptedCount++
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":         "accepted",
		"accepted_count": acceptedCount,
	})
}

// =============================================================================
// 2. INCIDENTS API
// =============================================================================

// HandleListIncidents handles GET /api/incidents
func (h *APIHandler) HandleListIncidents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var projectID int64
	hasExplicitProject := r.URL.Query().Get("project") != "" ||
		r.URL.Query().Get("project_name") != "" ||
		r.URL.Query().Get("project_id") != "" ||
		r.Header.Get("X-Project") != "" ||
		r.Header.Get("X-Project-Name") != "" ||
		r.Header.Get("Authorization") != "" ||
		r.URL.Query().Get("token") != ""

	if hasExplicitProject {
		if p, err := h.resolveProject(r, ""); err == nil && p != nil {
			projectID = p.ID
		}
	}

	q := r.URL.Query()
	status := strings.TrimSpace(q.Get("status"))
	if status == "" {
		status = "unresolved"
	}

	queryStr := strings.TrimSpace(q.Get("q"))
	environment := strings.TrimSpace(q.Get("environment"))
	release := strings.TrimSpace(q.Get("release"))
	sort := strings.TrimSpace(q.Get("sort"))
	if sort == "" {
		sort = "recent"
	}

	page := 1
	if pStr := q.Get("page"); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
			page = p
		}
	}

	limit := 50
	if lStr := q.Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}
	if limit > 200 {
		limit = 200
	}

	var isRegression *bool
	if regStr := q.Get("is_regression"); regStr != "" {
		val := regStr == "true" || regStr == "1"
		isRegression = &val
	}

	includeTrace := q.Get("include_trace") == "true" || q.Get("include_trace") == "1"

	offset := (page - 1) * limit
	filter := domain.IncidentFilter{
		ProjectID:    projectID,
		Status:       status,
		Query:        queryStr,
		Environment:  environment,
		Release:      release,
		IsRegression: isRegression,
		Sort:         sort,
		Limit:        limit,
		Offset:       offset,
	}

	incidents, err := h.incidentRepo.GetIncidents(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal Server Error: "+err.Error())
		return
	}

	total, err := h.incidentRepo.GetIncidentsCount(r.Context(), filter)
	if err != nil {
		total = len(incidents)
	}

	// Optimize bandwidth: strip stack traces if not requested
	if !includeTrace {
		for i := range incidents {
			incidents[i].StackTrace = ""
		}
	}

	totalPages := 0
	if total > 0 {
		totalPages = (total + limit - 1) / limit
	}

	writeJSON(w, http.StatusOK, domain.IncidentListResponse{
		Items: incidents,
		Meta: domain.PageMeta{
			Total:      total,
			Page:       page,
			Limit:      limit,
			TotalPages: totalPages,
		},
	})
}

// HandleGetIncident handles GET /api/incidents/{id}
func (h *APIHandler) HandleGetIncident(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	idStr := getPathParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid incident ID")
		return
	}

	inc, err := h.incidentRepo.GetIncidentByID(r.Context(), id)
	if err != nil || inc == nil {
		writeError(w, http.StatusNotFound, "Incident not found")
		return
	}

	detail := domain.IncidentDetail{
		Incident:        *inc,
		AppStackTrace:   inc.AppStackTrace(),
		StackTraceStats: inc.StackTraceStats(),
	}

	writeJSON(w, http.StatusOK, detail)
}

// HandleResolveIncident handles POST /api/incidents/{id}/resolve
func (h *APIHandler) HandleResolveIncident(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	idStr := getPathParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid incident ID")
		return
	}

	inc, err := h.incidentRepo.GetIncidentByID(r.Context(), id)
	if err != nil || inc == nil {
		writeError(w, http.StatusNotFound, "Incident not found")
		return
	}

	var req domain.ResolveRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Type == "" {
		req.Type = r.URL.Query().Get("type")
	}
	if req.Type == "" {
		req.Type = "immediate"
	}
	if req.Version == "" {
		req.Version = r.URL.Query().Get("version")
	}

	if err := h.incidentRepo.ResolveIncident(r.Context(), id, req.Type, req.Version); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to resolve incident: "+err.Error())
		return
	}

	updated, _ := h.incidentRepo.GetIncidentByID(r.Context(), id)
	writeJSON(w, http.StatusOK, updated)
}

// HandleReopenIncident handles POST /api/incidents/{id}/reopen
func (h *APIHandler) HandleReopenIncident(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	idStr := getPathParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid incident ID")
		return
	}

	inc, err := h.incidentRepo.GetIncidentByID(r.Context(), id)
	if err != nil || inc == nil {
		writeError(w, http.StatusNotFound, "Incident not found")
		return
	}

	if err := h.incidentRepo.ReopenIncident(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to reopen incident: "+err.Error())
		return
	}

	updated, _ := h.incidentRepo.GetIncidentByID(r.Context(), id)
	writeJSON(w, http.StatusOK, updated)
}

// HandleIgnoreIncident handles POST /api/incidents/{id}/ignore
func (h *APIHandler) HandleIgnoreIncident(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	idStr := getPathParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid incident ID")
		return
	}

	inc, err := h.incidentRepo.GetIncidentByID(r.Context(), id)
	if err != nil || inc == nil {
		writeError(w, http.StatusNotFound, "Incident not found")
		return
	}

	var req struct {
		Ignored *bool `json:"ignored"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	ignored := true
	if req.Ignored != nil {
		ignored = *req.Ignored
	}

	if err := h.incidentRepo.IgnoreIncident(r.Context(), id, ignored); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to ignore incident: "+err.Error())
		return
	}

	updated, _ := h.incidentRepo.GetIncidentByID(r.Context(), id)
	writeJSON(w, http.StatusOK, updated)
}

// HandleDeleteIncident handles DELETE /api/incidents/{id}
func (h *APIHandler) HandleDeleteIncident(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	idStr := getPathParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid incident ID")
		return
	}

	inc, err := h.incidentRepo.GetIncidentByID(r.Context(), id)
	if err != nil || inc == nil {
		writeError(w, http.StatusNotFound, "Incident not found")
		return
	}

	if err := h.incidentRepo.DeleteIncident(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete incident: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleBatchIncidents handles POST /api/incidents/batch
func (h *APIHandler) HandleBatchIncidents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req domain.BatchIncidentsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Bad Request - Invalid JSON")
		return
	}

	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "Bad Request - IDs array is empty")
		return
	}

	var verifiedIDs []int64
	for _, id := range req.IDs {
		inc, err := h.incidentRepo.GetIncidentByID(r.Context(), id)
		if err == nil && inc != nil {
			verifiedIDs = append(verifiedIDs, id)
		}
	}

	if len(verifiedIDs) == 0 {
		writeError(w, http.StatusNotFound, "No matching incidents found")
		return
	}

	affected, err := h.incidentRepo.BatchAction(r.Context(), req.Action, verifiedIDs, req.Params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Batch action failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "success",
		"action":   req.Action,
		"affected": affected,
	})
}

// =============================================================================
// 3. RELEASES API
// =============================================================================

// HandleListReleases handles GET /api/releases
func (h *APIHandler) HandleListReleases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	project, err := h.authenticate(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Unauthorized - "+err.Error())
		return
	}

	if h.releaseRepo == nil {
		writeError(w, http.StatusInternalServerError, "Release repository not configured")
		return
	}

	limit := 20
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}

	releases, err := h.releaseRepo.GetReleasesWithStats(r.Context(), project.ID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load releases: "+err.Error())
		return
	}

	if releases == nil {
		releases = []domain.ReleaseSummary{}
	}

	writeJSON(w, http.StatusOK, releases)
}

// HandleCreateRelease handles POST /api/releases
func (h *APIHandler) HandleCreateRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	if h.releaseRepo == nil {
		writeError(w, http.StatusInternalServerError, "Release repository not configured")
		return
	}

	var req domain.CreateReleaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Bad Request - Invalid JSON")
		return
	}

	req.Version = strings.TrimSpace(req.Version)
	if req.Version == "" {
		writeError(w, http.StatusBadRequest, "Bad Request - Version cannot be empty")
		return
	}

	project, err := h.resolveProject(r, req.Project)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to resolve project: "+err.Error())
		return
	}

	rel, err := h.releaseRepo.CreateRelease(r.Context(), project.ID, req.Version, req.Ref, req.Commit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create release: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, rel)
}

// HandleReleaseIncidents handles GET /api/releases/{version}/incidents
func (h *APIHandler) HandleReleaseIncidents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	project, _ := h.resolveProject(r, "")

	if h.releaseRepo == nil {
		writeError(w, http.StatusInternalServerError, "Release repository not configured")
		return
	}

	version := getPathParam(r, "version")
	if version == "" {
		writeError(w, http.StatusBadRequest, "Release version required")
		return
	}

	newOnly := r.URL.Query().Get("new_only") == "true" || r.URL.Query().Get("new_only") == "1"

	incidents, err := h.releaseRepo.GetIncidentsByRelease(r.Context(), project.ID, version, newOnly)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load release incidents: "+err.Error())
		return
	}

	if incidents == nil {
		incidents = []domain.Incident{}
	}

	writeJSON(w, http.StatusOK, incidents)
}

// =============================================================================
// 4. ANALYTICS & STATS
// =============================================================================

// HandleStatsSummary handles GET /api/stats/summary
func (h *APIHandler) HandleStatsSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	project, _ := h.resolveProject(r, "")

	summary, err := h.incidentRepo.GetStatsSummary(r.Context(), project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to get stats: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, summary)
}

// =============================================================================
// 5. REALTIME SSE & SYSTEM HEALTH
// =============================================================================

// HandleEventsStreamJSON handles GET /api/events/stream
func (h *APIHandler) HandleEventsStreamJSON(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Streaming unsupported")
		return
	}

	if h.sseBroker == nil {
		writeError(w, http.StatusInternalServerError, "SSE Broker not configured")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	clientChan := make(chan []byte, 20)
	h.sseBroker.RegisterJSON(clientChan)
	defer h.sseBroker.UnregisterJSON(clientChan)

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
			_, _ = w.Write(msg)
			flusher.Flush()
		}
	}
}

// HandleHealthCheck handles GET /api/health
func (h *APIHandler) HandleHealthCheck(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(h.startTime).Truncate(time.Second).String()
	queueSize := 0
	if h.eventChan != nil {
		queueSize = len(h.eventChan)
	}

	status := domain.HealthStatus{
		Status:    "ok",
		Database:  "healthy",
		QueueSize: queueSize,
		Uptime:    uptime,
	}

	writeJSON(w, http.StatusOK, status)
}

// HandleUpdateProjectGitHub handles PUT /api/projects/{id}/github
func (h *APIHandler) HandleUpdateProjectGitHub(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	idStr := getPathParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	var req struct {
		GitHubRepo   string `json:"github_repo"`
		GitHubToken  string `json:"github_token"`
		GitHubBranch string `json:"github_branch"`
		PathPrefix   string `json:"path_prefix"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Bad Request - Invalid JSON")
		return
	}

	if err := h.projectRepo.UpdateGitHubSettings(r.Context(), id, req.GitHubRepo, req.GitHubToken, req.GitHubBranch, req.PathPrefix); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update GitHub settings: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "ok",
		"message":    "GitHub settings updated successfully",
		"project_id": id,
	})
}
