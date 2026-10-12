package http

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"server/internal/delivery/sse"
	"server/internal/domain"
)

type mockProjectRepo struct{}

func (m *mockProjectRepo) GetIDByKey(ctx context.Context, apiKey string) (int64, error) {
	return 1, nil
}
func (m *mockProjectRepo) GetByKey(ctx context.Context, apiKey string) (*domain.Project, error) {
	return &domain.Project{ID: 1, Name: "Test Project", APIKey: "test-key"}, nil
}
func (m *mockProjectRepo) GetByName(ctx context.Context, name string) (*domain.Project, error) {
	if name == "" {
		name = "Default Project"
	}
	return &domain.Project{ID: 1, Name: name, APIKey: "test-key"}, nil
}
func (m *mockProjectRepo) GetOrCreateByName(ctx context.Context, name string) (*domain.Project, error) {
	if name == "" {
		name = "Default Project"
	}
	return &domain.Project{ID: 1, Name: name, APIKey: "test-key"}, nil
}
func (m *mockProjectRepo) GetAllProjects(ctx context.Context) ([]domain.Project, error) {
	return []domain.Project{{ID: 1, Name: "Test Project"}}, nil
}
func (m *mockProjectRepo) GetByID(ctx context.Context, id int64) (*domain.Project, error) {
	return &domain.Project{ID: id, Name: "Test Project", APIKey: "test-key"}, nil
}
func (m *mockProjectRepo) UpdateGitHubSettings(ctx context.Context, id, repoID int64, branch, pathPrefix string) error {
	if repoID == 999 {
		return domain.ErrGitHubRepoNotSelected
	}
	return nil
}

type mockReleaseRepo struct{}

func (m *mockReleaseRepo) UpsertRelease(ctx context.Context, projectID int64, version string) (*domain.Release, error) {
	return &domain.Release{ID: 1, ProjectID: projectID, Version: version}, nil
}
func (m *mockReleaseRepo) CreateRelease(ctx context.Context, projectID int64, version, ref, commit string) (*domain.Release, error) {
	return &domain.Release{ID: 1, ProjectID: projectID, Version: version, Ref: ref, Commit: commit}, nil
}
func (m *mockReleaseRepo) GetLatestRelease(ctx context.Context, projectID int64) (*domain.Release, error) {
	return &domain.Release{ID: 1, ProjectID: projectID, Version: "v1.2.3"}, nil
}
func (m *mockReleaseRepo) GetRelease(ctx context.Context, projectID int64, version string) (*domain.Release, error) {
	return &domain.Release{ID: 1, ProjectID: projectID, Version: version}, nil
}
func (m *mockReleaseRepo) GetReleasesWithStats(ctx context.Context, projectID int64, limit int) ([]domain.ReleaseSummary, error) {
	return nil, nil
}
func (m *mockReleaseRepo) GetIncidentsByRelease(ctx context.Context, projectID int64, version string, newOnly bool) ([]domain.Incident, error) {
	return nil, nil
}

type mockIncidentRepo struct {
	incident *domain.Incident
}

func (m *mockIncidentRepo) UpsertIncident(ctx context.Context, event domain.IncomingEvent, hash string) (*domain.UpsertResult, error) {
	return &domain.UpsertResult{IncidentID: 1, TotalCount: 1}, nil
}
func (m *mockIncidentRepo) GetLatestIncidents(ctx context.Context, projectID int64, limit int) ([]domain.Incident, error) {
	return []domain.Incident{*m.incident}, nil
}
func (m *mockIncidentRepo) GetIncidents(ctx context.Context, filter domain.IncidentFilter) ([]domain.Incident, error) {
	return []domain.Incident{*m.incident}, nil
}
func (m *mockIncidentRepo) GetIncidentsCount(ctx context.Context, filter domain.IncidentFilter) (int, error) {
	return 1, nil
}
func (m *mockIncidentRepo) GetIncidentByID(ctx context.Context, id int64) (*domain.Incident, error) {
	return m.incident, nil
}
func (m *mockIncidentRepo) CountIncidents(ctx context.Context, projectID int64, status string) (int, error) {
	return 1, nil
}
func (m *mockIncidentRepo) CleanOldIncidents(ctx context.Context, daysToKeep int) (int64, error) {
	return 0, nil
}
func (m *mockIncidentRepo) ResolveIncident(ctx context.Context, id int64, resolutionType string, targetVersion string) error {
	return nil
}
func (m *mockIncidentRepo) ReopenIncident(ctx context.Context, id int64) error {
	return nil
}
func (m *mockIncidentRepo) IgnoreIncident(ctx context.Context, id int64, ignored bool) error {
	return nil
}
func (m *mockIncidentRepo) DeleteIncident(ctx context.Context, id int64) error {
	return nil
}
func (m *mockIncidentRepo) BatchAction(ctx context.Context, action string, ids []int64, params *domain.ResolveRequest) (int64, error) {
	return int64(len(ids)), nil
}
func (m *mockIncidentRepo) GetStatsSummary(ctx context.Context, projectID int64) (*domain.StatsSummary, error) {
	return &domain.StatsSummary{TotalIncidents: 1}, nil
}

