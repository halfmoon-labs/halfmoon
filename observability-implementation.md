# Observability Implementation Plan

Export agent-loop events through a pluggable backend system. The first backend is OpenTelemetry (OTLP), but the architecture supports adding new backends (Prometheus, Datadog, webhook, etc.) without changing the core exporter or the agent loop.

---

## Goal

Allow users to configure observability in `config.json` with a backend type and endpoint. When configured, all 19 agent-loop event types are exported as traces, metrics, or structured records depending on the backend. When not configured, zero overhead — no exporter is mounted.

---

## Architecture

### Design Principles

Follow the same patterns used elsewhere in the codebase:

- **LLM providers** — `LLMProvider` interface, each provider is a separate adapter
- **Chat channels** — `BaseChannel` + per-platform implementations
- **Web search** — `SearchProvider` interface, 7 backends, one active at a time
- **Tools** — `Tool` interface, registry-based lookup

The observability system follows the same interface-based, pluggable design.

### Prior Art: ZeroClaw

ZeroClaw (Rust-based agent runtime in the same project family) has a working observability system worth learning from. It uses a trait-based `Observer` with 4 backends (Noop, Log, OTEL, Verbose) and a `MultiObserver` fan-out combiner.

**Patterns we adopt:**

| ZeroClaw Pattern | How we apply it |
|-----------------|-----------------|
| **Graceful fallback** — if OTEL init fails, silently falls back to no-op observer. Agent never crashes from observability failures. | Same. If backend init fails, log error, don't mount the exporter. Agent runs unaffected. |
| **Post-hoc span construction** — creates spans after events complete using back-calculated start times (`now - duration`). No span context threaded through the agent loop. | Same. Our `tool_exec_end` and `llm_response` carry `Duration`. Backend reconstructs spans without touching the agent loop. |
| **`Name()` on the interface** — returns a string identifier for diagnostics and log messages. | Adopt. Useful for logging which backend is active and attributing errors. |
| **Simple config surface** — only 3 fields: `backend`, `otel_endpoint`, `otel_service_name`. Advanced options handled by OTEL SDK env vars. | Adopt the simplicity for the common case while keeping advanced fields available. |

**Patterns we diverge from:**

| ZeroClaw Pattern | Our approach | Why |
|-----------------|-------------|-----|
| **Multi-backend fan-out** (`MultiObserver` broadcasting to multiple backends) | Single backend at a time | Keeps config and error handling simple. One backend is sufficient — users pick their observability stack. Same pattern as `SearchProvider` (one active at a time). |
| **Dual-track `record_event()` + `record_metric()`** | Single `HandleEvent()` | Our 19 event types are already well-typed with payload structs. The backend routes to span or metric logic internally based on `EventKind`. Two methods would duplicate routing or force the exporter to decide what's "metric-worthy." |
| **Separate `Flush()` method** | `Close()` handles flush on shutdown | Mid-run flushing is an edge case. `Close()` already flushes. Avoids adding interface surface we don't need yet. |
| **`Arc<dyn Observer>` injected into Agent struct** | Mount via `HookManager` as `EventObserver` | We already have the hook infrastructure. No need to change the `AgentInstance` struct or threading model. |
| **10 event types (coarser granularity)** | 19 event types (finer granularity) | ZeroClaw groups some events (e.g., no separate `tool_exec_start`/`tool_exec_end`). Our finer events enable richer traces without needing to thread span contexts. |
| **Separate `LogObserver` backend** | Existing `logEvent()` in the agent loop | We already log every event via `logEvent()` at `loop.go:878`. A separate log backend would duplicate this. |

### Core Interface

