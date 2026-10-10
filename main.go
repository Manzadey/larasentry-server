package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

func main() {
	// Load .env file if exists
	_ = godotenv.Load()

	// Configuration
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
	
	intervalStr := os.Getenv("ALERT_INTERVAL")
	alertInterval := 5 * time.Minute
	if intervalStr != "" {
		parsed, err := time.ParseDuration(intervalStr)
		if err == nil {
			alertInterval = parsed
		}
	}

	// 1. Initialize SQLite Database
	db, err := InitDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize DB: %v", err)
	}
	defer db.Close()

	// 2. Setup Channel and Background Processor
	// Buffered channel for 1000 events to prevent HTTP blocking
	eventChan := make(chan IncomingEvent, 1000)
	
	processor := NewProcessor(db, eventChan)
	go processor.Start()

	// 2.5 Setup Database Cleaner
	go StartCleaner(db, 30)

	// 3. Setup Telegram Alert Manager
	alertManager := NewAlertManager(processor, tgToken, tgChatID, alertInterval)
	go alertManager.Start()

	// 4. Setup HTTP Server
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
	apiHandler := NewAPIHandler(db, eventChan, allowedIPs)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/events", apiHandler.HandleEvent)
	mux.HandleFunc("/api/incidents", apiHandler.HandleListIncidents)
	
	log.Printf("Larasentry server is running on :%s...", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
