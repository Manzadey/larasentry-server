package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

type APIHandler struct {
	db        *sql.DB
	eventChan chan IncomingEvent
}

func NewAPIHandler(db *sql.DB, eventChan chan IncomingEvent) *APIHandler {
	return &APIHandler{db: db, eventChan: eventChan}
}

func (h *APIHandler) HandleEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. Auth via API Key (Authorization: Bearer <token>)
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	apiKey := strings.TrimPrefix(authHeader, "Bearer ")

	// 2. Fetch Project ID
	var projectID int64
	err := h.db.QueryRow("SELECT id FROM projects WHERE api_key = ?", apiKey).Scan(&projectID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Unauthorized - Invalid API Key", http.StatusUnauthorized)
			return
		}
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// 3. Decode Payload
	var payload EventPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Bad Request - Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate minimal requirements
	if payload.ExceptionClass == "" || payload.Message == "" {
		http.Error(w, "Bad Request - Missing exception_class or message", http.StatusBadRequest)
		return
	}

	// 4. Send to Processor Channel (Non-blocking for the client request)
	h.eventChan <- IncomingEvent{
		ProjectID:  projectID,
		ProjectKey: apiKey,
		Payload:    payload,
	}

	log.Printf("Received event for project %d: %s\n", projectID, payload.ExceptionClass)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"status":"accepted"}`))
}

func (h *APIHandler) HandleListIncidents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Simple auth (you can expand this later)
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	apiKey := strings.TrimPrefix(authHeader, "Bearer ")

	var projectID int64
	err := h.db.QueryRow("SELECT id FROM projects WHERE api_key = ?", apiKey).Scan(&projectID)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	rows, err := h.db.Query(`
		SELECT id, hash, exception_class, message, file, line, app_url, request_url, events_count, first_seen_at, last_seen_at 
		FROM incidents 
		WHERE project_id = ? 
		ORDER BY last_seen_at DESC 
		LIMIT 100`, projectID)
	
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var incidents []Incident
	for rows.Next() {
		var inc Incident
		if err := rows.Scan(
			&inc.ID, &inc.Hash, &inc.ExceptionClass, &inc.Message, 
			&inc.File, &inc.Line, &inc.AppURL, &inc.RequestURL, 
			&inc.EventsCount, &inc.FirstSeenAt, &inc.LastSeenAt,
		); err != nil {
			log.Printf("Error scanning row: %v", err)
			continue
		}
		inc.ProjectID = projectID
		incidents = append(incidents, inc)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(incidents)
}
