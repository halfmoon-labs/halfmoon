package otlp

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otelmetric "go.opentelemetry.io/otel/metric"

	"github.com/halfmoon-labs/halfmoon/pkg/agent"
	"github.com/halfmoon-labs/halfmoon/pkg/logger"
)

const tracerName = "halfmoon"

// Config holds OTLP-specific settings.
type Config struct {
	Endpoint         string
	Protocol         string
	Headers          map[string]string
	Insecure         bool
	TimeoutMs        int
	ExportIntervalMs int
	BatchSize        int
	ServiceName      string
	ServiceVersion   string
}

// Backend implements the observability Backend interface using OpenTelemetry OTLP export.
type Backend struct {
	providers *providers
	spans     *spanManager
	inst      *instruments
}

// NewBackend creates an OTLP backend. Returns an error if the endpoint is not configured.
func NewBackend(cfg Config) (*Backend, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("otlp backend requires an endpoint")
	}

	ctx := context.Background()
	prov, err := newProviders(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("initialize otel providers: %w", err)
	}

	inst, err := newInstruments(prov.meterProvider)
	if err != nil {
		prov.shutdown(ctx) //nolint:errcheck
		return nil, fmt.Errorf("create otel instruments: %w", err)
	}

	tracer := prov.tracerProvider.Tracer(tracerName)

	logger.InfoCF("observability", "OTLP backend ready", map[string]any{
		"endpoint": cfg.Endpoint,
		"protocol": cfg.Protocol,
		"insecure": cfg.Insecure,
	})

	return &Backend{
		providers: prov,
		spans:     newSpanManager(tracer),
		inst:      inst,
	}, nil
}

func (b *Backend) Name() string { return "otlp" }

func (b *Backend) HandleEvent(ctx context.Context, evt agent.Event) error {
	switch p := evt.Payload.(type) {
	case agent.TurnStartPayload:
		b.handleTurnStart(ctx, evt.Meta, p)
	case agent.TurnEndPayload:
		b.handleTurnEnd(ctx, evt.Meta, p)
	case agent.LLMRequestPayload:
		b.handleLLMRequest(ctx, evt.Meta, p)
	case agent.LLMResponsePayload:
		b.handleLLMResponse(ctx, evt.Meta, p)
	case agent.LLMDeltaPayload:
		b.handleLLMDelta(evt.Meta, p)
	case agent.LLMRetryPayload:
		b.handleLLMRetry(ctx, evt.Meta, p)
	case agent.ToolExecStartPayload:
		b.handleToolExecStart(evt.Meta, p)
	case agent.ToolExecEndPayload:
		b.handleToolExecEnd(ctx, evt.Meta, p)
	case agent.ToolExecSkippedPayload:
		b.handleToolExecSkipped(ctx, evt.Meta, p)
	case agent.SubTurnSpawnPayload:
		b.handleSubTurnSpawn(ctx, evt.Meta, p)
	case agent.SubTurnEndPayload:
		b.handleSubTurnEnd(ctx, evt.Meta, p)
	case agent.SubTurnResultDeliveredPayload:
		b.handleSubTurnResultDelivered(evt.Meta, p)
	case agent.SubTurnOrphanPayload:
		b.handleSubTurnOrphan(ctx, evt.Meta, p)
	case agent.ErrorPayload:
		b.handleError(ctx, evt.Meta, p)
	case agent.ContextCompressPayload:
		b.handleContextCompress(ctx, evt.Meta, p)
	case agent.SessionSummarizePayload:
		b.handleSessionSummarize(evt.Meta, p)
	case agent.SteeringInjectedPayload:
		b.handleSteeringInjected(evt.Meta, p)
	case agent.FollowUpQueuedPayload:
		b.handleFollowUpQueued(evt.Meta, p)
	case agent.InterruptReceivedPayload:
		b.handleInterruptReceived(ctx, evt.Meta, p)
	}
	return nil
}

func (b *Backend) Close(ctx context.Context) error {
	logger.InfoCF("observability", "Shutting down OTLP backend, flushing pending data", nil)
	b.spans.stop()
	err := b.providers.shutdown(ctx)
	if err != nil {
		logger.ErrorCF("observability", "OTLP shutdown error", map[string]any{"error": err.Error()})
	} else {
		logger.InfoCF("observability", "OTLP backend shut down cleanly", nil)
	}
	return err
}