func TestHandleIncidentDetail_WithStackTraceFilter(t *testing.T) {
	sampleTrace := `#0 /var/www/html/vendor/laravel/framework/src/Illuminate/Database/Connection.php(712): Illuminate\Database\Connection->runQueryCallback()
#1 /var/www/html/app/Http/Controllers/UserController.php(42): App\Models\User::all()
#2 /var/www/html/public/index.php(52): App\Http\Kernel->handle()`

	mockInc := &domain.Incident{
		ID:             42,
		ProjectID:      1,
		ProjectName:    "Test Project",
		Hash:           "hash123",
		ExceptionClass: "QueryException",
		Message:        "SQLSTATE[HY000]: General error",
		File:           "/var/www/html/app/Http/Controllers/UserController.php",
		Line:           42,
		StackTrace:     sampleTrace,
		EventsCount:    1,
		FirstSeenAt:    time.Now(),
		LastSeenAt:     time.Now(),
	}

	handler, err := NewUIHandler(&mockProjectRepo{}, &mockIncidentRepo{incident: mockInc}, &mockReleaseRepo{}, sse.NewBroker())
	if err != nil {
		t.Fatalf("failed to create UIHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ui/incidents/42", nil)
	rec := httptest.NewRecorder()

	handler.HandleIncidentDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()

	// Verify buttons and elements are rendered
	if !strings.Contains(body, "Без vendor") {
		t.Errorf("expected body to contain 'Без vendor'")
	}
	if !strings.Contains(body, "app-stack-trace") {
		t.Errorf("expected body to contain 'app-stack-trace'")
	}
	if !strings.Contains(body, "raw-stack-trace") {
		t.Errorf("expected body to contain 'raw-stack-trace'")
	}
	// Check that filtered trace contains UserController.php and does not contain Connection.php
	if !strings.Contains(body, "UserController.php(42)") {
		t.Errorf("expected body to contain UserController.php(42)")
	}
}

func TestHandleIncidentDetail_ManualResolveAndReopen(t *testing.T) {
	mockInc := &domain.Incident{
		ID:             42,
		ProjectID:      1,
		ProjectName:    "Test Project",
		Hash:           "hash123",
		ExceptionClass: "QueryException",
		Message:        "SQLSTATE[HY000]: General error",
		File:           "/var/www/html/app/Http/Controllers/UserController.php",
		Line:           42,
		Status:         domain.StatusUnresolved,
		EventsCount:    1,
		FirstSeenAt:    time.Now(),
		LastSeenAt:     time.Now(),
	}

	handler, err := NewUIHandler(&mockProjectRepo{}, &mockIncidentRepo{incident: mockInc}, &mockReleaseRepo{}, sse.NewBroker())
	if err != nil {
		t.Fatalf("failed to create UIHandler: %v", err)
	}

	// 1. Resolve request with view=row
	reqResolve := httptest.NewRequest(http.MethodPost, "/ui/incidents/42/resolve?type=immediate&view=row", nil)
	recResolve := httptest.NewRecorder()
	handler.HandleIncidentDetail(recResolve, reqResolve)

	if recResolve.Code != http.StatusOK {
		t.Fatalf("expected status 200 on resolve, got %d", recResolve.Code)
	}
	if recResolve.Header().Get("HX-Trigger") != "incident-updated" {
		t.Errorf("expected HX-Trigger header to be 'incident-updated'")
	}

	// 2. Reopen request
	reqReopen := httptest.NewRequest(http.MethodPost, "/ui/incidents/42/reopen?view=row", nil)
	recReopen := httptest.NewRecorder()
	handler.HandleIncidentDetail(recReopen, reqReopen)

	if recReopen.Code != http.StatusOK {
		t.Fatalf("expected status 200 on reopen, got %d", recReopen.Code)
	}
	if recReopen.Header().Get("HX-Trigger") != "incident-updated" {
		t.Errorf("expected HX-Trigger header on reopen to be 'incident-updated'")
	}
}

func TestHandleIncidentDetail_WithGitHubAndSnippet(t *testing.T) {
	mockInc := &domain.Incident{
		ID:             100,
		ProjectID:      1,
		ProjectName:    "Test Project",
		Hash:           "hash100",
		ExceptionClass: "RuntimeException",
		Message:        "Something went wrong",
		File:           "/var/www/html/app/Http/Controllers/OrderController.php",
		Line:           42,
		Status:         domain.StatusUnresolved,
		EventsCount:    1,
		FirstSeenAt:    time.Now(),
		LastSeenAt:     time.Now(),
		CodeSnippet: &domain.CodeSnippet{
			File: "app/Http/Controllers/OrderController.php",
			Line: 42,
			Lines: []domain.CodeLine{
				{Number: 41, Code: "    $cart = $request->user()->cart();", IsError: false},
				{Number: 42, Code: "    $total = $cart->items->sum('price');", IsError: true},
				{Number: 43, Code: "    return response()->json(['total' => $total]);", IsError: false},
			},
			GitHubURL: "https://github.com/myorg/myapp/blob/main/app/Http/Controllers/OrderController.php#L42",
			Source:    "client",
		},
	}

	handler, err := NewUIHandler(&mockProjectRepo{}, &mockIncidentRepo{incident: mockInc}, &mockReleaseRepo{}, sse.NewBroker())
	if err != nil {
		t.Fatalf("failed to create UIHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ui/incidents/100", nil)
	rec := httptest.NewRecorder()
	handler.HandleIncidentDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	// Should render the GitHub link
	if !strings.Contains(body, "https://github.com/myorg/myapp/blob/main/app/Http/Controllers/OrderController.php#L42") {
		t.Errorf("rendered HTML does not contain GitHub link")
	}
	// Should render snippet code lines
	if !strings.Contains(body, "$total = $cart") {
		t.Errorf("rendered HTML does not contain snippet code")
	}
}

func TestHandleIncidentDetail_PHPVersion(t *testing.T) {
	mockInc := &domain.Incident{
		ID:             55,
		ProjectID:      1,
		ProjectName:    "Test Project",
		Hash:           "hash55",
		ExceptionClass: "DivisionByZeroError",
		Message:        "Division by zero",
		File:           "/var/www/html/app/Http/Controllers/MathController.php",
		Line:           15,
		Status:         domain.StatusUnresolved,
		EventsCount:    2,
		FirstSeenAt:    time.Now(),
		LastSeenAt:     time.Now(),
		PHPVersion:     "8.2.18",
		LaravelVersion: "11.28.0",
	}

	handler, err := NewUIHandler(&mockProjectRepo{}, &mockIncidentRepo{incident: mockInc}, &mockReleaseRepo{}, sse.NewBroker())
	if err != nil {
		t.Fatalf("failed to create UIHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ui/incidents/55", nil)
	rec := httptest.NewRecorder()
	handler.HandleIncidentDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "PHP 8.2.18") {
		t.Errorf("expected body to contain 'PHP 8.2.18', got: %s", body)
	}
	if !strings.Contains(body, "Версия PHP") {
		t.Errorf("expected body to contain 'Версия PHP'")
	}
	if !strings.Contains(body, "Laravel 11.28.0") {
		t.Errorf("expected body to contain 'Laravel 11.28.0', got: %s", body)
	}
	if !strings.Contains(body, "Фреймворк (Laravel)") {
		t.Errorf("expected body to contain 'Фреймворк (Laravel)'")
	}
}

func TestHandleUpdateProjectGitHubSettings_Form(t *testing.T) {
	handler, err := NewUIHandler(&mockProjectRepo{}, &mockIncidentRepo{}, &mockReleaseRepo{}, sse.NewBroker())
	if err != nil {
		t.Fatalf("failed to create UIHandler: %v", err)
	}

	form := strings.NewReader("project_id=1&github_repo_id=7&github_branch=main&path_prefix=/var/www")
	req := httptest.NewRequest(http.MethodPost, "/ui/projects/github-settings", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handler.HandleUpdateProjectGitHubSettings(rec, req)

	if rec.Code != http.StatusSeeOther && rec.Code != http.StatusOK {
		t.Fatalf("expected status 303 or 200, got %d", rec.Code)
	}
	if rec.Header().Get("HX-Trigger") != "github-settings-updated" {
		t.Errorf("expected HX-Trigger 'github-settings-updated'")
	}
}

func TestHandleUpdateProjectGitHubSettings_RejectsUnselectedRepo(t *testing.T) {
	handler, err := NewUIHandler(&mockProjectRepo{}, &mockIncidentRepo{}, &mockReleaseRepo{}, sse.NewBroker())
	if err != nil {
		t.Fatalf("failed to create UIHandler: %v", err)
	}

	form := strings.NewReader("project_id=1&github_repo_id=999&github_branch=main&path_prefix=")
	req := httptest.NewRequest(http.MethodPost, "/ui/projects/github-settings", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handler.HandleUpdateProjectGitHubSettings(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleIncidentDetail_ExtendedContextTabs(t *testing.T) {
	maxTries := 5
	configCached := true
	mockInc := &domain.Incident{
		ID:             77,
		ProjectID:      1,
		ProjectName:    "Shop Project",
		Hash:           "hash77",
		ExceptionClass: "PaymentFailedException",
		Message:        "Stripe charge failed",
		File:           "app/Services/StripeService.php",
		Line:           92,
		Status:         domain.StatusUnresolved,
		EventsCount:    3,
		FirstSeenAt:    time.Now().Add(-10 * time.Minute),
		LastSeenAt:     time.Now(),
		RouteName:      "orders.checkout",
		RouteAction:    "App\\Http\\Controllers\\OrderController@checkout",
		ExecutionType:  "queue",
		Route: &domain.RouteContext{
			Name:       "orders.checkout",
			Action:     "App\\Http\\Controllers\\OrderController@checkout",
			Middleware: []string{"web", "auth:sanctum"},
		},
		HTTPRequest: &domain.HTTPRequestContext{
			URL:      "https://shop.example.com/checkout",
			Method:   "POST",
			ClientIP: "192.168.1.100",
			Headers:  map[string]string{"user-agent": "Mozilla/5.0"},
			Body:     map[string]any{"amount": 500},
		},
		QueueJob: &domain.QueueJobContext{
			JobName:  "App\\Jobs\\ProcessPaymentJob",
			Queue:    "payments-high",
			Attempts: 2,
			MaxTries: &maxTries,
			Timeout:  120,
		},
		System: &domain.SystemContext{
			PHPVersion:       "8.3.4",
			LaravelVersion:   "11.28.0",
			MemoryUsageBytes: 14680064,
			MemoryPeakBytes:  27262976,
			DurationMS:       312.45,
			Hostname:         "worker-pod-7",
			OS:               "Linux 6.1.0",
			SAPI:             "cli",
			ConfigCached:     &configCached,
		},
		Breadcrumbs: []domain.Breadcrumb{
			{
				Category:  "sql",
				Level:     "info",
				Message:   "SELECT * FROM orders WHERE id = 4910",
				Timestamp: 1728676510.12,
			},
			{
				Category:  "http",
				Level:     "error",
				Message:   "POST https://api.stripe.com/v1/charges [500 Server Error]",
				Timestamp: 1728676515.34,
			},
		},
	}

	handler, err := NewUIHandler(&mockProjectRepo{}, &mockIncidentRepo{incident: mockInc}, &mockReleaseRepo{}, sse.NewBroker())
	if err != nil {
		t.Fatalf("failed to create UIHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ui/incidents/77", nil)
	rec := httptest.NewRecorder()
	handler.HandleIncidentDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()

	// 1. Tab buttons
	if !strings.Contains(body, "tab-btn-stack") || !strings.Contains(body, "tab-btn-breadcrumbs") ||
		!strings.Contains(body, "tab-btn-request") || !strings.Contains(body, "tab-btn-system") {
		t.Errorf("expected tab navigation buttons in rendered HTML")
	}

	// 2. Breadcrumbs content
	if !strings.Contains(body, "SELECT * FROM orders WHERE id = 4910") {
		t.Errorf("expected breadcrumb SQL message in body")
	}
	if !strings.Contains(body, "https://api.stripe.com/v1/charges") {
		t.Errorf("expected breadcrumb HTTP message in body")
	}

	// 3. Request / Queue content
	if !strings.Contains(body, "orders.checkout") {
		t.Errorf("expected route name 'orders.checkout' in body")
	}
	if !strings.Contains(body, "App\\Jobs\\ProcessPaymentJob") {
		t.Errorf("expected job name 'App\\Jobs\\ProcessPaymentJob' in body")
	}
	if !strings.Contains(body, "payments-high") {
		t.Errorf("expected queue name 'payments-high' in body")
	}

	// 4. System content
	if !strings.Contains(body, "worker-pod-7") {
		t.Errorf("expected hostname 'worker-pod-7' in body")
	}
	if !strings.Contains(body, "Linux 6.1.0") {
		t.Errorf("expected OS 'Linux 6.1.0' in body")
	}
}

func renderIncidentDetail(t *testing.T, inc *domain.Incident) string {
	t.Helper()
	handler, err := NewUIHandler(&mockProjectRepo{}, &mockIncidentRepo{incident: inc}, &mockReleaseRepo{}, sse.NewBroker())
	if err != nil {
		t.Fatalf("failed to create UIHandler: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/ui/incidents/%d", inc.ID), nil)
	rec := httptest.NewRecorder()
	handler.HandleIncidentDetail(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	return rec.Body.String()
}

func newContextIncident(appCtx *domain.AppContext) *domain.Incident {
	return &domain.Incident{
		ID:             78,
		ProjectID:      1,
		ProjectName:    "Shop Project",
		Hash:           "hash78",
		ExceptionClass: "InvalidOrderException",
		Message:        "Order is invalid",
		File:           "app/Services/OrderService.php",
		Line:           10,
		Status:         domain.StatusUnresolved,
		EventsCount:    1,
		FirstSeenAt:    time.Now(),
		LastSeenAt:     time.Now(),
		AppContext:     appCtx,
	}
}

func TestHandleIncidentDetail_AppContextTab(t *testing.T) {
	body := renderIncidentDetail(t, newContextIncident(&domain.AppContext{
		Shared:    map[string]any{"trace_id": "t-1"},
		Exception: map[string]any{"_truncated": true, "_size": float64(20000)},
	}))

	for _, want := range []string{"tab-btn-context", "tab-content-context", "trace_id", "t-1", "Контекст больше лимита (20000 байт)"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in rendered HTML", want)
		}
	}
}

func TestHandleIncidentDetail_AppContextEmpty(t *testing.T) {
	body := renderIncidentDetail(t, newContextIncident(nil))

	if !strings.Contains(body, "Приложение не передало контекст") {
		t.Errorf("expected empty-context placeholder in rendered HTML")
	}
}

func TestHandleIncidentDetail_AppContextKeepsAmpersandReadable(t *testing.T) {
	body := renderIncidentDetail(t, newContextIncident(&domain.AppContext{
		Shared: map[string]any{"url": "https://shop.test/?a=1&b=2"},
	}))

	if strings.Contains(body, `\u0026`) || !strings.Contains(body, "a=1&amp;b=2") {
		t.Errorf("expected URL with readable ampersand in rendered HTML")
	}
}

func TestPlural(t *testing.T) {
	for n, want := range map[int]string{0: "ошибок", 1: "ошибка", 3: "ошибки", 5: "ошибок", 11: "ошибок", 12: "ошибок", 21: "ошибка", 22: "ошибки", 111: "ошибок"} {
		if got := plural(n, "ошибка", "ошибки", "ошибок"); got != want {
			t.Errorf("plural(%d) = %q, want %q", n, got, want)
		}
	}
}
