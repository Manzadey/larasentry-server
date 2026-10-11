package sse

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"server/internal/domain"
	"sync"
)

type Broker struct {
	mu      sync.RWMutex
	clients map[chan []byte]bool
}

func NewBroker() *Broker {
	return &Broker{
		clients: make(map[chan []byte]bool),
	}
}

func (b *Broker) Register(ch chan []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.clients[ch] = true
	log.Printf("[SSE] Client connected. Total clients: %d", len(b.clients))
}

func (b *Broker) Unregister(ch chan []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.clients, ch)
	close(ch)
	log.Printf("[SSE] Client disconnected. Total clients: %d", len(b.clients))
}

func (b *Broker) Broadcast(msg []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch := range b.clients {
		select {
		case ch <- msg:
		default:
			log.Println("[SSE] Dropped message for slow client")
		}
	}
}

// BroadcastIncident formats an incident as an SSE event containing HTML
func (b *Broker) BroadcastIncident(inc domain.Incident, rowTmpl *template.Template) {
	var buf bytes.Buffer
	if err := rowTmpl.Execute(&buf, inc); err != nil {
		log.Printf("[SSE] Error rendering template for broadcast: %v", err)
		return
	}

	htmlContent := buf.String()
	// Format as SSE event matching sse-swap="new-incident" in HTMX
	eventPayload := fmt.Sprintf("event: new-incident\ndata: %s\n\n", htmlContent)
	b.Broadcast([]byte(eventPayload))
}