// --- Turn lifecycle ---

func (b *Backend) handleTurnStart(_ context.Context, meta agent.EventMeta, p agent.TurnStartPayload) {
	key := spanKey{TurnID: meta.TurnID, SpanType: "turn"}
	attrs := append(metaAttributes(meta), turnStartAttributes(p)...)
	b.spans.startSpan(key, "halfmoon.turn", context.Background(), attrs)
}

func (b *Backend) handleTurnEnd(ctx context.Context, meta agent.EventMeta, p agent.TurnEndPayload) {
	key := spanKey{TurnID: meta.TurnID, SpanType: "turn"}
	status := codes.Ok
	desc := ""
	if p.Status == agent.TurnEndStatusError || p.Status == agent.TurnEndStatusAborted {
		status = codes.Error
		desc = string(p.Status)
	}
	b.spans.endSpan(key, turnEndAttributes(p), status, desc)

	// Record metrics
	metricAttrs := otelmetric.WithAttributes(
		attribute.String("agent_id", meta.AgentID),
		attribute.String("channel", p.Channel),
		attribute.String("status", string(p.Status)),
	)
	b.inst.turnsTotal.Add(ctx, 1, metricAttrs)
	b.inst.turnsDuration.Record(ctx, float64(p.Duration.Milliseconds()), metricAttrs)
	b.inst.turnsIterations.Record(ctx, int64(p.Iterations), otelmetric.WithAttributes(
		attribute.String("agent_id", meta.AgentID),
		attribute.String("channel", p.Channel),
	))
}

// --- LLM lifecycle ---

func (b *Backend) handleLLMRequest(ctx context.Context, meta agent.EventMeta, p agent.LLMRequestPayload) {
	turnKey := spanKey{TurnID: meta.TurnID, SpanType: "turn"}
	parentCtx, ok := b.spans.getContext(turnKey)
	if !ok {
		parentCtx = context.Background()
	}

	llmKey := spanKey{TurnID: meta.TurnID, SpanType: "llm", Iteration: meta.Iteration}
	attrs := append(metaAttributes(meta), llmRequestAttributes(p)...)
	b.spans.startSpan(llmKey, "halfmoon.llm_call", parentCtx, attrs)

	b.inst.llmRequests.Add(ctx, 1, otelmetric.WithAttributes(
		attribute.String("agent_id", meta.AgentID),
		attribute.String("model", p.Model),
	))
}

func (b *Backend) handleLLMResponse(ctx context.Context, meta agent.EventMeta, p agent.LLMResponsePayload) {
	llmKey := spanKey{TurnID: meta.TurnID, SpanType: "llm", Iteration: meta.Iteration}
	b.spans.endSpan(llmKey, llmResponseAttributes(p), codes.Unset, "")

	// Record metrics
	modelAttrs := otelmetric.WithAttributes(
		attribute.String("agent_id", meta.AgentID),
		attribute.String("model", p.Model),
	)
	b.inst.llmDuration.Record(ctx, float64(p.Duration.Milliseconds()), modelAttrs)
	b.inst.llmTokensInput.Add(ctx, int64(p.InputTokens), modelAttrs)
	b.inst.llmTokensOutput.Add(ctx, int64(p.OutputTokens), modelAttrs)
}

func (b *Backend) handleLLMDelta(meta agent.EventMeta, p agent.LLMDeltaPayload) {
	llmKey := spanKey{TurnID: meta.TurnID, SpanType: "llm", Iteration: meta.Iteration}
	b.spans.addEvent(llmKey, "llm_delta", []attribute.KeyValue{
		attribute.Int("llm.content_delta_length", p.ContentDeltaLen),
		attribute.Int("llm.reasoning_delta_length", p.ReasoningDeltaLen),
	})
}

func (b *Backend) handleLLMRetry(ctx context.Context, meta agent.EventMeta, p agent.LLMRetryPayload) {
	llmKey := spanKey{TurnID: meta.TurnID, SpanType: "llm", Iteration: meta.Iteration}
	b.spans.addEvent(llmKey, "llm_retry", llmRetryAttributes(p))

	b.inst.llmRetries.Add(ctx, 1, otelmetric.WithAttributes(
		attribute.String("agent_id", meta.AgentID),
		attribute.String("reason", p.Reason),
	))
}

