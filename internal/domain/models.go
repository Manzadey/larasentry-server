package domain

import (
	"context"
	"time"
)

// EventPayload represents the incoming JSON from Laravel
type EventPayload struct {
	ExceptionClass string `json:"exception_class"`
	Message        string `json:"message"`
	File           string `json:"file"`
	Line           int    `json:"line"`
	StackTrace     string `json:"stack_trace"`
	AppURL         string `json:"app_url"`
	RequestURL     string `json:"request_url"`
	RequestMethod  string `json:"request_method"`
}

// IncomingEvent represents an internal event passed through the channel
type IncomingEvent struct {
	ProjectID  int64
	ProjectKey string
	Payload    EventPayload
}

// Project represents a client application
type Project struct {
	ID     int64
	Name   string
	APIKey string
}

// Incident represents a grouped error in the database
type Incident struct {
	ID             int64
	ProjectID      int64
	Hash           string
	ExceptionClass string
	Message        string
	File           string
	Line           int
	StackTrace     string
	AppURL         string
	RequestURL     string
	EventsCount    int
	FirstSeenAt    time.Time
	LastSeenAt     time.Time
}

// ProjectRepository describes operations for projects
type ProjectRepository interface {
	GetIDByKey(ctx context.Context, apiKey string) (int64, error)
}

// IncidentRepository describes operations for incidents
type IncidentRepository interface {
	UpsertIncident(ctx context.Context, event IncomingEvent, hash string) (int64, int, error) // ID, count, error
	GetLatestIncidents(ctx context.Context, projectID int64, limit int) ([]Incident, error)
	CleanOldIncidents(ctx context.Context, daysToKeep int) (int64, error)
}

// AlertSender abstracts notification sending
type AlertSender interface {
	SendMessage(ctx context.Context, text string) error
}
