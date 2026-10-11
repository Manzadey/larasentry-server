package http

import (
	"context"
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
func (m *mockProjectRepo) GetAllProjects(ctx context.Context) ([]domain.Project, error) {
	return []domain.Project{{ID: 1, Name: "Test Project"}}, nil
}

type mockReleaseRepo struct{}

func (m *mockReleaseRepo) UpsertRelease(ctx context.Context, projectID int64, version string) (*domain.Release, error) {
	return &domain.Release{ID: 1, ProjectID: projectID, Version: version}, nil
}
func (m *mockReleaseRepo) GetLatestRelease(ctx context.Context, projectID int64) (*domain.Release, error) {
	return &domain.Release{ID: 1, ProjectID: projectID, Version: "v1.2.3"}, nil
}
func (m *mockReleaseRepo) GetRelease(ctx context.Context, projectID int64, version string) (*domain.Release, error) {
	return &domain.Release{ID: 1, ProjectID: projectID, Version: version}, nil
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
