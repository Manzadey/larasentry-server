package domain

import (
	"context"
	"time"
)

type IncidentStatus string

const (
	StatusUnresolved IncidentStatus = "unresolved"
	StatusResolved   IncidentStatus = "resolved"
	StatusIgnored    IncidentStatus = "ignored"
)

// EventPayload represents the incoming JSON from Laravel
type EventPayload struct {
	Release        string `json:"release"`
	Environment    string `json:"environment"`
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
	ProjectID   int64
	ProjectName string
	ProjectKey  string
	Payload     EventPayload
}

// Project represents a client application
type Project struct {
	ID     int64
	Name   string
	APIKey string
}

// Release represents a tracked application version
type Release struct {
	ID        int64
	ProjectID int64
	Version   string
	CreatedAt time.Time
}

// Incident represents a grouped error in the database
type Incident struct {
	ID                int64
	ProjectID         int64
	ProjectName       string
	Hash              string
	ExceptionClass    string
	Message           string
	File              string
	Line              int
	StackTrace        string
	AppURL            string
	RequestURL        string
	EventsCount       int
	FirstSeenAt       time.Time
	LastSeenAt        time.Time
	Environment       string
	FirstRelease      string
	LastRelease       string
	Status            IncidentStatus
	ResolvedInRelease string
	ResolvedAt        *time.Time
	IsRegression      bool
}

// IncidentFilter represents parameters for filtering incidents
type IncidentFilter struct {
	ProjectID int64
	Query     string
	Status    string // "unresolved", "resolved", "all"
	Sort      string // "recent", "count"
	Limit     int
}

// UpsertResult contains the outcome of an event upsert
type UpsertResult struct {
	IncidentID   int64
	TotalCount   int
	IsRegression bool
	IsNew        bool
	Incident     Incident
	Ignored      bool // true if event belongs to an already resolved issue from an older release
}

// ProjectRepository describes operations for projects
type ProjectRepository interface {
	GetIDByKey(ctx context.Context, apiKey string) (int64, error)
	GetByKey(ctx context.Context, apiKey string) (*Project, error)
	GetAllProjects(ctx context.Context) ([]Project, error)
}

// ReleaseRepository describes operations for releases
type ReleaseRepository interface {
	UpsertRelease(ctx context.Context, projectID int64, version string) (*Release, error)
	GetLatestRelease(ctx context.Context, projectID int64) (*Release, error)
	GetRelease(ctx context.Context, projectID int64, version string) (*Release, error)
}

// IncidentRepository describes operations for incidents
type IncidentRepository interface {
	UpsertIncident(ctx context.Context, event IncomingEvent, hash string) (*UpsertResult, error)
	GetLatestIncidents(ctx context.Context, projectID int64, limit int) ([]Incident, error)
	GetIncidents(ctx context.Context, filter IncidentFilter) ([]Incident, error)
	GetIncidentByID(ctx context.Context, id int64) (*Incident, error)
	CountIncidents(ctx context.Context, projectID int64, status string) (int, error)
	CleanOldIncidents(ctx context.Context, daysToKeep int) (int64, error)
	ResolveIncident(ctx context.Context, id int64, resolutionType string, targetVersion string) error
	ReopenIncident(ctx context.Context, id int64) error
}

// AlertSender abstracts notification sending
type AlertSender interface {
	SendMessage(ctx context.Context, text string) error
}
