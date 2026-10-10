package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"
)

// StartCleaner запукает фоновую горутину, которая раз в сутки удаляет старые ошибки
func StartCleaner(db *sql.DB, daysToKeep int) {
	ticker := time.NewTicker(24 * time.Hour)
	log.Printf("Starting DB Cleaner (retention: %d days)...", daysToKeep)
	
	// Запускаем сразу при старте один раз
	cleanOldIncidents(db, daysToKeep)

	for range ticker.C {
		cleanOldIncidents(db, daysToKeep)
	}
}

func cleanOldIncidents(db *sql.DB, daysToKeep int) {
	query := `DELETE FROM incidents WHERE last_seen_at < datetime('now', ?)`
	modifier := fmt.Sprintf("-%d days", daysToKeep)

	result, err := db.Exec(query, modifier)
	if err != nil {
		log.Printf("Failed to clean old incidents: %v", err)
		return
	}

	deleted, _ := result.RowsAffected()
	if deleted > 0 {
		log.Printf("Cleaned %d old incidents (older than %d days)", deleted, daysToKeep)
	}
}
