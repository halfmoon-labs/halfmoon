package otlp

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/halfmoon-labs/halfmoon/pkg/agent"
)

// testBackend creates a Backend wired to in-memory exporters for testing.
func testBackend(t *testing.T) (*Backend, *tracetest.InMemoryExporter, *sdkmetric.ManualReader) {
	t.Helper()

	spanExporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(spanExporter),
		sdktrace.WithResource(resource.Default()),
	)

	metricReader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(metricReader),
		sdkmetric.WithResource(resource.Default()),
	)

	inst, err := newInstruments(mp)
	if err != nil {
		t.Fatalf("newInstruments: %v", err)
	}

	tracer := tp.Tracer(tracerName)
	b := &Backend{
		providers: &providers{tracerProvider: tp, meterProvider: mp},
		spans:     newSpanManager(tracer),
		inst:      inst,
	}
	t.Cleanup(func() {
		b.spans.stop()
	})

	return b, spanExporter, metricReader
}

func meta(turnID string, iteration int) agent.EventMeta {
	return agent.EventMeta{
		AgentID:    "test-agent",
		TurnID:     turnID,
		SessionKey: "test-session",
		Iteration:  iteration,
	}
}

func TestBackend_TurnSpanLifecycle(t *testing.T) {
	b, exporter, reader := testBackend(t)
	ctx := context.Background()

	_ = b.HandleEvent(ctx, agent.Event{
		Kind:    agent.EventKindTurnStart,
		Time:    time.Now(),
		Meta:    meta("turn-1", 0),
		Payload: agent.TurnStartPayload{Channel: "telegram", ChatID: "chat-1", UserMessage: "hello", MediaCount: 0},
	})

	_ = b.HandleEvent(ctx, agent.Event{
		Kind: agent.EventKindTurnEnd,
		Time: time.Now(),
		Meta: meta("turn-1", 1),
		Payload: agent.TurnEndPayload{
			Status:     agent.TurnEndStatusCompleted,
			Iterations: 1,
			Duration:   500 * time.Millisecond,
			Channel:    "telegram",
			ChatID:     "chat-1",
		},
	})

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 completed span, got %d", len(spans))
	}
	if spans[0].Name != "halfmoon.turn" {
		t.Fatalf("expected span name halfmoon.turn, got %q", spans[0].Name)
	}
	if spans[0].Status.Code != codes.Ok {
		t.Fatalf("expected OK status, got %v", spans[0].Status.Code)
	}

	// Verify turn metric
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	found := findMetric(rm, "halfmoon.turns.total")
	if found == nil {
		t.Fatal("expected halfmoon.turns.total metric")
	}
}

func TestBackend_TurnError(t *testing.T) {
	b, exporter, _ := testBackend(t)
	ctx := context.Background()

	_ = b.HandleEvent(ctx, agent.Event{
		Kind:    agent.EventKindTurnStart,
		Time:    time.Now(),
		Meta:    meta("turn-err", 0),
		Payload: agent.TurnStartPayload{Channel: "cli"},
	})

	_ = b.HandleEvent(ctx, agent.Event{
		Kind: agent.EventKindTurnEnd,
		Time: time.Now(),
		Meta: meta("turn-err", 1),
		Payload: agent.TurnEndPayload{
			Status:   agent.TurnEndStatusError,
			Duration: 100 * time.Millisecond,
			Channel:  "cli",
		},
	})

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Fatalf("expected ERROR status, got %v", spans[0].Status.Code)
	}
}

func TestBackend_LLMCallChildSpan(t *testing.T) {
	b, exporter, reader := testBackend(t)
	ctx := context.Background()

	_ = b.HandleEvent(
		ctx,
		agent.Event{
			Kind:    agent.EventKindTurnStart,
			Time:    time.Now(),
			Meta:    meta("turn-llm", 0),
			Payload: agent.TurnStartPayload{Channel: "cli"},
		},
	)
	_ = b.HandleEvent(
		ctx,
		agent.Event{
			Kind:    agent.EventKindLLMRequest,
			Time:    time.Now(),
			Meta:    meta("turn-llm", 1),
			Payload: agent.LLMRequestPayload{Model: "gpt-4o", MessagesCount: 3, ToolsCount: 2},
		},
	)
	_ = b.HandleEvent(
		ctx,
		agent.Event{
			Kind: agent.EventKindLLMResponse,
			Time: time.Now(),
			Meta: meta("turn-llm", 1),
			Payload: agent.LLMResponsePayload{
				Model:        "gpt-4o",
				ContentLen:   100,
				InputTokens:  500,
				OutputTokens: 100,
				Duration:     200 * time.Millisecond,
			},
		},
	)
	_ = b.HandleEvent(
		ctx,
		agent.Event{
			Kind: agent.EventKindTurnEnd,
			Time: time.Now(),
			Meta: meta("turn-llm", 1),
			Payload: agent.TurnEndPayload{
				Status:   agent.TurnEndStatusCompleted,
				Duration: 300 * time.Millisecond,
				Channel:  "cli",
			},
		},
	)

	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans (turn + llm_call), got %d", len(spans))
	}

	// Find the llm_call span
	var llmSpan *tracetest.SpanStub
	for i := range spans {
		if spans[i].Name == "halfmoon.llm_call" {
			llmSpan = &spans[i]
		}
	}
	if llmSpan == nil {
		t.Fatal("expected halfmoon.llm_call span")
	}
	if !hasAttribute(llmSpan.Attributes, "llm.model", "gpt-4o") {
		t.Fatal("expected llm.model attribute on llm_call span")
	}

	// Verify token metrics
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	if m := findMetric(rm, "halfmoon.llm.tokens.input"); m == nil {
		t.Fatal("expected halfmoon.llm.tokens.input metric")
	}
}

