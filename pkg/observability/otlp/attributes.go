package otlp

import (
	"go.opentelemetry.io/otel/attribute"

	"github.com/halfmoon-labs/halfmoon/pkg/agent"
)

func metaAttributes(meta agent.EventMeta) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("agent.id", meta.AgentID),
		attribute.String("turn.id", meta.TurnID),
		attribute.String("session.key", meta.SessionKey),
		attribute.Int("iteration", meta.Iteration),
	}
	if meta.ParentTurnID != "" {
		attrs = append(attrs, attribute.String("parent_turn.id", meta.ParentTurnID))
	}
	return attrs
}

func turnStartAttributes(p agent.TurnStartPayload) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("channel", p.Channel),
		attribute.String("chat_id", p.ChatID),
		attribute.Int("user_message.length", len(p.UserMessage)),
		attribute.Int("media.count", p.MediaCount),
	}
}

func turnEndAttributes(p agent.TurnEndPayload) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("turn.status", string(p.Status)),
		attribute.Int("turn.iterations", p.Iterations),
		attribute.Float64("turn.duration_ms", float64(p.Duration.Milliseconds())),
		attribute.Int("turn.final_content_length", p.FinalContentLen),
		attribute.String("channel", p.Channel),
		attribute.String("chat_id", p.ChatID),
	}
}

func llmRequestAttributes(p agent.LLMRequestPayload) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("llm.model", p.Model),
		attribute.Int("llm.messages_count", p.MessagesCount),
		attribute.Int("llm.tools_count", p.ToolsCount),
		attribute.Int("llm.max_tokens", p.MaxTokens),
		attribute.Float64("llm.temperature", p.Temperature),
	}
}

func llmResponseAttributes(p agent.LLMResponsePayload) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("llm.model", p.Model),
		attribute.Int("llm.content_length", p.ContentLen),
		attribute.Int("llm.tool_calls", p.ToolCalls),
		attribute.Bool("llm.has_reasoning", p.HasReasoning),
		attribute.Int("llm.input_tokens", p.InputTokens),
		attribute.Int("llm.output_tokens", p.OutputTokens),
		attribute.Int("llm.total_tokens", p.TotalTokens),
		attribute.Float64("llm.duration_ms", float64(p.Duration.Milliseconds())),
	}
}

func llmRetryAttributes(p agent.LLMRetryPayload) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.Int("llm.retry.attempt", p.Attempt),
		attribute.Int("llm.retry.max_retries", p.MaxRetries),
		attribute.String("llm.retry.reason", p.Reason),
		attribute.String("llm.retry.error", p.Error),
		attribute.Float64("llm.retry.backoff_ms", float64(p.Backoff.Milliseconds())),
	}
}

func toolExecStartAttributes(p agent.ToolExecStartPayload) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("tool.name", p.Tool),
		attribute.Int("tool.args_count", len(p.Arguments)),
	}
}

func toolExecEndAttributes(p agent.ToolExecEndPayload) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("tool.name", p.Tool),
		attribute.Float64("tool.duration_ms", float64(p.Duration.Milliseconds())),
		attribute.Int("tool.for_llm_length", p.ForLLMLen),
		attribute.Int("tool.for_user_length", p.ForUserLen),
		attribute.Bool("tool.is_error", p.IsError),
		attribute.Bool("tool.async", p.Async),
	}
}

func subturnSpawnAttributes(p agent.SubTurnSpawnPayload) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("subturn.agent_id", p.AgentID),
		attribute.String("subturn.label", p.Label),
		attribute.String("subturn.parent_turn_id", p.ParentTurnID),
	}
}

func subturnEndAttributes(p agent.SubTurnEndPayload) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("subturn.agent_id", p.AgentID),
		attribute.String("subturn.status", p.Status),
	}
}

func errorAttributes(p agent.ErrorPayload) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("error.stage", p.Stage),
		attribute.String("error.message", p.Message),
	}
}