```go
// Backend is the pluggable interface for observability export.
// Each implementation maps agent events to its own wire format
// (OTEL spans, Prometheus metrics, HTTP webhooks, etc.)
type Backend interface {
    // Name returns a human-readable identifier for this backend (e.g., "otlp").
    // Used in log messages and diagnostics.
    Name() string

    // HandleEvent processes a single agent-loop event.
    // Called from the EventObserver goroutine — must not block.
    HandleEvent(ctx context.Context, evt agent.Event) error

    // Close flushes pending data and releases resources.
    // Called during graceful shutdown. Must be idempotent.
    Close(ctx context.Context) error
}
```

### Pipeline

```
Agent Loop
  │
  emitEvent()
  │
  ▼
EventBus.Emit()
  │
  ▼
HookManager.dispatchEvents()
  │
  ▼
Exporter (implements EventObserver)
  │
  ├─ Filters excluded events
  ├─ Calls backend.HandleEvent()
  │
  ▼
Backend (one active at a time, like SearchProvider)
  │
  ├─ OTELBackend      → TracerProvider + MeterProvider → OTLP Exporter → Collector
  ├─ [future] PrometheusBackend → /metrics endpoint
  └─ [future] WebhookBackend    → HTTP POST batched JSON
```

### Exporter — the shared core

A single struct implementing `EventObserver`. Backend-agnostic.

```go
type Exporter struct {
    backend  Backend
    excluded map[agent.EventKind]bool
}

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
```

Mounted via:

```go
hookManager.Mount(agent.NamedHook("observability", exporter))
```

### Backend Factory

```go
func NewBackend(cfg ObservabilityConfig) (Backend, error) {
    switch cfg.Backend {
    case "otlp":
        return newOTELBackend(cfg.OTLP)
    default:
        return nil, fmt.Errorf("unknown observability backend: %q", cfg.Backend)
    }
}
```

Adding a new backend means:
1. Implement `Backend` interface
2. Add a case to the factory
3. Add config fields

No changes to the exporter, the agent loop, or existing backends.

---

## Configuration

```json
{
  "observability": {
    "enabled": true,
    "backend": "otlp",
    "service_name": "halfmoon",
    "excluded_events": [],
    "otlp": {
      "endpoint": "localhost:4317",
      "protocol": "grpc",
      "headers": {
        "x-api-key": "your-key"
      },
      "insecure": false,
      "timeout_ms": 10000,
      "export_interval_ms": 5000,
      "batch_size": 512
    }
  }
}
```

### Shared fields (apply to all backends)

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `false` | Master switch. When false, nothing is initialized. |
| `backend` | `"otlp"` | Which backend to use. Currently only `"otlp"`. |
| `service_name` | `"halfmoon"` | Service identifier. Passed to backend for resource labeling. |
| `excluded_events` | `[]` | Event kinds to skip (e.g., `["llm_delta"]`). Handled by the exporter, not the backend. |

### OTLP backend fields (under `otlp`)

| Field | Default | Description |
|-------|---------|-------------|
| `endpoint` | — | Required. OTLP collector address. |
| `protocol` | `"grpc"` | `"grpc"` or `"http"`. |
| `headers` | `{}` | Custom headers for auth. |
| `insecure` | `false` | Skip TLS (for local collectors). |
| `timeout_ms` | `10000` | Export timeout per batch. |
| `export_interval_ms` | `5000` | Batch flush interval. |
| `batch_size` | `512` | Max spans/metrics per batch. |

### Environment variable overrides

Halfmoon-specific:

| Env Var | Overrides |
|---------|-----------|
| `HALFMOON_OBSERVABILITY_ENABLED` | `observability.enabled` |
| `HALFMOON_OBSERVABILITY_BACKEND` | `observability.backend` |
| `HALFMOON_OBSERVABILITY_OTLP_ENDPOINT` | `observability.otlp.endpoint` |
| `HALFMOON_OBSERVABILITY_OTLP_PROTOCOL` | `observability.otlp.protocol` |
| `HALFMOON_OBSERVABILITY_SERVICE_NAME` | `observability.service_name` |

Standard OTEL env vars as fallback (respected natively by the OTEL SDK):

