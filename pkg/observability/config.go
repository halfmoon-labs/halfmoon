package observability

import "github.com/halfmoon-labs/halfmoon/pkg/config"

// Config is an alias for the observability configuration defined in the config package.
type Config = config.ObservabilityConfig

// ApplyDefaults fills in zero-value fields with sensible defaults.
func ApplyDefaults(cfg *Config) {
	if cfg.Backend == "" {
		cfg.Backend = "otlp"
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "halfmoon"
	}
	if cfg.OTLP.Protocol == "" {
		cfg.OTLP.Protocol = "grpc"
	}
	if cfg.OTLP.TimeoutMs <= 0 {
		cfg.OTLP.TimeoutMs = 10000
	}
	if cfg.OTLP.ExportIntervalMs <= 0 {
		cfg.OTLP.ExportIntervalMs = 5000
	}
	if cfg.OTLP.BatchSize <= 0 {
		cfg.OTLP.BatchSize = 512
	}
}
