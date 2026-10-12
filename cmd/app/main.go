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
// @version 2.0
// @description REST API LaraSentry: приём ошибок от Laravel, жизненный цикл инцидентов, релизы из CI/CD, аналитика и поток событий.
// @description Ключ не нужен: проект определяется по имени, а доступ ограничен ALLOWED_IPS (пусто — только хост сервиса).
// @host localhost:8080
// @BasePath /

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

	db, err := sqlite.InitDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize DB: %v", err)
	}
	defer db.Close()

	projectRepo := sqlite.NewProjectRepo(db)
	releaseRepo := sqlite.NewReleaseRepo(db)
	incidentRepo := sqlite.NewIncidentRepo(db, releaseRepo)

	eventChan := make(chan domain.IncomingEvent, 1000)
	processor := usecase.NewProcessor(incidentRepo, eventChan)
	go processor.Start()

	go sqlite.StartCleaner(incidentRepo, 30)

	alertSender := telegram.NewBotSender(tgToken, tgChatID, tgProxy)
	alertManager := usecase.NewAlertManager(processor, alertSender, alertInterval)
	go alertManager.Start()

	sseBroker := sse.NewBroker()
	uiHandler, err := deliveryHttp.NewUIHandler(projectRepo, incidentRepo, releaseRepo, sseBroker)
	if err != nil {
		log.Fatalf("Failed to initialize UI handler: %v", err)
	}

	githubClient := github.NewClient(nil, "", 15*time.Minute)
	uiHandler.SetGitHubClient(githubClient)
	githubAccountRepo := sqlite.NewGitHubAccountRepo(db)
	uiHandler.SetGitHubAccounts(githubAccountRepo, usecase.NewGitHubAccounts(githubAccountRepo, githubClient))

	processor.SetOnIncident(func(inc domain.Incident) {
		sseBroker.BroadcastIncident(inc)
	})

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
	apiHandler := deliveryHttp.NewAPIHandler(projectRepo, incidentRepo, eventChan)
	apiHandler.SetReleaseRepo(releaseRepo)
	apiHandler.SetSSEBroker(sseBroker)

	processor.SetOnJSONEvent(func(eventType string, data any) {
		sseBroker.BroadcastJSON(eventType, data)
	})

	mux := http.NewServeMux()
	// UI и Swagger закрываются basic auth на nginx, список IP — только для API
	api := http.NewServeMux()
	mux.Handle("/api/", deliveryHttp.NewIPAllowlist(allowedIPs).Middleware(api))

	api.HandleFunc("POST /api/events", apiHandler.HandleEvent)
	api.HandleFunc("POST /api/events/batch", apiHandler.HandleBatchEvents)

	api.HandleFunc("GET /api/incidents", apiHandler.HandleListIncidents)
	api.HandleFunc("POST /api/incidents/batch", apiHandler.HandleBatchIncidents)
	api.HandleFunc("GET /api/incidents/{id}", apiHandler.HandleGetIncident)
	api.HandleFunc("DELETE /api/incidents/{id}", apiHandler.HandleDeleteIncident)
	api.HandleFunc("POST /api/incidents/{id}/resolve", apiHandler.HandleResolveIncident)
	api.HandleFunc("POST /api/incidents/{id}/reopen", apiHandler.HandleReopenIncident)
	api.HandleFunc("POST /api/incidents/{id}/ignore", apiHandler.HandleIgnoreIncident)

	api.HandleFunc("PUT /api/projects/{id}/github", apiHandler.HandleUpdateProjectGitHub)
	api.HandleFunc("POST /api/projects/{id}/github", apiHandler.HandleUpdateProjectGitHub)

	api.HandleFunc("GET /api/releases", apiHandler.HandleListReleases)
	api.HandleFunc("POST /api/releases", apiHandler.HandleCreateRelease)
	api.HandleFunc("GET /api/releases/{version}/incidents", apiHandler.HandleReleaseIncidents)

	api.HandleFunc("GET /api/stats/summary", apiHandler.HandleStatsSummary)
	api.HandleFunc("GET /api/events/stream", apiHandler.HandleEventsStreamJSON)
	api.HandleFunc("GET /api/health", apiHandler.HandleHealthCheck)

	mux.HandleFunc("/swagger/", httpSwagger.WrapHandler)


	mux.HandleFunc("/ui", uiHandler.HandleIndex)
	mux.HandleFunc("/ui/incidents", uiHandler.HandleIncidents)
	mux.HandleFunc("/ui/incidents/", uiHandler.HandleIncidentDetail)
	mux.HandleFunc("POST /ui/projects/github-settings", uiHandler.HandleUpdateProjectGitHubSettings)
	mux.HandleFunc("GET /ui/github", uiHandler.HandleGitHubPage)
	mux.HandleFunc("POST /ui/github/accounts", uiHandler.HandleCreateGitHubAccount)
	mux.HandleFunc("POST /ui/github/accounts/{id}/sync", uiHandler.HandleSyncGitHubAccount)
	mux.HandleFunc("POST /ui/github/accounts/{id}/delete", uiHandler.HandleDeleteGitHubAccount)
	mux.HandleFunc("POST /ui/github/accounts/{id}/repos", uiHandler.HandleSaveGitHubRepos)
	mux.HandleFunc("/ui/events/stream", uiHandler.HandleEventsStream)

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
