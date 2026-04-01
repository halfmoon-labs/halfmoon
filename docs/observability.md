# Observability

Halfmoon exports agent-loop events as OpenTelemetry traces and metrics. Configure an OTLP endpoint to receive data in your own observability backend (Jaeger, Grafana, Datadog, Honeycomb, etc.).

## Quick Start

Add to your `config.json`:

```json
{
  "observability": {
    "enabled": true,
    "otlp": {
      "endpoint": "localhost:4317",
      "protocol": "grpc",
      "insecure": true
    }
  }
}
```

Start a local grafana otel instance to receive traces:

```bash
docker run -d --name grafana-otel \
    -p 3000:3000 \
    -p 4317:4317 \
    -p 4318:4318 \
    grafana/otel-lgtm:latest
```

Start Halfmoon. Visit `http://localhost:3000` (Grafana UI, login: admin/admin) to see traces under Explore → Tempo.

## Configuration

| Field | Default | Env Override | Description |
|-------|---------|-------------|-------------|
| `enabled` | `false` | `HALFMOON_OBSERVABILITY_ENABLED` | Master switch |
| `backend` | `"otlp"` | `HALFMOON_OBSERVABILITY_BACKEND` | Backend type |
| `service_name` | `"halfmoon"` | `HALFMOON_OBSERVABILITY_SERVICE_NAME` | OTEL service name |
| `excluded_events` | `[]` | — | Event kinds to skip (e.g., `["llm_delta"]`) |
| `otlp.endpoint` | — | `HALFMOON_OBSERVABILITY_OTLP_ENDPOINT` | OTLP collector address |
| `otlp.protocol` | `"grpc"` | `HALFMOON_OBSERVABILITY_OTLP_PROTOCOL` | `"grpc"` or `"http"` |
| `otlp.headers` | `{}` | — | Custom headers (auth tokens, API keys) |
| `otlp.insecure` | `false` | `HALFMOON_OBSERVABILITY_OTLP_INSECURE` | Skip TLS verification |
| `otlp.timeout_ms` | `10000` | — | Export timeout per batch |
| `otlp.export_interval_ms` | `5000` | — | Batch flush interval |
| `otlp.batch_size` | `512` | — | Max spans/metrics per batch |

Standard OTEL env vars (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, etc.) are also respected as fallbacks by the OTEL SDK.

## Traces

Each agent turn becomes a trace with child spans for LLM calls and tool executions.

```
halfmoon.turn (root)
├── halfmoon.llm_call        # LLM request → response
├── halfmoon.tool_exec       # Tool execution
├── halfmoon.llm_call        # Second LLM call (iteration 2)
└── halfmoon.subturn          # Sub-agent (linked trace)
```

### Span Attributes

**halfmoon.turn**: `agent.id`, `session.key`, `channel`, `chat_id`, `turn.status`, `turn.duration_ms`, `turn.iterations`

**halfmoon.llm_call**: `llm.model`, `llm.messages_count`, `llm.tools_count`, `llm.input_tokens`, `llm.output_tokens`, `llm.duration_ms`

**halfmoon.tool_exec**: `tool.name`, `tool.duration_ms`, `tool.is_error`, `tool.async`

**halfmoon.subturn**: `subturn.agent_id`, `subturn.label`, `subturn.parent_turn_id`

### Span Events

Events like `llm_retry`, `context_compress`, `steering_injected`, `interrupt_received`, and `tool_skipped` are attached as span events on the relevant parent span.

## Metrics

| Metric | Type | Labels |
|--------|------|--------|
| `halfmoon.turns.total` | Counter | `agent_id`, `channel`, `status` |
| `halfmoon.turns.duration_ms` | Histogram | `agent_id`, `channel`, `status` |
| `halfmoon.turns.iterations` | Histogram | `agent_id`, `channel` |
| `halfmoon.llm.requests.total` | Counter | `agent_id`, `model` |
| `halfmoon.llm.duration_ms` | Histogram | `agent_id`, `model` |
| `halfmoon.llm.tokens.input` | Counter | `agent_id`, `model` |
| `halfmoon.llm.tokens.output` | Counter | `agent_id`, `model` |
| `halfmoon.llm.retries.total` | Counter | `agent_id`, `reason` |
| `halfmoon.tools.executions.total` | Counter | `agent_id`, `tool`, `is_error` |
| `halfmoon.tools.duration_ms` | Histogram | `agent_id`, `tool` |
| `halfmoon.tools.skipped.total` | Counter | `agent_id`, `tool`, `reason` |
| `halfmoon.subturns.spawned.total` | Counter | `parent_agent_id`, `child_agent_id` |
| `halfmoon.subturns.orphaned.total` | Counter | `reason` |
| `halfmoon.errors.total` | Counter | `agent_id`, `stage` |
| `halfmoon.context.compressions.total` | Counter | `agent_id`, `reason` |
| `halfmoon.interrupts.total` | Counter | `agent_id`, `kind` |

## Example Setups

### Jaeger (local)

```bash
docker run -d -p 16686:16686 -p 4317:4317 jaegertracing/all-in-one:latest
```

Config: `endpoint: "localhost:4317"`, `protocol: "grpc"`, `insecure: true`

### Grafana (Tempo + Alloy)

Configure Grafana Alloy to receive OTLP on port 4317 and forward to Tempo.

Config: `endpoint: "localhost:4317"`, `protocol: "grpc"`, `insecure: true`

### Datadog

Configure the Datadog Agent with OTLP ingest enabled.

Config: `endpoint: "localhost:4317"`, `protocol: "grpc"`, `insecure: true`

### Honeycomb

Config: `endpoint: "api.honeycomb.io:443"`, `protocol: "grpc"`, `headers: {"x-honeycomb-team": "your-api-key"}`

## Reducing Volume

Exclude high-frequency events like `llm_delta` (fired per streaming token):

```json
{
  "observability": {
    "excluded_events": ["llm_delta"]
  }
}
```

## Disabled Mode

When `observability.enabled` is `false` (the default), no OTEL SDK is initialized, no exporter is mounted, and there is zero performance overhead.
