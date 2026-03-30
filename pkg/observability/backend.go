package observability

import (
	"context"

	"github.com/halfmoon-labs/halfmoon/pkg/agent"
)

// Backend is the pluggable interface for observability export.
// Each implementation maps agent events to its own wire format
// (OTEL spans, Prometheus metrics, HTTP webhooks, etc.)
type Backend interface {
	// Name returns a human-readable identifier for this backend (e.g., "otlp").
	Name() string

	// HandleEvent processes a single agent-loop event.
	// Called from the EventObserver goroutine — must not block.
	HandleEvent(ctx context.Context, evt agent.Event) error

	// Close flushes pending data and releases resources.
	// Must be idempotent.
	Close(ctx context.Context) error
}
