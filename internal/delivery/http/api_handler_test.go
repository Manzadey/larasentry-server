package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"server/internal/delivery/sse"
	"server/internal/domain"
)

func setupTestAPIHandler(t *testing.T) (*APIHandler, chan domain.IncomingEvent, *domain.Incident) {
	mockInc := &domain.Incident{
		ID:             42,
		ProjectID:      1,
		ProjectName:    "Test Project",
		Hash:           "hash123",
		ExceptionClass: "InvalidArgumentException",
		Message:        "Invalid user parameter",
		File:           "/var/www/app/Http/Controllers/UserController.php",
		Line:           25,
		StackTrace:     "#0 /var/www/vendor/laravel/framework/src/Router.php(50): App\\Http\\Controllers\\UserController->index()\n#1 /var/www/app/Http/Controllers/UserController.php(25): doSomething()",
		Status:         domain.StatusUnresolved,
		EventsCount:    5,
		FirstSeenAt:    time.Now().Add(-1 * time.Hour),
		LastSeenAt:     time.Now(),
		Environment:    "production",
		FirstRelease:   "1.0.0",
		LastRelease:    "1.0.1",
	}

	eventChan := make(chan domain.IncomingEvent, 100)
	broker := sse.NewBroker()
	projectRepo := &mockProjectRepo{}
	incidentRepo := &mockIncidentRepo{incident: mockInc}
	releaseRepo := &mockReleaseRepo{}

	handler := NewAPIHandler(projectRepo, incidentRepo, eventChan)
	handler.SetReleaseRepo(releaseRepo)
	handler.SetSSEBroker(broker)

	return handler, eventChan, mockInc
}

func TestHandleEvent_Success(t *testing.T) {
	handler, eventChan, _ := setupTestAPIHandler(t)

	payload := domain.EventPayload{
		ExceptionClass: "RuntimeException",
		Message:        "Database connection timeout",
		File:           "/app/Services/DB.php",
		Line:           102,
		Release:        "1.0.0",
		Environment:    "production",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/events", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleEvent(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "accepted" {
		t.Errorf("expected status 'accepted', got '%s'", resp["status"])
	}
	if resp["event_id"] == "" {
		t.Errorf("expected non-empty event_id")
	}
	if resp["hash"] == "" {
		t.Errorf("expected non-empty hash")
	}

	select {
	case event := <-eventChan:
		if event.Payload.ExceptionClass != "RuntimeException" {
			t.Errorf("expected event in channel with RuntimeException, got %s", event.Payload.ExceptionClass)
		}
		if event.EventID != resp["event_id"] {
			t.Errorf("expected event channel to have event_id %s, got %s", resp["event_id"], event.EventID)
		}
	default:
		t.Errorf("expected event to be dispatched into eventChan")
	}
}

