package sqlite

import (
	"context"
	"log"
	"server/internal/domain"
	"time"
)

// StartCleaner запукает фоновую горутину, которая раз в сутки удаляет старые ошибки
func StartCleaner(repo domain.IncidentRepository, daysToKeep int) {
	ticker := time.NewTicker(24 * time.Hour)
	log.Printf("Starting DB Cleaner (retention: %d days)...", daysToKeep)
	
	cleanOldIncidents(repo, daysToKeep)

	for range ticker.C {
		cleanOldIncidents(repo, daysToKeep)
	}
}

func cleanOldIncidents(repo domain.IncidentRepository, daysToKeep int) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	deleted, err := repo.CleanOldIncidents(ctx, daysToKeep)
	if err != nil {
		log.Printf("Failed to clean old incidents: %v", err)
		return
	}

	if deleted > 0 {
		log.Printf("Cleaned %d old incidents (older than %d days)", deleted, daysToKeep)
	}
}
