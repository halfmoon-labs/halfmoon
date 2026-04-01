package observability

import (
	"fmt"

	"github.com/halfmoon-labs/halfmoon/pkg/observability/otlp"
)

// NewBackend creates a Backend based on the configured backend type.
// serviceVersion is optional and used for OTEL resource attributes.
func NewBackend(cfg Config, serviceVersion string) (Backend, error) {
	ApplyDefaults(&cfg)

	switch cfg.Backend {
	case "otlp":
		return otlp.NewBackend(otlp.Config{
			Endpoint:         cfg.OTLP.Endpoint,
			Protocol:         cfg.OTLP.Protocol,
			Headers:          cfg.OTLP.Headers,
			Insecure:         cfg.OTLP.Insecure,
			TimeoutMs:        cfg.OTLP.TimeoutMs,
			ExportIntervalMs: cfg.OTLP.ExportIntervalMs,
			BatchSize:        cfg.OTLP.BatchSize,
			ServiceName:      cfg.ServiceName,
			ServiceVersion:   serviceVersion,
		})
	default:
		return nil, fmt.Errorf("unknown observability backend: %q", cfg.Backend)
	}
}