| Env Var | Effect |
|---------|--------|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Fallback if `otlp.endpoint` not set |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | Fallback if `otlp.protocol` not set |
| `OTEL_EXPORTER_OTLP_HEADERS` | Merged with `otlp.headers` |
| `OTEL_SERVICE_NAME` | Fallback if `service_name` not set |

---

## Event Payload Changes

Two additive changes. No breaking changes to existing payloads.

### 1. Enrich `LLMResponsePayload`

```go
// Before
type LLMResponsePayload struct {
    ContentLen   int
    ToolCalls    int
    HasReasoning bool
}

// After
type LLMResponsePayload struct {
    Model        string
    ContentLen   int
    ToolCalls    int
    HasReasoning bool
    InputTokens  int
    OutputTokens int
    TotalTokens  int
    Duration     time.Duration
}
```

**Why:** Token counts are needed for metrics (`halfmoon.llm.tokens.*`). `Model` is needed to label metrics per model. `Duration` avoids requiring timestamp correlation between `llm_request` and `llm_response`.

**Where:** `LLMResponse.Usage` is already in scope at the emit site in `loop.go`. Copy the values.

### 2. Enrich `TurnEndPayload`

```go
// Before
type TurnEndPayload struct {
    Status          TurnEndStatus
    Iterations      int
    Duration        time.Duration
    FinalContentLen int
}

// After
type TurnEndPayload struct {
    Status          TurnEndStatus
    Iterations      int
    Duration        time.Duration
    FinalContentLen int
    Channel         string
    ChatID          string
}
```

**Why:** `channel` label on `halfmoon.turns.*` metrics. Avoids stateful correlation in backends.

**Where:** `turnState` already holds `channel` and `chatID`. One-line addition at the emit site (`loop.go:1687`).

### 3. No changes to other events

All other payloads carry sufficient data. See the complete mapping table below.

---

## OTLP Backend — OTEL Data Model

### Traces — Turn lifecycle as spans

Each turn becomes a trace. LLM calls and tool executions become child spans. Sub-agent turns become linked child traces.

```
Trace: turn-abc123
│
├─ Span: halfmoon.turn (root)
│   attributes: agent.id, session.key, channel, chat_id, user_message.length, media.count
│   status: OK | ERROR
│   duration: full turn duration
│
├─ Span: halfmoon.llm_call (child)
│   attributes: llm.model, llm.messages_count, llm.tools_count, llm.max_tokens
│   events:
│     - llm_response: content_len, tool_calls, input_tokens, output_tokens, duration_ms
│     - llm_retry (if any): attempt, reason, error, backoff_ms
│
├─ Span: halfmoon.tool_exec (child)
│   attributes: tool.name, tool.args_count
│   status: OK | ERROR
│   duration: tool execution duration
│
├─ Span: halfmoon.llm_call (child, iteration 2)
│   ...
│
└─ Span: halfmoon.subturn (linked trace)
    trace_id: turn-def456 (child's own trace)
    attributes: subturn.agent_id, subturn.label, subturn.parent_turn_id
    link: parent trace turn-abc123
```

### Span lifecycle per event kind

| Event Kind | Span Action | Target Span |
|-----------|-------------|-------------|
| `turn_start` | Start root span | `halfmoon.turn` |
| `turn_end` | End root span, set status | `halfmoon.turn` |
| `llm_request` | Start child span | `halfmoon.llm_call` |
| `llm_response` | End child span, set attributes | `halfmoon.llm_call` |
| `llm_delta` | AddEvent (if not excluded) | `halfmoon.llm_call` |
| `llm_retry` | AddEvent | `halfmoon.llm_call` |
| `tool_exec_start` | Start child span | `halfmoon.tool_exec` |
| `tool_exec_end` | End child span, set attributes | `halfmoon.tool_exec` |
| `tool_exec_skipped` | AddEvent | `halfmoon.turn` |
| `context_compress` | AddEvent | `halfmoon.turn` |
| `session_summarize` | AddEvent | `halfmoon.turn` |
| `steering_injected` | AddEvent | `halfmoon.turn` |
| `follow_up_queued` | AddEvent | `halfmoon.turn` |
| `interrupt_received` | AddEvent | `halfmoon.turn` |
| `subturn_spawn` | Start linked span | `halfmoon.subturn` |
| `subturn_end` | End linked span | `halfmoon.subturn` |
| `subturn_result_delivered` | AddEvent | `halfmoon.subturn` |
| `subturn_orphan` | AddEvent, set ERROR | `halfmoon.subturn` |
| `error` | RecordError on active span | Active span |

