package sqlite

import (
	"context"
	"os"
	"testing"

	"server/internal/domain"
)

func setupTestDB(t *testing.T) (*IncidentRepo, *ReleaseRepo, func()) {
	tmpFile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatalf("failed to create temp db: %v", err)
	}
	tmpFile.Close()

	db, err := InitDB(tmpFile.Name())
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("failed to init db: %v", err)
	}

	relRepo := &ReleaseRepo{db: db}
	incRepo := &IncidentRepo{db: db, releaseRepo: relRepo}

	cleanup := func() {
		db.Close()
		os.Remove(tmpFile.Name())
		os.Remove(tmpFile.Name() + "-shm")
		os.Remove(tmpFile.Name() + "-wal")
	}

	return incRepo, relRepo, cleanup
}

func TestIncidentLifecycleAndRegression(t *testing.T) {
	incRepo, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// 1. New event in version 1.0.0
	evt1 := domain.IncomingEvent{
		ProjectID:   1,
		ProjectName: "Default Project",
		ProjectKey:  "test-api-key",
		Payload: domain.EventPayload{
			ExceptionClass: "InvalidArgumentException",
			Message:        "Invalid user ID",
			File:           "app/Services/UserService.php",
			Line:           25,
			Release:        "1.0.0",
		},
	}
	hash := "testhash123"

	res1, err := incRepo.UpsertIncident(ctx, evt1, hash)
	if err != nil {
		t.Fatalf("UpsertIncident failed: %v", err)
	}
	if !res1.IsNew || res1.TotalCount != 1 || res1.IsRegression || res1.Ignored {
		t.Errorf("unexpected res1: %+v", res1)
	}

	// Verify incident in DB
	inc, err := incRepo.GetIncidentByID(ctx, res1.IncidentID)
	if err != nil {
		t.Fatalf("GetIncidentByID failed: %v", err)
	}
	if inc.Status != domain.StatusUnresolved || inc.FirstRelease != "1.0.0" {
		t.Errorf("expected unresolved status and 1.0.0 first release, got: %+v", inc)
	}

	// 2. Resolve incident in version 1.0.0
	if err := incRepo.ResolveIncident(ctx, inc.ID, "version", "1.0.0"); err != nil {
		t.Fatalf("ResolveIncident failed: %v", err)
	}

	inc, _ = incRepo.GetIncidentByID(ctx, inc.ID)
	if inc.Status != domain.StatusResolved || inc.ResolvedInRelease != "1.0.0" {
		t.Errorf("expected resolved status in 1.0.0, got: %+v", inc)
	}

	// 3. Old event arrives from version 0.9.0 -> should be ignored (status remains resolved)
	evtOld := evt1
	evtOld.Payload.Release = "0.9.0"
	resOld, err := incRepo.UpsertIncident(ctx, evtOld, hash)
	if err != nil {
		t.Fatalf("UpsertIncident old failed: %v", err)
	}
	if !resOld.Ignored || resOld.IsRegression {
		t.Errorf("expected Ignored=true and IsRegression=false for older version, got: %+v", resOld)
	}
	inc, _ = incRepo.GetIncidentByID(ctx, inc.ID)
	if inc.Status != domain.StatusResolved {
		t.Errorf("expected status to remain resolved, got %s", inc.Status)
	}

	// 4. Newer event arrives from version 1.1.0 -> REGRESSION!
	evtNew := evt1
	evtNew.Payload.Release = "1.1.0"
	resNew, err := incRepo.UpsertIncident(ctx, evtNew, hash)
	if err != nil {
		t.Fatalf("UpsertIncident new failed: %v", err)
	}
	if resNew.Ignored || !resNew.IsRegression {
		t.Errorf("expected Ignored=false and IsRegression=true for newer version, got: %+v", resNew)
	}
	inc, _ = incRepo.GetIncidentByID(ctx, inc.ID)
	if inc.Status != domain.StatusUnresolved || !inc.IsRegression || inc.LastRelease != "1.1.0" {
		t.Errorf("expected status=unresolved, isRegression=true, got: %+v", inc)
	}

	// 5. Test Reopen
	_ = incRepo.ResolveIncident(ctx, inc.ID, "immediate", "")
	if err := incRepo.ReopenIncident(ctx, inc.ID); err != nil {
		t.Fatalf("ReopenIncident failed: %v", err)
	}
	inc, _ = incRepo.GetIncidentByID(ctx, inc.ID)
	if inc.Status != domain.StatusUnresolved || inc.ResolvedInRelease != "" {
		t.Errorf("expected unresolved status after reopen, got: %+v", inc)
	}
}

