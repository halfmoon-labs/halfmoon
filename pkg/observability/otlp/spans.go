package otlp

import (
	"context"
	"sync"
	"time"

	"github.com/halfmoon-labs/halfmoon/pkg/logger"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const staleSpanTimeout = 10 * time.Minute

type spanEntry struct {
	span      trace.Span
	ctx       context.Context
	createdAt time.Time
}

type spanKey struct {
	TurnID    string
	SpanType  string // "turn", "llm", "tool", "subturn"
	Iteration int
	Name      string // tool name or agent ID — disambiguates concurrent tool executions
}

type spanManager struct {
	tracer trace.Tracer
	spans  sync.Map // spanKey → *spanEntry

	stopCleanup chan struct{}
	cleanupDone chan struct{}
}

func newSpanManager(tracer trace.Tracer) *spanManager {
	sm := &spanManager{
		tracer:      tracer,
		stopCleanup: make(chan struct{}),
		cleanupDone: make(chan struct{}),
	}
	go sm.cleanupLoop()
	return sm
}

func (sm *spanManager) startSpan(key spanKey, name string, parentCtx context.Context, attrs []attribute.KeyValue) context.Context {
	ctx, span := sm.tracer.Start(parentCtx, name,
		trace.WithAttributes(attrs...),
		trace.WithSpanKind(trace.SpanKindInternal),
	)
	sm.spans.Store(key, &spanEntry{
		span:      span,
		ctx:       ctx,
		createdAt: time.Now(),
	})
	return ctx
}

func (sm *spanManager) endSpan(key spanKey, attrs []attribute.KeyValue, status codes.Code, description string) {
	val, ok := sm.spans.LoadAndDelete(key)
	if !ok {
		return
	}
	entry := val.(*spanEntry)
	entry.span.SetAttributes(attrs...)
	if status != codes.Unset {
		entry.span.SetStatus(status, description)
	}
	entry.span.End()
}

func (sm *spanManager) addEvent(key spanKey, eventName string, attrs []attribute.KeyValue) {
	val, ok := sm.spans.Load(key)
	if !ok {
		return
	}
	entry := val.(*spanEntry)
	entry.span.AddEvent(eventName, trace.WithAttributes(attrs...))
}

func (sm *spanManager) recordError(key spanKey, attrs []attribute.KeyValue) {
	val, ok := sm.spans.Load(key)
	if !ok {
		return
	}
	entry := val.(*spanEntry)
	entry.span.SetStatus(codes.Error, "error event recorded")
	entry.span.AddEvent("error", trace.WithAttributes(attrs...))
}

func (sm *spanManager) getContext(key spanKey) (context.Context, bool) {
	val, ok := sm.spans.Load(key)
	if !ok {
		return context.Background(), false
	}
	return val.(*spanEntry).ctx, true
}

func (sm *spanManager) cleanupLoop() {
	defer close(sm.cleanupDone)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-sm.stopCleanup:
			return
		case <-ticker.C:
			sm.cleanupStale()
		}
	}
}

func (sm *spanManager) cleanupStale() {
	cutoff := time.Now().Add(-staleSpanTimeout)
	sm.spans.Range(func(key, val any) bool {
		entry := val.(*spanEntry)
		if entry.createdAt.Before(cutoff) {
			k := key.(spanKey)
			logger.WarnCF("observability", "Force-ending stale span", map[string]any{
				"turn_id":   k.TurnID,
				"span_type": k.SpanType,
				"age_s":     int(time.Since(entry.createdAt).Seconds()),
			})
			entry.span.SetStatus(codes.Error, "span abandoned (stale cleanup)")
			entry.span.End()
			sm.spans.Delete(key)
		}
		return true
	})
}

func (sm *spanManager) stop() {
	select {
	case <-sm.stopCleanup:
		// Already stopped.
	default:
		close(sm.stopCleanup)
	}
	<-sm.cleanupDone
}
