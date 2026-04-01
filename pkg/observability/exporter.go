package observability

import (
	"context"

	"github.com/halfmoon-labs/halfmoon/pkg/agent"
	"github.com/halfmoon-labs/halfmoon/pkg/logger"
)

// Exporter implements agent.EventObserver and delegates events to a Backend.
// It handles event filtering and error logging so backends can focus on export.
type Exporter struct {
	backend  Backend
	excluded map[agent.EventKind]bool
}

// NewExporter creates an Exporter that delegates to the given backend.
// Events listed in cfg.ExcludedEvents are silently dropped.
func NewExporter(backend Backend, cfg Config) *Exporter {
	// Build a reverse lookup from event name to kind.
	nameToKind := make(map[string]agent.EventKind, agent.EventKindCount())
	for kind := agent.EventKind(0); kind < agent.EventKindCount(); kind++ {
		nameToKind[kind.String()] = kind
	}

	excluded := make(map[agent.EventKind]bool, len(cfg.ExcludedEvents))
	for _, name := range cfg.ExcludedEvents {
		if kind, ok := nameToKind[name]; ok {
			excluded[kind] = true
		}
	}

	return &Exporter{
		backend:  backend,
		excluded: excluded,
	}
}

// OnEvent implements agent.EventObserver.
func (e *Exporter) OnEvent(ctx context.Context, evt agent.Event) error {
	if e.excluded[evt.Kind] {
		return nil
	}
	if err := e.backend.HandleEvent(ctx, evt); err != nil {
		logger.WarnCF("observability", "Backend event handling failed", map[string]any{
			"backend": e.backend.Name(),
			"event":   evt.Kind.String(),
			"error":   err.Error(),
		})
	}
	return nil
}

// Close shuts down the backend, flushing any pending data.
func (e *Exporter) Close(ctx context.Context) error {
	return e.backend.Close(ctx)
}