// --- Tool lifecycle ---

func (b *Backend) handleToolExecStart(meta agent.EventMeta, p agent.ToolExecStartPayload) {
	turnKey := spanKey{TurnID: meta.TurnID, SpanType: "turn"}
	parentCtx, ok := b.spans.getContext(turnKey)
	if !ok {
		parentCtx = context.Background()
	}

	toolKey := spanKey{TurnID: meta.TurnID, SpanType: "tool", Iteration: meta.Iteration, Name: p.Tool}
	attrs := append(metaAttributes(meta), toolExecStartAttributes(p)...)
	b.spans.startSpan(toolKey, "halfmoon.tool_exec", parentCtx, attrs)
}

func (b *Backend) handleToolExecEnd(ctx context.Context, meta agent.EventMeta, p agent.ToolExecEndPayload) {
	toolKey := spanKey{TurnID: meta.TurnID, SpanType: "tool", Iteration: meta.Iteration, Name: p.Tool}
	status := codes.Unset
	desc := ""
	if p.IsError {
		status = codes.Error
		desc = "tool execution error"
	}
	b.spans.endSpan(toolKey, toolExecEndAttributes(p), status, desc)

	// Record metrics
	toolAttrs := otelmetric.WithAttributes(
		attribute.String("agent_id", meta.AgentID),
		attribute.String("tool", p.Tool),
		attribute.Bool("is_error", p.IsError),
	)
	b.inst.toolsExecTotal.Add(ctx, 1, toolAttrs)
	b.inst.toolsDuration.Record(ctx, float64(p.Duration.Milliseconds()), otelmetric.WithAttributes(
		attribute.String("agent_id", meta.AgentID),
		attribute.String("tool", p.Tool),
	))
}

func (b *Backend) handleToolExecSkipped(ctx context.Context, meta agent.EventMeta, p agent.ToolExecSkippedPayload) {
	turnKey := spanKey{TurnID: meta.TurnID, SpanType: "turn"}
	b.spans.addEvent(turnKey, "tool_skipped", []attribute.KeyValue{
		attribute.String("tool.name", p.Tool),
		attribute.String("tool.skip_reason", p.Reason),
	})
	b.inst.toolsSkipped.Add(ctx, 1, otelmetric.WithAttributes(
		attribute.String("agent_id", meta.AgentID),
		attribute.String("tool", p.Tool),
		attribute.String("reason", p.Reason),
	))
}

// --- Sub-turn lifecycle ---

func (b *Backend) handleSubTurnSpawn(ctx context.Context, meta agent.EventMeta, p agent.SubTurnSpawnPayload) {
	turnKey := spanKey{TurnID: meta.TurnID, SpanType: "turn"}
	parentCtx, ok := b.spans.getContext(turnKey)
	if !ok {
		parentCtx = context.Background()
	}

	subturnKey := spanKey{TurnID: meta.TurnID, SpanType: "subturn", Iteration: meta.Iteration}
	attrs := append(metaAttributes(meta), subturnSpawnAttributes(p)...)
	b.spans.startSpan(subturnKey, "halfmoon.subturn", parentCtx, attrs)

	b.inst.subturnsSpawned.Add(ctx, 1, otelmetric.WithAttributes(
		attribute.String("parent_agent_id", meta.AgentID),
		attribute.String("child_agent_id", p.AgentID),
	))
}

func (b *Backend) handleSubTurnEnd(_ context.Context, meta agent.EventMeta, p agent.SubTurnEndPayload) {
	subturnKey := spanKey{TurnID: meta.TurnID, SpanType: "subturn", Iteration: meta.Iteration}
	b.spans.endSpan(subturnKey, subturnEndAttributes(p), codes.Unset, "")
}

func (b *Backend) handleSubTurnResultDelivered(meta agent.EventMeta, p agent.SubTurnResultDeliveredPayload) {
	subturnKey := spanKey{TurnID: meta.TurnID, SpanType: "subturn", Iteration: meta.Iteration}
	b.spans.addEvent(subturnKey, "result_delivered", []attribute.KeyValue{
		attribute.String("subturn.target_channel", p.TargetChannel),
		attribute.String("subturn.target_chat_id", p.TargetChatID),
		attribute.Int("subturn.content_length", p.ContentLen),
	})
}

