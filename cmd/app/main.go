package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	httpSwagger "github.com/swaggo/http-swagger"

	_ "server/docs"
	"server/internal/domain"
	deliveryHttp "server/internal/delivery/http"
	"server/internal/delivery/sse"
	"server/internal/delivery/telegram"
	"server/internal/pkg/github"
	"server/internal/repository/sqlite"
	"server/internal/usecase"
)

// @title LaraSentry API
// @version 1.0
// @description API documentation for LaraSentry Go Server.
// @host localhost:8080
// @BasePath /

// @securityDefinitions.apikey Bearer
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and API key.

func main() {
	_ = godotenv.Load()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "tracker.db"
	}
	tgToken := os.Getenv("TG_BOT_TOKEN")
	tgChatID := os.Getenv("TG_CHAT_ID")
	tgProxy := os.Getenv("TG_PROXY")
	
	intervalStr := os.Getenv("ALERT_INTERVAL")
	alertInterval := 5 * time.Minute
	if intervalStr != "" {
		parsed, err := time.ParseDuration(intervalStr)
		if err == nil {
			alertInterval = parsed
		}
	}

	// 1. Initialize SQLite Database
	db, err := sqlite.InitDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize DB: %v", err)
	}
	defer db.Close()

	// 2. Setup Repositories
	projectRepo := sqlite.NewProjectRepo(db)
	releaseRepo := sqlite.NewReleaseRepo(db)
	incidentRepo := sqlite.NewIncidentRepo(db, releaseRepo)

	// 3. Setup Channels and UseCases
	eventChan := make(chan domain.IncomingEvent, 1000)
	processor := usecase.NewProcessor(incidentRepo, eventChan)
	go processor.Start()

	// 4. Setup Database Cleaner
	go sqlite.StartCleaner(incidentRepo, 30)

	// 5. Setup Telegram Alert Manager
	alertSender := telegram.NewBotSender(tgToken, tgChatID, tgProxy)
	alertManager := usecase.NewAlertManager(processor, alertSender, alertInterval)
	go alertManager.Start()

	// 6. Setup SSE Broker & UI Handler
	sseBroker := sse.NewBroker()
	uiHandler, err := deliveryHttp.NewUIHandler(projectRepo, incidentRepo, releaseRepo, sseBroker)
	if err != nil {
		log.Fatalf("Failed to initialize UI handler: %v", err)
	}

	// Setup GitHub API client
	githubClient := github.NewClient(nil, "", 15*time.Minute)
	uiHandler.SetGitHubClient(githubClient)
	githubAccountRepo := sqlite.NewGitHubAccountRepo(db)
	uiHandler.SetGitHubAccounts(githubAccountRepo, usecase.NewGitHubAccounts(githubAccountRepo, githubClient))

	processor.SetOnIncident(func(inc domain.Incident) {
		sseBroker.BroadcastIncident(inc, uiHandler.GetRowTemplate())
	})

	// 7. Setup HTTP Server
	allowedIPsStr := os.Getenv("ALLOWED_IPS")
	var allowedIPs []string
	if allowedIPsStr != "" {
		for _, ip := range strings.Split(allowedIPsStr, ",") {
			ip = strings.TrimSpace(ip)
			if ip != "" {
				allowedIPs = append(allowedIPs, ip)
			}
		}
	}
	apiHandler := deliveryHttp.NewAPIHandler(projectRepo, incidentRepo, eventChan, allowedIPs)
	apiHandler.SetReleaseRepo(releaseRepo)
	apiHandler.SetSSEBroker(sseBroker)

	processor.SetOnJSONEvent(func(eventType string, data any) {
		sseBroker.BroadcastJSON(eventType, data)
	})

	mux := http.NewServeMux()

	// 1. Ingestion routes
	mux.HandleFunc("POST /api/events", apiHandler.HandleEvent)
	mux.HandleFunc("POST /api/events/batch", apiHandler.HandleBatchEvents)

	// 2. Incidents routes
	mux.HandleFunc("GET /api/incidents", apiHandler.HandleListIncidents)
	mux.HandleFunc("POST /api/incidents/batch", apiHandler.HandleBatchIncidents)
	mux.HandleFunc("GET /api/incidents/{id}", apiHandler.HandleGetIncident)
	mux.HandleFunc("DELETE /api/incidents/{id}", apiHandler.HandleDeleteIncident)
	mux.HandleFunc("POST /api/incidents/{id}/resolve", apiHandler.HandleResolveIncident)
	mux.HandleFunc("POST /api/incidents/{id}/reopen", apiHandler.HandleReopenIncident)
	mux.HandleFunc("POST /api/incidents/{id}/ignore", apiHandler.HandleIgnoreIncident)

	// 3. Projects routes
	mux.HandleFunc("PUT /api/projects/{id}/github", apiHandler.HandleUpdateProjectGitHub)
	mux.HandleFunc("POST /api/projects/{id}/github", apiHandler.HandleUpdateProjectGitHub)

	// 4. Releases routes
	mux.HandleFunc("GET /api/releases", apiHandler.HandleListReleases)
	mux.HandleFunc("POST /api/releases", apiHandler.HandleCreateRelease)
	mux.HandleFunc("GET /api/releases/{version}/incidents", apiHandler.HandleReleaseIncidents)

	// 5. Analytics & System
	mux.HandleFunc("GET /api/stats/summary", apiHandler.HandleStatsSummary)
	mux.HandleFunc("GET /api/events/stream", apiHandler.HandleEventsStreamJSON)
	mux.HandleFunc("GET /api/health", apiHandler.HandleHealthCheck)

	// Swagger documentation
	mux.HandleFunc("/swagger/", httpSwagger.WrapHandler)


	// UI routes
	mux.HandleFunc("/ui", uiHandler.HandleIndex)
	mux.HandleFunc("/ui/incidents", uiHandler.HandleIncidents)
	mux.HandleFunc("/ui/incidents/", uiHandler.HandleIncidentDetail)
	mux.HandleFunc("POST /ui/projects/github-settings", uiHandler.HandleUpdateProjectGitHubSettings)
	mux.HandleFunc("/ui/events/stream", uiHandler.HandleEventsStream)

	// Root redirect
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/ui", http.StatusSeeOther)
			return
		}
		http.NotFound(w, r)
	})
	
	log.Printf("Larasentry server is running on :%s (UI at http://localhost:%s/ui)...", port, port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
