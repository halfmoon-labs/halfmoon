package observability

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/halfmoon-labs/halfmoon/pkg/agent"
)

type mockBackend struct {
	mu     sync.Mutex
	events []agent.Event
	closed bool
	err    error // if set, HandleEvent returns this error
}

func (m *mockBackend) Name() string { return "mock" }

func (m *mockBackend) HandleEvent(_ context.Context, evt agent.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, evt)
	return m.err
}

func (m *mockBackend) Close(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockBackend) eventCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

func TestExporter_DelegatesToBackend(t *testing.T) {
	mock := &mockBackend{}
	exporter := NewExporter(mock, Config{})

	evt := agent.Event{
		Kind: agent.EventKindTurnStart,
		Time: time.Now(),
		Meta: agent.EventMeta{TurnID: "turn-1"},
	}
	if err := exporter.OnEvent(context.Background(), evt); err != nil {
		t.Fatalf("OnEvent failed: %v", err)
	}
	if mock.eventCount() != 1 {
		t.Fatalf("expected 1 event, got %d", mock.eventCount())
	}
}

func TestExporter_ExcludedEvents(t *testing.T) {
	mock := &mockBackend{}
	exporter := NewExporter(mock, Config{
		ExcludedEvents: []string{"llm_delta"},
	})

	// Excluded event should be filtered
	if err := exporter.OnEvent(context.Background(), agent.Event{
		Kind: agent.EventKindLLMDelta,
		Time: time.Now(),
	}); err != nil {
		t.Fatalf("OnEvent failed: %v", err)
	}
	if mock.eventCount() != 0 {
		t.Fatalf("expected excluded event to be filtered, got %d events", mock.eventCount())
	}

	// Non-excluded event should pass through
	if err := exporter.OnEvent(context.Background(), agent.Event{
		Kind: agent.EventKindTurnStart,
		Time: time.Now(),
	}); err != nil {
		t.Fatalf("OnEvent failed: %v", err)
	}
	if mock.eventCount() != 1 {
		t.Fatalf("expected 1 event after non-excluded event, got %d", mock.eventCount())
	}
}

func TestExporter_BackendErrorDoesNotPropagate(t *testing.T) {
	mock := &mockBackend{err: fmt.Errorf("export failed")}
	exporter := NewExporter(mock, Config{})

	err := exporter.OnEvent(context.Background(), agent.Event{
		Kind: agent.EventKindTurnStart,
		Time: time.Now(),
	})
	if err != nil {
		t.Fatalf("expected backend error to be swallowed, got: %v", err)
	}
	// Event was still passed to backend
	if mock.eventCount() != 1 {
		t.Fatalf("expected event to reach backend despite error, got %d", mock.eventCount())
	}
}

func TestExporter_CloseDelegatesToBackend(t *testing.T) {
	mock := &mockBackend{}
	exporter := NewExporter(mock, Config{})

	if err := exporter.Close(context.Background()); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if !mock.closed {
		t.Fatal("expected backend.Close() to be called")
	}
}