func TestBackend_ToolExecChildSpan(t *testing.T) {
	b, exporter, reader := testBackend(t)
	ctx := context.Background()

	_ = b.HandleEvent(
		ctx,
		agent.Event{
			Kind:    agent.EventKindTurnStart,
			Time:    time.Now(),
			Meta:    meta("turn-tool", 0),
			Payload: agent.TurnStartPayload{Channel: "cli"},
		},
	)
	_ = b.HandleEvent(
		ctx,
		agent.Event{
			Kind:    agent.EventKindToolExecStart,
			Time:    time.Now(),
			Meta:    meta("turn-tool", 1),
			Payload: agent.ToolExecStartPayload{Tool: "web_search", Arguments: map[string]any{"query": "test"}},
		},
	)
	_ = b.HandleEvent(
		ctx,
		agent.Event{
			Kind:    agent.EventKindToolExecEnd,
			Time:    time.Now(),
			Meta:    meta("turn-tool", 1),
			Payload: agent.ToolExecEndPayload{Tool: "web_search", Duration: 150 * time.Millisecond, ForLLMLen: 500},
		},
	)
	_ = b.HandleEvent(
		ctx,
		agent.Event{
			Kind: agent.EventKindTurnEnd,
			Time: time.Now(),
			Meta: meta("turn-tool", 1),
			Payload: agent.TurnEndPayload{
				Status:   agent.TurnEndStatusCompleted,
				Duration: 200 * time.Millisecond,
				Channel:  "cli",
			},
		},
	)

	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}

	var toolSpan *tracetest.SpanStub
	for i := range spans {
		if spans[i].Name == "halfmoon.tool_exec" {
			toolSpan = &spans[i]
		}
	}
	if toolSpan == nil {
		t.Fatal("expected halfmoon.tool_exec span")
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	if m := findMetric(rm, "halfmoon.tools.executions.total"); m == nil {
		t.Fatal("expected halfmoon.tools.executions.total metric")
	}
}

func TestBackend_ErrorSetsSpanStatus(t *testing.T) {
	b, exporter, reader := testBackend(t)
	ctx := context.Background()

	_ = b.HandleEvent(
		ctx,
		agent.Event{
			Kind:    agent.EventKindTurnStart,
			Time:    time.Now(),
			Meta:    meta("turn-err2", 0),
			Payload: agent.TurnStartPayload{Channel: "cli"},
		},
	)
	_ = b.HandleEvent(
		ctx,
		agent.Event{
			Kind:    agent.EventKindError,
			Time:    time.Now(),
			Meta:    meta("turn-err2", 1),
			Payload: agent.ErrorPayload{Stage: "tool_exec", Message: "permission denied"},
		},
	)
	_ = b.HandleEvent(
		ctx,
		agent.Event{
			Kind: agent.EventKindTurnEnd,
			Time: time.Now(),
			Meta: meta("turn-err2", 1),
			Payload: agent.TurnEndPayload{
				Status:   agent.TurnEndStatusError,
				Duration: 50 * time.Millisecond,
				Channel:  "cli",
			},
		},
	)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Fatalf("expected ERROR status on turn span after error event, got %v", spans[0].Status.Code)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	if m := findMetric(rm, "halfmoon.errors.total"); m == nil {
		t.Fatal("expected halfmoon.errors.total metric")
	}
}

func TestBackend_GracefulShutdown(t *testing.T) {
	b, exporter, _ := testBackend(t)
	ctx := context.Background()

	_ = b.HandleEvent(
		ctx,
		agent.Event{
			Kind:    agent.EventKindTurnStart,
			Time:    time.Now(),
			Meta:    meta("turn-shutdown", 0),
			Payload: agent.TurnStartPayload{Channel: "cli"},
		},
	)
	_ = b.HandleEvent(
		ctx,
		agent.Event{
			Kind: agent.EventKindTurnEnd,
			Time: time.Now(),
			Meta: meta("turn-shutdown", 1),
			Payload: agent.TurnEndPayload{
				Status:   agent.TurnEndStatusCompleted,
				Duration: 100 * time.Millisecond,
				Channel:  "cli",
			},
		},
	)

	// With syncer exporter, spans are exported immediately on End().
	// Verify they exist before shutdown.
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span before shutdown, got %d", len(spans))
	}

	// Close should not error.
	if err := b.Close(ctx); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

// --- Helpers ---

func hasAttribute(attrs []attribute.KeyValue, key, value string) bool {
	for _, a := range attrs {
		if string(a.Key) == key && a.Value.AsString() == value {
			return true
		}
	}
	return false
}

func findMetric(rm metricdata.ResourceMetrics, name string) *metricdata.Metrics {
	for _, sm := range rm.ScopeMetrics {
		for i := range sm.Metrics {
			if sm.Metrics[i].Name == name {
				return &sm.Metrics[i]
			}
		}
	}
	return nil
}
