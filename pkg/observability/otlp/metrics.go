package otlp

import (
	"go.opentelemetry.io/otel/metric"
)

const meterName = "halfmoon"

type instruments struct {
	turnsTotal       metric.Int64Counter
	turnsDuration    metric.Float64Histogram
	turnsIterations  metric.Int64Histogram
	llmRequests      metric.Int64Counter
	llmDuration      metric.Float64Histogram
	llmTokensInput   metric.Int64Counter
	llmTokensOutput  metric.Int64Counter
	llmRetries       metric.Int64Counter
	toolsExecTotal   metric.Int64Counter
	toolsDuration    metric.Float64Histogram
	toolsSkipped     metric.Int64Counter
	subturnsSpawned  metric.Int64Counter
	subturnsOrphaned metric.Int64Counter
	errorsTotal      metric.Int64Counter
	contextCompress  metric.Int64Counter
	interruptsTotal  metric.Int64Counter
}

func newInstruments(mp metric.MeterProvider) (*instruments, error) {
	m := mp.Meter(meterName)
	var inst instruments
	var err error

	if inst.turnsTotal, err = m.Int64Counter("halfmoon.turns.total"); err != nil {
		return nil, err
	}
	if inst.turnsDuration, err = m.Float64Histogram("halfmoon.turns.duration_ms",
		metric.WithUnit("ms")); err != nil {
		return nil, err
	}
	if inst.turnsIterations, err = m.Int64Histogram("halfmoon.turns.iterations"); err != nil {
		return nil, err
	}
	if inst.llmRequests, err = m.Int64Counter("halfmoon.llm.requests.total"); err != nil {
		return nil, err
	}
	if inst.llmDuration, err = m.Float64Histogram("halfmoon.llm.duration_ms",
		metric.WithUnit("ms")); err != nil {
		return nil, err
	}
	if inst.llmTokensInput, err = m.Int64Counter("halfmoon.llm.tokens.input"); err != nil {
		return nil, err
	}
	if inst.llmTokensOutput, err = m.Int64Counter("halfmoon.llm.tokens.output"); err != nil {
		return nil, err
	}
	if inst.llmRetries, err = m.Int64Counter("halfmoon.llm.retries.total"); err != nil {
		return nil, err
	}
	if inst.toolsExecTotal, err = m.Int64Counter("halfmoon.tools.executions.total"); err != nil {
		return nil, err
	}
	if inst.toolsDuration, err = m.Float64Histogram("halfmoon.tools.duration_ms",
		metric.WithUnit("ms")); err != nil {
		return nil, err
	}
	if inst.toolsSkipped, err = m.Int64Counter("halfmoon.tools.skipped.total"); err != nil {
		return nil, err
	}
	if inst.subturnsSpawned, err = m.Int64Counter("halfmoon.subturns.spawned.total"); err != nil {
		return nil, err
	}
	if inst.subturnsOrphaned, err = m.Int64Counter("halfmoon.subturns.orphaned.total"); err != nil {
		return nil, err
	}
	if inst.errorsTotal, err = m.Int64Counter("halfmoon.errors.total"); err != nil {
		return nil, err
	}
	if inst.contextCompress, err = m.Int64Counter("halfmoon.context.compressions.total"); err != nil {
		return nil, err
	}
	if inst.interruptsTotal, err = m.Int64Counter("halfmoon.interrupts.total"); err != nil {
		return nil, err
	}

	return &inst, nil
}
