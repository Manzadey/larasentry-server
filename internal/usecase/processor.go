package usecase

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log"
	"server/internal/domain"
	"strings"
	"sync"
)

// CalculateHash computes MD5 hash from event payload and project key
func CalculateHash(projectKey string, payload domain.EventPayload) string {
	var hashInput string
	if len(payload.Fingerprint) > 0 {
		hashInput = fmt.Sprintf("%s:%s", projectKey, strings.Join(payload.Fingerprint, ":"))
	} else {
		hashInput = fmt.Sprintf("%s:%s:%s:%d", projectKey, payload.ExceptionClass, payload.File, payload.Line)
	}
	hashBytes := md5.Sum([]byte(hashInput))
	return hex.EncodeToString(hashBytes[:])
}

type Processor struct {
	repo        domain.IncidentRepository
	eventChan   chan domain.IncomingEvent
	alertBuffer map[string]*domain.Incident
	mu          sync.Mutex
	onIncident  func(inc domain.Incident)
	onJSONEvent func(eventType string, data any)
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

func (p *Processor) SetOnJSONEvent(fn func(eventType string, data any)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onJSONEvent = fn
}

func (p *Processor) Start() {
	log.Println("Starting Background Processor...")
	for event := range p.eventChan {
		p.processEvent(context.Background(), event)
	}
}

func (p *Processor) processEvent(ctx context.Context, event domain.IncomingEvent) {
	// 1. Calculate hash
	hash := CalculateHash(event.ProjectKey, event.Payload)

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
	jsonCallback := p.onJSONEvent
	p.mu.Unlock()

	if callback != nil && !res.Ignored {
		go callback(res.Incident)
	}

	if jsonCallback != nil && !res.Ignored {
		eventType := "incident_updated"
		if res.IsNew {
			eventType = "incident_new"
		} else if res.IsRegression {
			eventType = "regression"
		}
		go jsonCallback(eventType, res.Incident)
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

