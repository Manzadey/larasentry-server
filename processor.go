package main

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"sync"
	"time"
)

type Processor struct {
	db          *sql.DB
	eventChan   chan IncomingEvent
	alertBuffer map[string]*Incident // In-memory buffer for alerts
	mu          sync.Mutex           // Protects alertBuffer
}

type IncomingEvent struct {
	ProjectID  int64
	ProjectKey string
	Payload    EventPayload
}

func NewProcessor(db *sql.DB, eventChan chan IncomingEvent) *Processor {
	return &Processor{
		db:          db,
		eventChan:   eventChan,
		alertBuffer: make(map[string]*Incident),
	}
}

func (p *Processor) Start() {
	log.Println("Starting Background Processor...")
	for event := range p.eventChan {
		p.processEvent(event)
	}
}

func (p *Processor) processEvent(event IncomingEvent) {
	// 1. Calculate hash: md5(ProjectKey + ExceptionClass + File + Line)
	hashInput := fmt.Sprintf("%s:%s:%s:%d", event.ProjectKey, event.Payload.ExceptionClass, event.Payload.File, event.Payload.Line)
	hashBytes := md5.Sum([]byte(hashInput))
	hash := hex.EncodeToString(hashBytes[:])

	now := time.Now()

	// 2. UPSERT in SQLite
	query := `
	INSERT INTO incidents (
		project_id, hash, exception_class, message, file, line, 
		stack_trace, app_url, request_url, events_count, first_seen_at, last_seen_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
	ON CONFLICT(hash) DO UPDATE SET 
		events_count = events_count + 1,
		last_seen_at = excluded.last_seen_at,
		message = excluded.message
	RETURNING id, events_count;
	`
	
	var incidentID int64
	var totalCount int
	err := p.db.QueryRow(query, 
		event.ProjectID, hash, event.Payload.ExceptionClass, event.Payload.Message, 
		event.Payload.File, event.Payload.Line, event.Payload.StackTrace, 
		event.Payload.AppURL, event.Payload.RequestURL, now, now,
	).Scan(&incidentID, &totalCount)

	if err != nil {
		log.Printf("Error upserting incident: %v", err)
		return
	}

	// 3. Update alert buffer for Telegram
	p.mu.Lock()
	defer p.mu.Unlock()

	if incident, exists := p.alertBuffer[hash]; exists {
		incident.EventsCount++
	} else {
		p.alertBuffer[hash] = &Incident{
			ID:             incidentID,
			ProjectID:      event.ProjectID,
			Hash:           hash,
			ExceptionClass: event.Payload.ExceptionClass,
			Message:        event.Payload.Message,
			File:           event.Payload.File,
			Line:           event.Payload.Line,
			AppURL:         event.Payload.AppURL,
			EventsCount:    1, // count in the current time window
		}
	}
}

func (p *Processor) GetAndClearBuffer() map[string]*Incident {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	if len(p.alertBuffer) == 0 {
		return nil
	}

	// Copy current buffer
	current := p.alertBuffer
	// Reset buffer
	p.alertBuffer = make(map[string]*Incident)
	return current
}