func TestIncident_CodeSnippetPersistence(t *testing.T) {
	incRepo, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	snippet := &domain.CodeSnippet{
		File: "app/Http/Controllers/OrderController.php",
		Line: 42,
		Lines: []domain.CodeLine{
			{Number: 41, Code: "    $cart = $this->cartService->get();", IsError: false},
			{Number: 42, Code: "    $total = $cart->items->sum('price');", IsError: true},
			{Number: 43, Code: "    return response()->json(['total' => $total]);", IsError: false},
		},
		Source: "client",
	}

	evt := domain.IncomingEvent{
		ProjectID:   1,
		ProjectName: "Default Project",
		ProjectKey:  "test-api-key",
		Payload: domain.EventPayload{
			ExceptionClass: "Error",
			Message:        "Call to a member function sum() on null",
			File:           "/var/www/html/app/Http/Controllers/OrderController.php",
			Line:           42,
			CodeSnippet:    snippet,
		},
	}
	hash := "snippethash123"

	res, err := incRepo.UpsertIncident(ctx, evt, hash)
	if err != nil {
		t.Fatalf("UpsertIncident failed: %v", err)
	}

	inc, err := incRepo.GetIncidentByID(ctx, res.IncidentID)
	if err != nil {
		t.Fatalf("GetIncidentByID failed: %v", err)
	}
	if inc.CodeSnippet == nil {
		t.Fatalf("expected CodeSnippet to be persisted, but got nil")
	}
	if inc.CodeSnippet.Line != 42 || len(inc.CodeSnippet.Lines) != 3 {
		t.Errorf("unexpected CodeSnippet content: %+v", inc.CodeSnippet)
	}
	if inc.CodeSnippet.Lines[1].IsError != true {
		t.Errorf("expected line 42 to have IsError=true")
	}
}

func TestIncident_PHPVersionPersistence(t *testing.T) {
	incRepo, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Ingest event with direct PHPVersion
	evt1 := domain.IncomingEvent{
		ProjectID:   1,
		ProjectName: "Default Project",
		ProjectKey:  "test-api-key",
		Payload: domain.EventPayload{
			ExceptionClass: "Error",
			Message:        "Direct PHP version test",
			File:           "app/Services/Test.php",
			Line:           10,
			PHPVersion:     "8.2.18",
		},
	}
	res1, err := incRepo.UpsertIncident(ctx, evt1, "hash-php-1")
	if err != nil {
		t.Fatalf("UpsertIncident failed: %v", err)
	}

	inc1, err := incRepo.GetIncidentByID(ctx, res1.IncidentID)
	if err != nil {
		t.Fatalf("GetIncidentByID failed: %v", err)
	}
	if inc1.PHPVersion != "8.2.18" {
		t.Errorf("expected PHPVersion 8.2.18, got %q", inc1.PHPVersion)
	}

	// 2. Ingest event with PHP version inside tags["php_version"]
	evt2 := domain.IncomingEvent{
		ProjectID:   1,
		ProjectName: "Default Project",
		ProjectKey:  "test-api-key",
		Payload: domain.EventPayload{
			ExceptionClass: "Error",
			Message:        "Tag PHP version test",
			File:           "app/Services/Test2.php",
			Line:           20,
			Tags: map[string]string{
				"php_version": "8.3.4",
			},
		},
	}
	res2, err := incRepo.UpsertIncident(ctx, evt2, "hash-php-2")
	if err != nil {
		t.Fatalf("UpsertIncident failed: %v", err)
	}

	inc2, err := incRepo.GetIncidentByID(ctx, res2.IncidentID)
	if err != nil {
		t.Fatalf("GetIncidentByID failed: %v", err)
	}
	if inc2.PHPVersion != "8.3.4" {
		t.Errorf("expected PHPVersion 8.3.4 from tags, got %q", inc2.PHPVersion)
	}

	// 3. Test list incidents returns PHPVersion
	list, err := incRepo.GetIncidents(ctx, domain.IncidentFilter{Status: "all"})
	if err != nil {
		t.Fatalf("GetIncidents failed: %v", err)
	}
	found := false
	for _, item := range list {
		if item.ID == inc1.ID && item.PHPVersion == "8.2.18" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected to find incident with PHPVersion in GetIncidents list")
	}
}

