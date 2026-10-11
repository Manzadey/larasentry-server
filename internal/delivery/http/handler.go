package http

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"

	"server/internal/domain"
)

type APIHandler struct {
	projectRepo  domain.ProjectRepository
	incidentRepo domain.IncidentRepository
	eventChan    chan domain.IncomingEvent
	allowedIPs   []string
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
	}
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

// HandleEvent godoc
// @Summary Send a new error event
// @Description Receives an exception/error payload from a Laravel application.
// @Tags events
// @Accept  json
// @Produce  json
// @Security Bearer
// @Param payload body domain.EventPayload true "Event Payload"
// @Success 202 {object} map[string]string
// @Failure 400 {string} string "Bad Request"
// @Failure 401 {string} string "Unauthorized"
// @Failure 403 {string} string "Forbidden"
// @Failure 405 {string} string "Method Not Allowed"
// @Router /api/events [post]
func (h *APIHandler) HandleEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if len(h.allowedIPs) > 0 {
		clientIP := getClientIP(r)
		allowed := false
		for _, ip := range h.allowedIPs {
			if ip == clientIP {
				allowed = true
				break
			}
		}
		if !allowed {
			http.Error(w, "Forbidden - IP not allowed", http.StatusForbidden)
			return
		}
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	apiKey := strings.TrimPrefix(authHeader, "Bearer ")

	project, err := h.projectRepo.GetByKey(r.Context(), apiKey)
	if err != nil {
		http.Error(w, "Unauthorized - Invalid API Key", http.StatusUnauthorized)
		return
	}

	var payload domain.EventPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Bad Request - Invalid JSON", http.StatusBadRequest)
		return
	}

	// Trim incoming strings
	payload.Release = strings.TrimSpace(payload.Release)
	payload.Environment = strings.TrimSpace(payload.Environment)
	payload.Message = strings.TrimSpace(payload.Message)
	payload.ExceptionClass = strings.TrimSpace(payload.ExceptionClass)
	payload.File = strings.TrimSpace(payload.File)
	payload.AppURL = strings.TrimSpace(payload.AppURL)
	payload.RequestURL = strings.TrimSpace(payload.RequestURL)
	payload.StackTrace = strings.TrimSpace(payload.StackTrace)

	if payload.ExceptionClass == "" || payload.Message == "" {
		http.Error(w, "Bad Request - Missing exception_class or message", http.StatusBadRequest)
		return
	}

	h.eventChan <- domain.IncomingEvent{
		ProjectID:   project.ID,
		ProjectName: project.Name,
		ProjectKey:  apiKey,
		Payload:     payload,
	}

	log.Printf("Received event for project %s (%d): %s\n", project.Name, project.ID, payload.ExceptionClass)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"status":"accepted"}`))
}

// HandleListIncidents godoc
// @Summary List incidents
// @Description Returns the latest 100 grouped incidents for the authenticated project.
// @Tags incidents
// @Produce  json
// @Security Bearer
// @Success 200 {array} domain.Incident
// @Failure 401 {string} string "Unauthorized"
// @Failure 405 {string} string "Method Not Allowed"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/incidents [get]
func (h *APIHandler) HandleListIncidents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	apiKey := strings.TrimPrefix(authHeader, "Bearer ")

	projectID, err := h.projectRepo.GetIDByKey(r.Context(), apiKey)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	incidents, err := h.incidentRepo.GetLatestIncidents(r.Context(), projectID, 100)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(incidents)
}

// HandleIncidentAction handles /api/incidents/{id}/resolve and /api/incidents/{id}/reopen
func (h *APIHandler) HandleIncidentAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	apiKey := strings.TrimPrefix(authHeader, "Bearer ")

	projectID, err := h.projectRepo.GetIDByKey(r.Context(), apiKey)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 4 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(pathParts[2], 10, 64)
	if err != nil {
		http.Error(w, "Invalid incident ID", http.StatusBadRequest)
		return
	}

	action := pathParts[3]
	incident, err := h.incidentRepo.GetIncidentByID(r.Context(), id)
	if err != nil || incident == nil {
		http.Error(w, "Incident not found", http.StatusNotFound)
		return
	}

	if incident.ProjectID != projectID {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if action == "resolve" {
		resType := r.URL.Query().Get("type")
		if resType == "" {
			resType = "immediate"
		}
		version := r.URL.Query().Get("version")
		if err := h.incidentRepo.ResolveIncident(r.Context(), id, resType, version); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"resolved","id":` + strconv.FormatInt(id, 10) + `}`))
		return
	} else if action == "reopen" {
		if err := h.incidentRepo.ReopenIncident(r.Context(), id); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"unresolved","id":` + strconv.FormatInt(id, 10) + `}`))
		return
	}

	http.NotFound(w, r)
}
