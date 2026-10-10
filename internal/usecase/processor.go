package usecase

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log"
	"server/internal/domain"
	"sync"
)

type Processor struct {
	repo        domain.IncidentRepository
	eventChan   chan domain.IncomingEvent
	alertBuffer map[string]*domain.Incident
	mu          sync.Mutex
}

func NewProcessor(repo domain.IncidentRepository, eventChan chan domain.IncomingEvent) *Processor {
	return &Processor{
		repo:        repo,
		eventChan:   eventChan,
		alertBuffer: make(map[string]*domain.Incident),
	}
}

func (p *Processor) Start() {
	log.Println("Starting Background Processor...")
	for event := range p.eventChan {
		p.processEvent(context.Background(), event)
	}
}

func (p *Processor) processEvent(ctx context.Context, event domain.IncomingEvent) {
	// 1. Calculate hash
	hashInput := fmt.Sprintf("%s:%s:%s:%d", event.ProjectKey, event.Payload.ExceptionClass, event.Payload.File, event.Payload.Line)
	hashBytes := md5.Sum([]byte(hashInput))
	hash := hex.EncodeToString(hashBytes[:])

	// 2. UPSERT in Database
	incidentID, _, err := p.repo.UpsertIncident(ctx, event, hash)
	if err != nil {
		log.Printf("Error upserting incident: %v", err)
		return
	}

	// 3. Update alert buffer
	p.mu.Lock()
	defer p.mu.Unlock()

	if incident, exists := p.alertBuffer[hash]; exists {
		incident.EventsCount++
	} else {
		p.alertBuffer[hash] = &domain.Incident{
			ID:             incidentID,
			ProjectID:      event.ProjectID,
			Hash:           hash,
			ExceptionClass: event.Payload.ExceptionClass,
			Message:        event.Payload.Message,
			File:           event.Payload.File,
			Line:           event.Payload.Line,
			AppURL:         event.Payload.AppURL,
			EventsCount:    1,
		}
	}
}

func (p *Processor) GetAndClearBuffer() map[string]*domain.Incident {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	if len(p.alertBuffer) == 0 {
		return nil
	}

	current := p.alertBuffer
	p.alertBuffer = make(map[string]*domain.Incident)
	return current
}