func (b *Backend) handleSubTurnOrphan(ctx context.Context, meta agent.EventMeta, p agent.SubTurnOrphanPayload) {
	subturnKey := spanKey{TurnID: meta.TurnID, SpanType: "subturn", Iteration: meta.Iteration}
	b.spans.recordError(subturnKey, []attribute.KeyValue{
		attribute.String("subturn.parent_turn_id", p.ParentTurnID),
		attribute.String("subturn.child_turn_id", p.ChildTurnID),
		attribute.String("subturn.reason", p.Reason),
	})
	b.inst.subturnsOrphaned.Add(ctx, 1, otelmetric.WithAttributes(
		attribute.String("reason", p.Reason),
	))
}

// --- Context and session ---

func (b *Backend) handleContextCompress(ctx context.Context, meta agent.EventMeta, p agent.ContextCompressPayload) {
	turnKey := spanKey{TurnID: meta.TurnID, SpanType: "turn"}
	b.spans.addEvent(turnKey, "context_compress", []attribute.KeyValue{
		attribute.String("compress.reason", string(p.Reason)),
		attribute.Int("compress.dropped_messages", p.DroppedMessages),
		attribute.Int("compress.remaining_messages", p.RemainingMessages),
	})
	b.inst.contextCompress.Add(ctx, 1, otelmetric.WithAttributes(
		attribute.String("agent_id", meta.AgentID),
		attribute.String("reason", string(p.Reason)),
	))
}

func (b *Backend) handleSessionSummarize(meta agent.EventMeta, p agent.SessionSummarizePayload) {
	turnKey := spanKey{TurnID: meta.TurnID, SpanType: "turn"}
	b.spans.addEvent(turnKey, "session_summarize", []attribute.KeyValue{
		attribute.Int("summarize.summarized_messages", p.SummarizedMessages),
		attribute.Int("summarize.kept_messages", p.KeptMessages),
		attribute.Int("summarize.summary_length", p.SummaryLen),
		attribute.Bool("summarize.omitted_oversized", p.OmittedOversized),
	})
}

// --- Steering and interrupts ---

func (b *Backend) handleSteeringInjected(meta agent.EventMeta, p agent.SteeringInjectedPayload) {
	turnKey := spanKey{TurnID: meta.TurnID, SpanType: "turn"}
	b.spans.addEvent(turnKey, "steering_injected", []attribute.KeyValue{
		attribute.Int("steering.count", p.Count),
		attribute.Int("steering.total_content_length", p.TotalContentLen),
	})
}

func (b *Backend) handleFollowUpQueued(meta agent.EventMeta, p agent.FollowUpQueuedPayload) {
	turnKey := spanKey{TurnID: meta.TurnID, SpanType: "turn"}
	b.spans.addEvent(turnKey, "follow_up_queued", []attribute.KeyValue{
		attribute.String("followup.source_tool", p.SourceTool),
		attribute.String("followup.channel", p.Channel),
		attribute.String("followup.chat_id", p.ChatID),
		attribute.Int("followup.content_length", p.ContentLen),
	})
}

func (b *Backend) handleInterruptReceived(ctx context.Context, meta agent.EventMeta, p agent.InterruptReceivedPayload) {
	turnKey := spanKey{TurnID: meta.TurnID, SpanType: "turn"}
	b.spans.addEvent(turnKey, "interrupt_received", []attribute.KeyValue{
		attribute.String("interrupt.kind", string(p.Kind)),
		attribute.String("interrupt.role", p.Role),
		attribute.Int("interrupt.content_length", p.ContentLen),
		attribute.Int("interrupt.queue_depth", p.QueueDepth),
	})
	b.inst.interruptsTotal.Add(ctx, 1, otelmetric.WithAttributes(
		attribute.String("agent_id", meta.AgentID),
		attribute.String("kind", string(p.Kind)),
	))
}

// --- Errors ---

func (b *Backend) handleError(ctx context.Context, meta agent.EventMeta, p agent.ErrorPayload) {
	// Try to record on the active turn span
	turnKey := spanKey{TurnID: meta.TurnID, SpanType: "turn"}
	b.spans.recordError(turnKey, errorAttributes(p))

	b.inst.errorsTotal.Add(ctx, 1, otelmetric.WithAttributes(
		attribute.String("agent_id", meta.AgentID),
		attribute.String("stage", p.Stage),
	))
}
