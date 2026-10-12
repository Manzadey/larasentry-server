package sse

import (
	"encoding/json"
	"fmt"
	"log"
	"server/internal/domain"
	"sync"
)

type Broker struct {
	mu          sync.RWMutex
	htmlClients map[chan []byte]bool
	jsonClients map[chan []byte]bool
}

func NewBroker() *Broker {
	return &Broker{
		htmlClients: make(map[chan []byte]bool),
		jsonClients: make(map[chan []byte]bool),
	}
}

func (b *Broker) Register(ch chan []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.htmlClients[ch] = true
	log.Printf("[SSE] HTML Client connected. Total: %d", len(b.htmlClients))
}

func (b *Broker) Unregister(ch chan []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.htmlClients, ch)
	close(ch)
	log.Printf("[SSE] HTML Client disconnected. Total: %d", len(b.htmlClients))
}

func (b *Broker) RegisterJSON(ch chan []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.jsonClients[ch] = true
	log.Printf("[SSE] JSON Client connected. Total: %d", len(b.jsonClients))
}

func (b *Broker) UnregisterJSON(ch chan []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.jsonClients, ch)
	close(ch)
	log.Printf("[SSE] JSON Client disconnected. Total: %d", len(b.jsonClients))
}

func (b *Broker) Broadcast(msg []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch := range b.htmlClients {
		select {
		case ch <- msg:
		default:
			log.Println("[SSE] Dropped HTML message for slow client")
		}
	}
}

// BroadcastIncident notifies UI clients that an incident changed.
// Отправляется только id: UI сам перезапрашивает список с текущими фильтрами,
// а многострочный HTML в одной строке data: браузер обрезает до первой строки.
func (b *Broker) BroadcastIncident(inc domain.Incident) {
	b.Broadcast([]byte(fmt.Sprintf("event: new-incident\ndata: %d\n\n", inc.ID)))
}

// BroadcastJSON sends a typed JSON event to connected API clients
func (b *Broker) BroadcastJSON(eventType string, data any) {
	bytesData, err := json.Marshal(data)
	if err != nil {
		log.Printf("[SSE] Error marshaling JSON for broadcast: %v", err)
		return
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	if len(b.jsonClients) == 0 {
		return
	}

	eventPayload := []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, string(bytesData)))
	for ch := range b.jsonClients {
		select {
		case ch <- eventPayload:
		default:
			log.Println("[SSE] Dropped JSON message for slow client")
		}
	}
}