### Span state management

The OTLP backend maintains a `sync.Map` of active spans keyed by `TurnID`:

```go
type spanKey struct {
    TurnID     string
    SpanType   string // "turn", "llm", "tool", "subturn"
    Iteration  int
}
```

Safety: spans not ended within 10 minutes are force-ended with ERROR status to prevent memory leaks from abandoned turns.

### Metrics

Derived from the same events, exported via OTEL metrics API alongside traces.

| Metric | Type | Source Event | Labels |
|--------|------|-------------|--------|
| `halfmoon.turns.total` | Counter | `turn_end` | `agent_id`, `channel`, `status` |
| `halfmoon.turns.duration_ms` | Histogram | `turn_end` | `agent_id`, `channel`, `status` |
| `halfmoon.turns.iterations` | Histogram | `turn_end` | `agent_id`, `channel` |
| `halfmoon.llm.requests.total` | Counter | `llm_request` | `agent_id`, `model` |
| `halfmoon.llm.duration_ms` | Histogram | `llm_response` | `agent_id`, `model` |
| `halfmoon.llm.tokens.input` | Counter | `llm_response` | `agent_id`, `model` |
| `halfmoon.llm.tokens.output` | Counter | `llm_response` | `agent_id`, `model` |
| `halfmoon.llm.retries.total` | Counter | `llm_retry` | `agent_id`, `model`, `reason` |
| `halfmoon.tools.executions.total` | Counter | `tool_exec_end` | `agent_id`, `tool`, `is_error` |
| `halfmoon.tools.duration_ms` | Histogram | `tool_exec_end` | `agent_id`, `tool` |
| `halfmoon.tools.skipped.total` | Counter | `tool_exec_skipped` | `agent_id`, `tool`, `reason` |
| `halfmoon.subturns.spawned.total` | Counter | `subturn_spawn` | `parent_agent_id`, `child_agent_id` |
| `halfmoon.subturns.orphaned.total` | Counter | `subturn_orphan` | `reason` |
| `halfmoon.errors.total` | Counter | `error` | `agent_id`, `stage` |
| `halfmoon.context.compressions.total` | Counter | `context_compress` | `agent_id`, `reason` |
| `halfmoon.interrupts.total` | Counter | `interrupt_received` | `agent_id`, `kind` |

### Resource attributes

Set once at startup:

| Attribute | Source |
|-----------|--------|
| `service.name` | `observability.service_name` config |
| `service.version` | Build version (e.g., `v0.2.3`) |
| `host.arch` | `runtime.GOARCH` |
| `os.type` | `runtime.GOOS` |

---

## New Dependencies

```
go.opentelemetry.io/otel
go.opentelemetry.io/otel/sdk
go.opentelemetry.io/otel/sdk/metric
go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc
go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp
go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc
go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp
```

All pure Go — compatible with `CGO_ENABLED=0`. Only imported when `observability.enabled` is true.

---

## Implementation Phases

### Phase 1: Event payload enrichment

**Files changed:** `pkg/agent/events.go`, `pkg/agent/loop.go`

1. Add `Model`, `InputTokens`, `OutputTokens`, `TotalTokens`, `Duration` to `LLMResponsePayload`
2. Add `Channel`, `ChatID` to `TurnEndPayload`
3. Update emit sites in `loop.go` to populate new fields
4. Update `logEvent()` to log the new fields
5. Update tests in `eventbus_test.go` to validate new fields