func TestHandleEvent_NoToken_AutoResolvesDefaultProject(t *testing.T) {
	handler, eventChan, _ := setupTestAPIHandler(t)

	payload := domain.EventPayload{
		ExceptionClass: "RuntimeException",
		Message:        "Failed",
	}
	body, _ := json.Marshal(payload)

	// Без заголовка и токена запрос проходит, проект определяется автоматически
	req := httptest.NewRequest(http.MethodPost, "/api/events", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.HandleEvent(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("expected 202 Accepted for missing token, got %d: %s", rec.Code, rec.Body.String())
	}

	select {
	case event := <-eventChan:
		if event.ProjectName != "Default Project" {
			t.Errorf("expected Default Project, got %s", event.ProjectName)
		}
	default:
		t.Errorf("expected event in eventChan")
	}
}

func TestHandleEvent_WithProjectNameInPayload(t *testing.T) {
	handler, eventChan, _ := setupTestAPIHandler(t)

	payload := domain.EventPayload{
		Project:        "shop-backend",
		ExceptionClass: "QueryException",
		Message:        "Table not found",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/events", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.HandleEvent(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("expected 202 Accepted, got %d: %s", rec.Code, rec.Body.String())
	}

	select {
	case event := <-eventChan:
		if event.ProjectName != "shop-backend" {
			t.Errorf("expected shop-backend, got %s", event.ProjectName)
		}
	default:
		t.Errorf("expected event in eventChan")
	}
}

func TestHandleEvent_WithProjectNameInHeader(t *testing.T) {
	handler, eventChan, _ := setupTestAPIHandler(t)

	payload := domain.EventPayload{
		ExceptionClass: "LogicException",
		Message:        "Bad state",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/events", bytes.NewReader(body))
	req.Header.Set("X-Project", "crm-system")
	rec := httptest.NewRecorder()
	handler.HandleEvent(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("expected 202 Accepted, got %d: %s", rec.Code, rec.Body.String())
	}

	select {
	case event := <-eventChan:
		if event.ProjectName != "crm-system" {
			t.Errorf("expected crm-system, got %s", event.ProjectName)
		}
	default:
		t.Errorf("expected event in eventChan")
	}
}

func TestHandleBatchEvents(t *testing.T) {
	handler, eventChan, _ := setupTestAPIHandler(t)

	reqBody := map[string]any{
		"events": []domain.EventPayload{
			{ExceptionClass: "ErrorOne", Message: "Msg 1"},
			{ExceptionClass: "ErrorTwo", Message: "Msg 2"},
		},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/events/batch", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()

	handler.HandleBatchEvents(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if int(resp["accepted_count"].(float64)) != 2 {
		t.Errorf("expected accepted_count 2, got %v", resp["accepted_count"])
	}

	if len(eventChan) != 2 {
		t.Errorf("expected 2 events in channel, got %d", len(eventChan))
	}
}

func TestHandleListIncidents(t *testing.T) {
	handler, _, _ := setupTestAPIHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/incidents?status=unresolved&page=1&limit=20&q=user", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()

	handler.HandleListIncidents(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp domain.IncidentListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}
	if resp.Meta.Page != 1 || resp.Meta.Limit != 20 {
		t.Errorf("expected page 1, limit 20, got page %d, limit %d", resp.Meta.Page, resp.Meta.Limit)
	}
}

func TestHandleGetIncident(t *testing.T) {
	handler, _, _ := setupTestAPIHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/incidents/42", nil)
	req.SetPathValue("id", "42")
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()

	handler.HandleGetIncident(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var detail domain.IncidentDetail
	if err := json.NewDecoder(rec.Body).Decode(&detail); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if detail.ID != 42 {
		t.Errorf("expected ID 42, got %d", detail.ID)
	}
	if detail.AppStackTrace == "" {
		t.Errorf("expected non-empty AppStackTrace")
	}
}

func TestHandleIncidentLifecycle(t *testing.T) {
	handler, _, _ := setupTestAPIHandler(t)

	resolveBody, _ := json.Marshal(domain.ResolveRequest{Type: "next"})
	req := httptest.NewRequest(http.MethodPost, "/api/incidents/42/resolve", bytes.NewReader(resolveBody))
	req.SetPathValue("id", "42")
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()
	handler.HandleResolveIncident(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve failed with %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/incidents/42/reopen", nil)
	req.SetPathValue("id", "42")
	req.Header.Set("Authorization", "Bearer test-key")
	rec = httptest.NewRecorder()
	handler.HandleReopenIncident(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reopen failed with %d: %s", rec.Code, rec.Body.String())
	}

	ignoreBody, _ := json.Marshal(map[string]bool{"ignored": true})
	req = httptest.NewRequest(http.MethodPost, "/api/incidents/42/ignore", bytes.NewReader(ignoreBody))
	req.SetPathValue("id", "42")
	req.Header.Set("Authorization", "Bearer test-key")
	rec = httptest.NewRecorder()
	handler.HandleIgnoreIncident(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ignore failed with %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/incidents/42", nil)
	req.SetPathValue("id", "42")
	req.Header.Set("Authorization", "Bearer test-key")
	rec = httptest.NewRecorder()
	handler.HandleDeleteIncident(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete failed with %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleBatchIncidents(t *testing.T) {
	handler, _, _ := setupTestAPIHandler(t)

	batchBody, _ := json.Marshal(domain.BatchIncidentsRequest{
		Action: "resolve",
		IDs:    []int64{42},
		Params: &domain.ResolveRequest{Type: "immediate"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/incidents/batch", bytes.NewReader(batchBody))
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()

	handler.HandleBatchIncidents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch action failed with %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["action"] != "resolve" {
		t.Errorf("expected action 'resolve', got %v", resp["action"])
	}
}

func TestHandleReleases(t *testing.T) {
	handler, _, _ := setupTestAPIHandler(t)

	createBody, _ := json.Marshal(domain.CreateReleaseRequest{
		Version: "v1.5.0",
		Ref:     "refs/heads/main",
		Commit:  "abcdef123456",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/releases", bytes.NewReader(createBody))
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()
	handler.HandleCreateRelease(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create release failed with %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/releases", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rec = httptest.NewRecorder()
	handler.HandleListReleases(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list releases failed with %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/releases/v1.5.0/incidents", nil)
	req.SetPathValue("version", "v1.5.0")
	req.Header.Set("Authorization", "Bearer test-key")
	rec = httptest.NewRecorder()
	handler.HandleReleaseIncidents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("release incidents failed with %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleStatsSummary(t *testing.T) {
	handler, _, _ := setupTestAPIHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/stats/summary", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()

	handler.HandleStatsSummary(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats summary failed with %d: %s", rec.Code, rec.Body.String())
	}

	var summary domain.StatsSummary
	if err := json.NewDecoder(rec.Body).Decode(&summary); err != nil {
		t.Fatalf("failed to decode summary: %v", err)
	}
	if summary.TotalIncidents != 1 {
		t.Errorf("expected TotalIncidents 1, got %d", summary.TotalIncidents)
	}
}

func TestHandleHealthCheck(t *testing.T) {
	handler, _, _ := setupTestAPIHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()

	handler.HandleHealthCheck(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health check failed with %d: %s", rec.Code, rec.Body.String())
	}

	var status domain.HealthStatus
	json.NewDecoder(rec.Body).Decode(&status)
	if status.Status != "ok" || status.Database != "healthy" {
		t.Errorf("expected ok & healthy, got status: %s, database: %s", status.Status, status.Database)
	}
}

func TestHandleUpdateProjectGitHub(t *testing.T) {
	handler, _, _ := setupTestAPIHandler(t)

	body := map[string]any{
		"github_repo_id": 7,
		"github_branch":  "main",
		"path_prefix":    "/var/www/html",
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPut, "/api/projects/1/github", bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()

	handler.HandleUpdateProjectGitHub(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var res map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if res["status"] != "ok" {
		t.Errorf("expected status 'ok', got %v", res["status"])
	}
}

func TestHandleUpdateProjectGitHub_RejectsLegacyBody(t *testing.T) {
	handler, _, _ := setupTestAPIHandler(t)

	body := `{"github_repo":"org/app","github_token":"ghp_x","github_branch":"main"}`
	req := httptest.NewRequest(http.MethodPut, "/api/projects/1/github", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()

	handler.HandleUpdateProjectGitHub(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("legacy body without github_repo_id must be rejected, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleUpdateProjectGitHub_RejectsUnselectedRepo(t *testing.T) {
	handler, _, _ := setupTestAPIHandler(t)

	req := httptest.NewRequest(http.MethodPut, "/api/projects/1/github", bytes.NewReader([]byte(`{"github_repo_id":999}`)))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()

	handler.HandleUpdateProjectGitHub(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleEvent_DecodesAppContext(t *testing.T) {
	handler, eventChan, _ := setupTestAPIHandler(t)

	body := []byte(`{"exception_class":"RuntimeException","message":"m","file":"f.php","line":1,` +
		`"release":"1.0.0","environment":"production",` +
		`"context":{"shared":{"trace_id":"t-1"},"exception":{"order_id":42}}}`)

	req := httptest.NewRequest(http.MethodPost, "/api/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleEvent(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d: %s", rec.Code, rec.Body.String())
	}

	select {
	case event := <-eventChan:
		c := event.Payload.Context
		if c == nil || c.Shared["trace_id"] != "t-1" || c.Exception["order_id"] != float64(42) {
			t.Errorf("unexpected decoded context: %+v", c)
		}
	default:
		t.Fatal("expected event to be dispatched into eventChan")
	}
}

type failingProjectRepo struct{ mockProjectRepo }

func (*failingProjectRepo) GetOrCreateByName(ctx context.Context, name string) (*domain.Project, error) {
	return nil, errors.New("database is locked")
}

func TestReportsFailCleanlyWhenProjectUnresolved(t *testing.T) {
	handler := NewAPIHandler(&failingProjectRepo{}, &mockIncidentRepo{}, nil)
	handler.SetReleaseRepo(&mockReleaseRepo{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/releases/{version}/incidents", handler.HandleReleaseIncidents)
	mux.HandleFunc("GET /api/stats/summary", handler.HandleStatsSummary)

	for _, path := range []string{"/api/releases/1.0.0/incidents", "/api/stats/summary"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: got %d, want 500", path, rec.Code)
		}
	}
}
