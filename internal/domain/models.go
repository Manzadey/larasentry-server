package domain

import (
	"context"
	"errors"
	"time"
)

type IncidentStatus string

const (
	StatusUnresolved IncidentStatus = "unresolved"
	StatusResolved   IncidentStatus = "resolved"
	StatusIgnored    IncidentStatus = "ignored"
)

// IncidentState — состояние решения, снятое до действия в UI, чтобы его можно было точно откатить.
type IncidentState struct {
	Status            IncidentStatus
	IsRegression      bool
	ResolvedInRelease string
	ResolvedAt        *time.Time
}

// UserContext — авторизованный пользователь в момент ошибки.
type UserContext struct {
	ID        string `json:"id,omitempty"`
	Email     string `json:"email,omitempty"`
	IPAddress string `json:"ip_address,omitempty"`
}

// EventPayload — входящий JSON от Laravel-клиента.
type EventPayload struct {
	Project        string            `json:"project,omitempty"`
	ProjectName    string            `json:"project_name,omitempty"`
	Release        string            `json:"release"`
	Environment    string            `json:"environment"`
	ExceptionClass string            `json:"exception_class"`
	Message        string            `json:"message"`
	File           string            `json:"file"`
	Line           int               `json:"line"`
	StackTrace     string            `json:"stack_trace"`
	AppURL         string            `json:"app_url"`
	RequestURL     string            `json:"request_url"`
	RequestMethod  string            `json:"request_method"`
	Level          string            `json:"level,omitempty"`
	User           *UserContext      `json:"user,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
	Fingerprint    []string          `json:"fingerprint,omitempty"`
	Commit         string            `json:"commit,omitempty"`
	CodeSnippet    *CodeSnippet        `json:"code_snippet,omitempty"`
	PHPVersion     string              `json:"php_version,omitempty"`
	LaravelVersion string              `json:"laravel_version,omitempty"`
	Route          *RouteContext       `json:"route,omitempty"`
	HTTPRequest    *HTTPRequestContext `json:"http_request,omitempty"`
	Execution      *ExecutionContext   `json:"execution,omitempty"`
	Breadcrumbs    []Breadcrumb        `json:"breadcrumbs,omitempty"`
	System         *SystemContext      `json:"system,omitempty"`
	Context        *AppContext         `json:"context,omitempty"`
}

type RouteContext struct {
	Name       string         `json:"name,omitempty"`
	Action     string         `json:"action,omitempty"`
	Middleware []string       `json:"middleware,omitempty"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

type HTTPRequestContext struct {
	URL         string            `json:"url,omitempty"`
	Method      string            `json:"method,omitempty"`
	QueryString string            `json:"query_string,omitempty"`
	QueryParams map[string]any    `json:"query_params,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	ClientIP    string            `json:"client_ip,omitempty"`
	Body        map[string]any    `json:"body,omitempty"`
}

type QueueJobContext struct {
	JobName    string `json:"job_name,omitempty"`
	Queue      string `json:"queue,omitempty"`
	Connection string `json:"connection,omitempty"`
	Attempts   int    `json:"attempts,omitempty"`
	MaxTries   *int   `json:"max_tries,omitempty"`
	Timeout    int    `json:"timeout,omitempty"`
}

// ExecutionContext — среда выполнения: HTTP, Queue или Console.
type ExecutionContext struct {
	Type     string           `json:"type"` // "http" | "queue" | "console"
	IsCLI    bool             `json:"is_cli"`
	Command  string           `json:"command,omitempty"`
	QueueJob *QueueJobContext `json:"queue_job,omitempty"`
}

type Breadcrumb struct {
	Category  string         `json:"category"` // "sql" | "log" | "http" | "queue" | "cache"
	Level     string         `json:"level,omitempty"` // "info" | "warning" | "error"
	Message   string         `json:"message"`
	Data      map[string]any `json:"data,omitempty"`
	Timestamp float64        `json:"timestamp"`
}

type AppContext struct {
	Shared    map[string]any `json:"shared,omitempty"`
	Exception map[string]any `json:"exception,omitempty"`
}

type SystemContext struct {
	PHPVersion       string  `json:"php_version,omitempty"`
	LaravelVersion   string  `json:"laravel_version,omitempty"`
	MemoryUsageBytes int64   `json:"memory_usage_bytes,omitempty"`
	MemoryPeakBytes  int64   `json:"memory_peak_bytes,omitempty"`
	DurationMS       float64 `json:"duration_ms,omitempty"`
	Hostname         string  `json:"hostname,omitempty"`
	OS               string  `json:"os,omitempty"`
	SAPI             string  `json:"sapi,omitempty"`
	Locale           string  `json:"locale,omitempty"`
	ConfigCached     *bool   `json:"config_cached,omitempty"`
	RoutesCached     *bool   `json:"routes_cached,omitempty"`
}

// RequestContextData объединяет route и HTTP-запрос для хранения в колонке request_context.
type RequestContextData struct {
	Route       *RouteContext       `json:"route,omitempty"`
	HTTPRequest *HTTPRequestContext `json:"http_request,omitempty"`
}

// CodeLine — строка исходного кода рядом с местом ошибки.
type CodeLine struct {
	Number  int    `json:"number"`
	Code    string `json:"code"`
	IsError bool   `json:"is_error"`
}

// CodeSnippet — фрагмент кода вокруг строки, где произошла ошибка.
type CodeSnippet struct {
	File      string     `json:"file"`
	Line      int        `json:"line"`
	Lines     []CodeLine `json:"lines"`
	GitHubURL string     `json:"github_url,omitempty"`
	Source    string     `json:"source,omitempty"` // "client" | "github"
}

// IncomingEvent — внутреннее событие, которое передаётся через канал.
type IncomingEvent struct {
	EventID     string
	ProjectID   int64
	ProjectName string
	ProjectKey  string
	Payload     EventPayload
}

// Project — клиентское приложение.
type Project struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	APIKey       string `json:"api_key"`
	GitHubRepoID int64  `json:"github_repo_id,omitempty"`
	GitHubRepo   string `json:"github_repo,omitempty"`
	GitHubToken  string `json:"-"`
	GitHubBranch string `json:"github_branch,omitempty"`
	PathPrefix   string `json:"path_prefix,omitempty"`
}

var ErrGitHubRepoNotSelected = errors.New("Репозиторий не найден среди выбранных")

// GitHubAccount — аккаунт GitHub, подключённый по Personal Access Token.
type GitHubAccount struct {
	ID            int64
	Name          string
	Login         string
	Token         string `json:"-"`
	ImportAll     bool
	SyncedAt      *time.Time
	SyncError     string
	ReposTotal    int
	ReposSelected int
}

// GitHubRepo — репозиторий из кэша github_repos.
type GitHubRepo struct {
	ID            int64
	AccountID     int64
	AccountName   string
	AccountLogin  string
	FullName      string
	DefaultBranch string
	Private       bool
	Selected      bool
}

// Release — отслеживаемая версия приложения.
type Release struct {
	ID        int64     `json:"id"`
	ProjectID int64     `json:"project_id"`
	Version   string    `json:"version"`
	Ref       string    `json:"ref,omitempty"`
	Commit    string    `json:"commit,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Incident — сгруппированная ошибка, хранимая в БД.
type Incident struct {
	ID                int64          `json:"id"`
	ProjectID         int64          `json:"project_id"`
	ProjectName       string         `json:"project_name"`
	Hash              string         `json:"hash"`
	ExceptionClass    string         `json:"exception_class"`
	Message           string         `json:"message"`
	File              string         `json:"file"`
	Line              int            `json:"line"`
	StackTrace        string         `json:"stack_trace,omitempty"`
	AppURL            string         `json:"app_url"`
	RequestURL        string         `json:"request_url"`
	EventsCount       int            `json:"events_count"`
	FirstSeenAt       time.Time      `json:"first_seen_at"`
	LastSeenAt        time.Time      `json:"last_seen_at"`
	Environment       string         `json:"environment"`
	FirstRelease      string         `json:"first_release"`
	LastRelease       string         `json:"last_release"`
	Status            IncidentStatus `json:"status"`
	ResolvedInRelease string         `json:"resolved_in_release,omitempty"`
	ResolvedAt        *time.Time     `json:"resolved_at,omitempty"`
	IsRegression      bool           `json:"is_regression"`
	CodeSnippet       *CodeSnippet   `json:"code_snippet,omitempty"`
	PHPVersion        string              `json:"php_version,omitempty"`
	LaravelVersion    string              `json:"laravel_version,omitempty"`
	RouteName         string              `json:"route_name,omitempty"`
	RouteAction       string              `json:"route_action,omitempty"`
	ExecutionType     string              `json:"execution_type,omitempty"`
	Route             *RouteContext       `json:"route,omitempty"`
	HTTPRequest       *HTTPRequestContext `json:"http_request,omitempty"`
	Execution         *ExecutionContext   `json:"execution,omitempty"`
	QueueJob          *QueueJobContext    `json:"queue_job,omitempty"`
	System            *SystemContext      `json:"system,omitempty"`
	Breadcrumbs       []Breadcrumb        `json:"breadcrumbs,omitempty"`
	AppContext        *AppContext         `json:"app_context,omitempty"`
}

// IncidentDetail — расширенное представление инцидента с разобранным stacktrace.
type IncidentDetail struct {
	Incident
	AppStackTrace   string          `json:"app_stack_trace"`
	StackTraceStats StackTraceStats `json:"stack_trace_stats"`
	CodeSnippet     *CodeSnippet    `json:"code_snippet,omitempty"`
}

type IncidentFilter struct {
	ProjectID    int64
	Query        string
	Status       string // "unresolved", "resolved", "ignored", "all"
	Environment  string
	Release      string
	IsRegression *bool
	Sort         string // "recent", "count", "first_seen"
	Limit        int
	Offset       int
}

type PageMeta struct {
	Total      int `json:"total"`
	Page       int `json:"page"`
	Limit      int `json:"limit"`
	TotalPages int `json:"total_pages"`
}

type IncidentListResponse struct {
	Items []Incident `json:"items"`
	Meta  PageMeta   `json:"meta"`
}

type ResolveRequest struct {
	Type    string `json:"type"`    // "immediate", "next", "current", "version"
	Version string `json:"version"` // целевая версия, если type == "version"
}

type BatchIncidentsRequest struct {
	Action string          `json:"action"` // "resolve", "reopen", "ignore", "delete"
	IDs    []int64         `json:"ids"`
	Params *ResolveRequest `json:"params,omitempty"`
}

// CreateReleaseRequest — регистрация релиза из CI/CD.
type CreateReleaseRequest struct {
	Project string `json:"project,omitempty"`
	Version string `json:"version"`
	Ref     string `json:"ref,omitempty"`
	Commit  string `json:"commit,omitempty"`
}

// ReleaseSummary — релиз вместе со счётчиками инцидентов.
type ReleaseSummary struct {
	Release
	TotalIncidents   int `json:"total_incidents"`
	NewIncidents     int `json:"new_incidents"`
	RegressionsCount int `json:"regressions_count"`
}

// StatsSummary — сводные метрики для дашборда.
type StatsSummary struct {
	TotalIncidents      int `json:"total_incidents"`
	Unresolved          int `json:"unresolved"`
	Resolved            int `json:"resolved"`
	Ignored             int `json:"ignored"`
	Regressions         int `json:"regressions"`
	EventsLast24H       int `json:"events_last_24h"`
	ActiveReleasesCount int `json:"active_releases_count"`
}

type HealthStatus struct {
	Status    string `json:"status"`
	Database  string `json:"database"`
	QueueSize int    `json:"queue_size"`
	Uptime    string `json:"uptime"`
}

type UpsertResult struct {
	IncidentID   int64
	TotalCount   int
	IsRegression bool
	IsNew        bool
	Incident     Incident
	Ignored      bool // true, если событие относится к уже решённой проблеме из более старого релиза
}

type ProjectRepository interface {
	GetIDByKey(ctx context.Context, apiKey string) (int64, error)
	GetByKey(ctx context.Context, apiKey string) (*Project, error)
	GetByName(ctx context.Context, name string) (*Project, error)
	GetOrCreateByName(ctx context.Context, name string) (*Project, error)
	GetByID(ctx context.Context, id int64) (*Project, error)
	GetAllProjects(ctx context.Context) ([]Project, error)
	UpdateGitHubSettings(ctx context.Context, id, repoID int64, branch, pathPrefix string) error
}

type GitHubAccountRepository interface {
	Create(ctx context.Context, name, token string) (int64, error)
	Get(ctx context.Context, id int64) (*GitHubAccount, error)
	List(ctx context.Context) ([]GitHubAccount, error)
	// Delete отвязывает проекты от репозиториев аккаунта, затем удаляет репозитории и аккаунт.
	Delete(ctx context.Context, id int64) error
	ListRepos(ctx context.Context, accountID int64) ([]GitHubRepo, error)
	ListSelectedRepos(ctx context.Context) ([]GitHubRepo, error)
	// ApplySync приводит кэш к списку repos: новые получают selected = import_all, пропавшие удаляются.
	ApplySync(ctx context.Context, accountID int64, login string, repos []GitHubRepo) error
	SetSyncError(ctx context.Context, accountID int64, msg string) error
	SetSelection(ctx context.Context, accountID int64, importAll bool, selectedIDs []int64) error
}

type ReleaseRepository interface {
	UpsertRelease(ctx context.Context, projectID int64, version string) (*Release, error)
	CreateRelease(ctx context.Context, projectID int64, version, ref, commit string) (*Release, error)
	GetLatestRelease(ctx context.Context, projectID int64) (*Release, error)
	// LatestVersions возвращает версию последнего релиза каждого проекта: project_id → version.
	LatestVersions(ctx context.Context) (map[int64]string, error)
	GetRelease(ctx context.Context, projectID int64, version string) (*Release, error)
	GetReleasesWithStats(ctx context.Context, projectID int64, limit int) ([]ReleaseSummary, error)
	GetIncidentsByRelease(ctx context.Context, projectID int64, version string, newOnly bool) ([]Incident, error)
}

type IncidentRepository interface {
	UpsertIncident(ctx context.Context, event IncomingEvent, hash string) (*UpsertResult, error)
	GetLatestIncidents(ctx context.Context, projectID int64, limit int) ([]Incident, error)
	GetIncidents(ctx context.Context, filter IncidentFilter) ([]Incident, error)
	GetIncidentsCount(ctx context.Context, filter IncidentFilter) (int, error)
	GetIncidentByID(ctx context.Context, id int64) (*Incident, error)
	CountIncidents(ctx context.Context, projectID int64, status string) (int, error)
	// CountByProject возвращает число инцидентов каждого проекта во всех статусах.
	CountByProject(ctx context.Context) (map[int64]int, error)
	CleanOldIncidents(ctx context.Context, daysToKeep int) (int64, error)
	ResolveIncident(ctx context.Context, id int64, resolutionType string, targetVersion string) error
	ReopenIncident(ctx context.Context, id int64) error
	RestoreIncidentState(ctx context.Context, id int64, state IncidentState) error
	IgnoreIncident(ctx context.Context, id int64, ignored bool) error
	DeleteIncident(ctx context.Context, id int64) error
	BatchAction(ctx context.Context, action string, ids []int64, params *ResolveRequest) (int64, error)
	GetStatsSummary(ctx context.Context, projectID int64) (*StatsSummary, error)
}

type AlertSender interface {
	SendMessage(ctx context.Context, text string) error
}

