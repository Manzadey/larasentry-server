package sse

import (
	"server/internal/domain"
	"testing"
)

func TestBroadcastIncident_SendsSingleLineEvent(t *testing.T) {
	b := NewBroker()
	ch := make(chan []byte, 1)
	b.Register(ch)

	b.BroadcastIncident(domain.Incident{ID: 42})

	if got, want := string(<-ch), "event: new-incident\ndata: 42\n\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
