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
	onIncident  func(inc domain.Incident)
}

func NewProcessor(repo domain.IncidentRepository, eventChan chan domain.IncomingEvent) *Processor {
	return &Processor{
		repo:        repo,
		eventChan:   eventChan,
		alertBuffer: make(map[string]*domain.Incident),
	}
}

func (p *Processor) SetOnIncident(fn func(inc domain.Incident)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onIncident = fn
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
	res, err := p.repo.UpsertIncident(ctx, event, hash)
	if err != nil {
		log.Printf("Error upserting incident: %v", err)
		return
	}

	// 3. Update alert buffer only if not ignored (resolved old versions or ignored issues are silenced)
	if !res.Ignored {
		p.mu.Lock()
		if existing, exists := p.alertBuffer[hash]; exists {
			existing.EventsCount++
			if res.IsRegression {
				existing.IsRegression = true
			}
			existing.LastRelease = res.Incident.LastRelease
		} else {
			incCopy := res.Incident
			p.alertBuffer[hash] = &incCopy
		}
		p.mu.Unlock()
	}

	// 4. SSE Realtime Broadcast
	p.mu.Lock()
	callback := p.onIncident
	p.mu.Unlock()

	if callback != nil && !res.Ignored {
		go callback(res.Incident)
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
