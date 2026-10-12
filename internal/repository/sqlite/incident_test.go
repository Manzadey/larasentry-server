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

	// 1. Ingest event with direct PHPVersion and LaravelVersion
	evt1 := domain.IncomingEvent{
		ProjectID:   1,
		ProjectName: "Default Project",
		ProjectKey:  "test-api-key",
		Payload: domain.EventPayload{
			ExceptionClass: "Error",
			Message:        "Direct PHP and Laravel version test",
			File:           "app/Services/Test.php",
			Line:           10,
			PHPVersion:     "8.2.18",
			LaravelVersion: "11.28.0",
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
	if inc1.LaravelVersion != "11.28.0" {
		t.Errorf("expected LaravelVersion 11.28.0, got %q", inc1.LaravelVersion)
	}

	// 2. Ingest event with versions inside tags
	evt2 := domain.IncomingEvent{
		ProjectID:   1,
		ProjectName: "Default Project",
		ProjectKey:  "test-api-key",
		Payload: domain.EventPayload{
			ExceptionClass: "Error",
			Message:        "Tag PHP and Laravel version test",
			File:           "app/Services/Test2.php",
			Line:           20,
			Tags: map[string]string{
				"php_version":     "8.3.4",
				"laravel_version": "10.48.2",
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
	if inc2.LaravelVersion != "10.48.2" {
		t.Errorf("expected LaravelVersion 10.48.2 from tags, got %q", inc2.LaravelVersion)
	}

	// 3. Test list incidents returns PHPVersion and LaravelVersion
	list, err := incRepo.GetIncidents(ctx, domain.IncidentFilter{Status: "all"})
	if err != nil {
		t.Fatalf("GetIncidents failed: %v", err)
	}
	found := false
	for _, item := range list {
		if item.ID == inc1.ID && item.PHPVersion == "8.2.18" && item.LaravelVersion == "11.28.0" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected to find incident with PHPVersion and LaravelVersion in GetIncidents list")
	}
}

func TestIncident_ExtendedContextPersistence(t *testing.T) {
	incRepo, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Ingest event with full extended context
	maxTries := 5
	configCached := true
	evt := domain.IncomingEvent{
		ProjectID:   1,
		ProjectName: "Default Project",
		ProjectKey:  "test-api-key",
		Payload: domain.EventPayload{
			ExceptionClass: "OrderProcessingException",
			Message:        "Payment gateway timed out",
			File:           "app/Services/OrderService.php",
			Line:           84,
			Release:        "2.4.0",
			Environment:    "production",
			Route: &domain.RouteContext{
				Name:       "orders.checkout",
				Action:     "App\\Http\\Controllers\\OrderController@checkout",
				Middleware: []string{"web", "auth:sanctum"},
				Parameters: map[string]any{"cart_id": "4910"},
			},
			HTTPRequest: &domain.HTTPRequestContext{
				URL:         "https://shop.example.com/checkout",
				Method:      "POST",
				QueryString: "promo=AUTUMN",
				ClientIP:    "192.168.1.100",
				Headers:     map[string]string{"user-agent": "Mozilla/5.0"},
				Body:        map[string]any{"amount": 500, "password": "[FILTERED]"},
			},
			Execution: &domain.ExecutionContext{
				Type:  "queue",
				IsCLI: true,
				QueueJob: &domain.QueueJobContext{
					JobName:    "App\\Jobs\\ProcessPaymentJob",
					Queue:      "payments-high",
					Connection: "redis",
					Attempts:   3,
					MaxTries:   &maxTries,
					Timeout:    120,
				},
			},
			System: &domain.SystemContext{
				PHPVersion:       "8.3.4",
				LaravelVersion:   "11.28.0",
				MemoryUsageBytes: 14680064,
				MemoryPeakBytes:  27262976,
				DurationMS:       312.45,
				Hostname:         "worker-pod-1",
				OS               : "Linux 6.1.0",
				SAPI             : "cli",
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
					Message:   "POST https://api.stripe.com/v1/charges [504 Gateway Timeout]",
					Timestamp: 1728676515.34,
				},
			},
		},
	}

	res, err := incRepo.UpsertIncident(ctx, evt, "hash-ext-ctx-1")
	if err != nil {
		t.Fatalf("UpsertIncident failed: %v", err)
	}

	inc, err := incRepo.GetIncidentByID(ctx, res.IncidentID)
	if err != nil {
		t.Fatalf("GetIncidentByID failed: %v", err)
	}

	// Verify denormalized fields
	if inc.RouteName != "orders.checkout" {
		t.Errorf("expected RouteName 'orders.checkout', got %q", inc.RouteName)
	}
	if inc.RouteAction != "App\\Http\\Controllers\\OrderController@checkout" {
		t.Errorf("expected RouteAction 'App\\Http\\Controllers\\OrderController@checkout', got %q", inc.RouteAction)
	}
	if inc.ExecutionType != "queue" {
		t.Errorf("expected ExecutionType 'queue', got %q", inc.ExecutionType)
	}
	if inc.PHPVersion != "8.3.4" {
		t.Errorf("expected PHPVersion '8.3.4' (synced from system), got %q", inc.PHPVersion)
	}
	if inc.LaravelVersion != "11.28.0" {
		t.Errorf("expected LaravelVersion '11.28.0' (synced from system), got %q", inc.LaravelVersion)
	}

	// Verify structured contexts
	if inc.Route == nil || inc.Route.Name != "orders.checkout" {
		t.Fatalf("expected parsed Route, got %+v", inc.Route)
	}
	if len(inc.Route.Middleware) != 2 || inc.Route.Middleware[1] != "auth:sanctum" {
		t.Errorf("unexpected Route middleware: %+v", inc.Route.Middleware)
	}

	if inc.HTTPRequest == nil || inc.HTTPRequest.Method != "POST" {
		t.Fatalf("expected parsed HTTPRequest, got %+v", inc.HTTPRequest)
	}
	if inc.HTTPRequest.ClientIP != "192.168.1.100" {
		t.Errorf("expected ClientIP '192.168.1.100', got %q", inc.HTTPRequest.ClientIP)
	}

	if inc.QueueJob == nil || inc.QueueJob.JobName != "App\\Jobs\\ProcessPaymentJob" {
		t.Fatalf("expected parsed QueueJob, got %+v", inc.QueueJob)
	}
	if inc.QueueJob.Attempts != 3 || inc.QueueJob.MaxTries == nil || *inc.QueueJob.MaxTries != 5 {
		t.Errorf("unexpected QueueJob parameters: %+v", inc.QueueJob)
	}

	if inc.System == nil || inc.System.Hostname != "worker-pod-1" {
		t.Fatalf("expected parsed SystemContext, got %+v", inc.System)
	}
	if inc.System.MemoryPeakBytes != 27262976 || inc.System.DurationMS != 312.45 {
		t.Errorf("unexpected System metrics: %+v", inc.System)
	}

	if len(inc.Breadcrumbs) != 2 {
		t.Fatalf("expected 2 breadcrumbs, got %d", len(inc.Breadcrumbs))
	}
	if inc.Breadcrumbs[0].Category != "sql" || inc.Breadcrumbs[1].Category != "http" {
		t.Errorf("unexpected breadcrumbs: %+v", inc.Breadcrumbs)
	}

	// 2. Test search by route_name
	filtered, err := incRepo.GetIncidents(ctx, domain.IncidentFilter{
		Query:  "orders.checkout",
		Status: "all",
	})
	if err != nil {
		t.Fatalf("GetIncidents by route_name failed: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ID != inc.ID {
		t.Errorf("expected 1 result searching for 'orders.checkout', got %d", len(filtered))
	}
}



func TestIncident_AppContextPersistence(t *testing.T) {
	incRepo, _, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	const hash = "hash-app-ctx"

	newEvent := func(release string, appCtx *domain.AppContext) domain.IncomingEvent {
		return domain.IncomingEvent{
			ProjectID:   1,
			ProjectName: "Default Project",
			ProjectKey:  "test-api-key",
			Payload: domain.EventPayload{
				ExceptionClass: "InvalidOrderException",
				Message:        "Order is invalid",
				File:           "app/Services/OrderService.php",
				Line:           10,
				Release:        release,
				Context:        appCtx,
			},
		}
	}
	upsert := func(evt domain.IncomingEvent) *domain.Incident {
		t.Helper()
		res, err := incRepo.UpsertIncident(ctx, evt, hash)
		if err != nil {
			t.Fatalf("UpsertIncident failed: %v", err)
		}
		inc, err := incRepo.GetIncidentByID(ctx, res.IncidentID)
		if err != nil {
			t.Fatalf("GetIncidentByID failed: %v", err)
		}
		return inc
	}

	inc := upsert(newEvent("1.0.0", &domain.AppContext{
		Shared:    map[string]any{"trace_id": "t-1"},
		Exception: map[string]any{"order_id": float64(42)},
	}))
	if inc.AppContext == nil || inc.AppContext.Shared["trace_id"] != "t-1" || inc.AppContext.Exception["order_id"] != float64(42) {
		t.Fatalf("new incident: unexpected app context %+v", inc.AppContext)
	}

	inc = upsert(newEvent("1.0.0", &domain.AppContext{Shared: map[string]any{"trace_id": "t-2"}}))
	if inc.AppContext == nil || inc.AppContext.Shared["trace_id"] != "t-2" || inc.AppContext.Exception != nil {
		t.Fatalf("repeat event must replace context, got %+v", inc.AppContext)
	}

	inc = upsert(newEvent("1.0.0", &domain.AppContext{}))
	if inc.AppContext == nil || inc.AppContext.Shared["trace_id"] != "t-2" {
		t.Fatalf("event without context must keep previous one, got %+v", inc.AppContext)
	}

	if err := incRepo.ResolveIncident(ctx, inc.ID, "version", "1.0.0"); err != nil {
		t.Fatalf("ResolveIncident failed: %v", err)
	}
	inc = upsert(newEvent("1.1.0", &domain.AppContext{Shared: map[string]any{"trace_id": "t-3"}}))
	if !inc.IsRegression || inc.AppContext == nil || inc.AppContext.Shared["trace_id"] != "t-3" {
		t.Fatalf("regression must update context, got regression=%v ctx=%+v", inc.IsRegression, inc.AppContext)
	}
}