No new dependencies. No behavior change.

### Phase 2: Observability core + Backend interface

**New files:**

```
pkg/observability/
├── config.go       # ObservabilityConfig, OTLPConfig structs
├── backend.go      # Backend interface definition
├── exporter.go     # Exporter struct (implements agent.EventObserver)
└── exporter_test.go
```

1. Define `Backend` interface
2. Define `ObservabilityConfig` struct with JSON/env tags
3. Implement `Exporter`:
   - `OnEvent()` — filters excluded events, delegates to backend
   - `Close()` — calls `backend.Close()`
4. Implement `NewBackend()` factory function
5. Test with a mock backend

### Phase 3: OTLP backend

**New files:**

```
pkg/observability/
├── otlp/
│   ├── backend.go      # OTELBackend implementing Backend
│   ├── spans.go        # Span lifecycle manager
│   ├── metrics.go      # Metric instrument definitions and recording
│   ├── provider.go     # TracerProvider + MeterProvider + OTLP exporter setup
│   ├── attributes.go   # Event → OTEL attribute mapping
│   └── backend_test.go # Tests with in-memory exporters
```

1. Implement `OTELBackend` struct implementing `Backend`
2. Span lifecycle: start/end spans based on event kind, `sync.Map` for active spans, stale cleanup
3. Metrics: 16 instruments (counters + histograms), recording on relevant events
4. Provider setup: `TracerProvider`, `MeterProvider`, OTLP exporters from config
5. Test with `sdktrace/tracetest` and `sdkmetric/metrictest` in-memory exporters

### Phase 4: Config integration and wiring

**Files changed:** `pkg/config/config.go`, `cmd/halfmoon/` startup

1. Add `Observability ObservabilityConfig` field to `Config`
2. Add env struct tags for `HALFMOON_OBSERVABILITY_*`
3. Wire up at startup:

```go
if cfg.Observability.Enabled {
    backend, err := observability.NewBackend(cfg.Observability)
    if err != nil {
        logger.ErrorCF("observability", "Failed to initialize backend", map[string]any{"error": err.Error()})
    } else {
        exporter := observability.NewExporter(backend, cfg.Observability)
        hookManager.Mount(agent.NamedHook("observability", exporter))
        defer exporter.Close(ctx)
    }
}
```

4. Add example config in `config/`

### Phase 5: Testing and documentation

1. Integration test: full turn lifecycle → verify spans and metrics in in-memory exporter
2. Test excluded events: verify `llm_delta` exclusion
3. Test graceful shutdown: verify pending spans are flushed
4. Test stale span cleanup: verify abandoned turns are force-ended
5. Test disabled mode: verify zero initialization when `enabled: false`
6. User docs: configuration, what traces/metrics to expect, example setups

---

## Adding a New Backend (Future)

Example: adding a Prometheus backend.

1. **Create** `pkg/observability/prometheus/backend.go`:

```go
type PrometheusBackend struct {
    counters   map[string]prometheus.Counter
    histograms map[string]prometheus.Histogram
}

func (b *PrometheusBackend) HandleEvent(ctx context.Context, evt agent.Event) error {
    // Map event to prometheus counter/histogram increments
}

func (b *PrometheusBackend) Close(ctx context.Context) error {
    // Nothing to flush — Prometheus scrapes
}
```

2. **Add** config fields:

```json
{
  "observability": {
    "backend": "prometheus",
    "prometheus": {
      "listen_addr": ":9090",
      "path": "/metrics"
    }
  }
}
```

3. **Add** factory case:

```go
case "prometheus":
    return newPrometheusBackend(cfg.Prometheus)
```

No changes to `Exporter`, `EventBus`, agent loop, or the OTLP backend.

---

## Event Reference — Complete Mapping

All 19 events and what they provide to any backend:

| # | Event Kind | Category | Key Data for Export |
|---|-----------|----------|-------------------|
| 1 | `turn_start` | Lifecycle | `channel`, `chat_id`, `user_message` length, `media_count` |
| 2 | `turn_end` | Lifecycle | `status`, `iterations`, `duration`, `final_content_length`, `channel`, `chat_id` |
| 3 | `llm_request` | LLM | `model`, `messages_count`, `tools_count`, `max_tokens`, `temperature` |
| 4 | `llm_delta` | LLM | `content_delta_length`, `reasoning_delta_length` |
| 5 | `llm_response` | LLM | `model`, `content_length`, `tool_calls`, `has_reasoning`, `input_tokens`, `output_tokens`, `total_tokens`, `duration` |
| 6 | `llm_retry` | LLM | `attempt`, `max_retries`, `reason`, `error`, `backoff` |
| 7 | `context_compress` | Context | `reason`, `dropped_messages`, `remaining_messages` |
| 8 | `session_summarize` | Context | `summarized_messages`, `kept_messages`, `summary_length`, `omitted_oversized` |
| 9 | `tool_exec_start` | Tools | `tool` name, `arguments` |
| 10 | `tool_exec_end` | Tools | `tool` name, `duration`, `for_llm_length`, `for_user_length`, `is_error`, `async` |
| 11 | `tool_exec_skipped` | Tools | `tool` name, `reason` |
| 12 | `steering_injected` | Steering | `count`, `total_content_length` |
| 13 | `follow_up_queued` | Steering | `source_tool`, `channel`, `chat_id`, `content_length` |
| 14 | `interrupt_received` | Steering | `kind`, `role`, `content_length`, `queue_depth` |
| 15 | `subturn_spawn` | Sub-agent | `agent_id` (child), `label`, `parent_turn_id` |
| 16 | `subturn_end` | Sub-agent | `agent_id` (child), `status` |
| 17 | `subturn_result_delivered` | Sub-agent | `target_channel`, `target_chat_id`, `content_length` |
| 18 | `subturn_orphan` | Sub-agent | `parent_turn_id`, `child_turn_id`, `reason` |
| 19 | `error` | Error | `stage`, `message` |

`EventMeta` on every event: `AgentID`, `TurnID`, `ParentTurnID`, `SessionKey`, `Iteration`, `TracePath`, `Source`.

---

## File Layout

```
pkg/observability/
├── backend.go          # Backend interface
├── config.go           # ObservabilityConfig, OTLPConfig structs
├── exporter.go         # Exporter (implements agent.EventObserver, delegates to Backend)
├── exporter_test.go    # Tests with mock backend
│
└── otlp/               # OTLP backend (first implementation)
    ├── backend.go      # OTELBackend struct implementing Backend
    ├── spans.go        # Span lifecycle management
    ├── metrics.go      # Metric instruments and recording
    ├── provider.go     # TracerProvider, MeterProvider, OTLP exporter setup
    ├── attributes.go   # Event payload → OTEL attribute mapping
    └── backend_test.go # Tests with in-memory OTEL exporters
```

Future backends follow the same pattern:

```
└── prometheus/
    ├── backend.go
    └── backend_test.go

└── webhook/
    ├── backend.go
    └── backend_test.go
```

---

## Constraints

- **No CGO.** OTEL Go SDK and OTLP exporters are pure Go.
- **No agent loop changes.** Hooks in via `EventObserver` on existing `HookManager`.
- **No external infra shipped.** User provides their own collector/backend.
- **Event payload changes are additive.** New fields only, no breaking changes.
- **Zero overhead when disabled.** No OTEL SDK initialized, no exporter mounted.
- **Graceful degradation.** Unreachable endpoint → log warning, drop events. Never blocks the agent loop.
- **Backend-agnostic core.** The `Exporter` and `Backend` interface don't import any OTEL packages. Only `pkg/observability/otlp/` imports the OTEL SDK.
