package ai

import (
	"context"
	"sync"
)

const (
	thinkSpanName      = "ai.think"
	commandSpanName    = "ai.command.execute"
	archiveSpanName    = "ai.archive"
	gameSessionIDAttr  = "game_session_id"
	outcomeAttr        = "outcome"
	categoryAttr       = "category"
	reasonAttr         = "reason"
	turnCountAttr      = "turn_count"
	attemptCountAttr   = "attempt_count"
	turnIndexAttr      = "turn_index"
	commandNameAttr    = "command_name"
	commandSuccessAttr = "success"
)

var gameSession struct {
	sync.RWMutex
	id string
}

// SetGameSessionID stores the current game run ID supplied by the runner.
func SetGameSessionID(id string) {
	gameSession.Lock()
	gameSession.id = id
	gameSession.Unlock()
}

func currentGameSessionID() string {
	gameSession.RLock()
	defer gameSession.RUnlock()
	return gameSession.id
}

func startOperationSpan(ctx context.Context, info SpanInfo) (context.Context, Span) {
	tracer := tracerFromContext(ctx)
	ctx = withTracer(ctx, tracer)
	spanCtx, span, err := tracer.StartSpan(ctx, info)
	if err != nil || spanCtx == nil || span == nil {
		return withTracer(ctx, NewNoopTracer()), noopSpan{}
	}
	return withTracer(spanCtx, tracer), span
}

func startThink(ctx context.Context) (context.Context, Span) {
	attributes := map[string]any{}
	if sessionID := currentGameSessionID(); sessionID != "" {
		attributes[gameSessionIDAttr] = sessionID
	}
	return startOperationSpan(ctx, SpanInfo{
		Name:       thinkSpanName,
		Operation:  thinkSpanName,
		Attributes: attributes,
	})
}

func endThink(span Span, finish thinkFinish) {
	if finish.outcome == outcomeFailure {
		span.RecordError(ErrorInfo{Attributes: failureAttributes(finish.category, finish.reason)})
	}
	span.End(SpanEnd{
		Status: operationStatus(finish.outcome),
		Attributes: operationAttributes(finish.outcome, finish.category, finish.reason, map[string]any{
			turnCountAttr:    finish.turnCount,
			attemptCountAttr: finish.attemptCount,
		}),
	})
}

func startCommand(ctx context.Context, turnIndex int, commandName string) Span {
	_, span := startOperationSpan(ctx, SpanInfo{
		Name:      commandSpanName,
		Operation: commandSpanName,
		Attributes: map[string]any{
			turnIndexAttr:   turnIndex,
			commandNameAttr: commandName,
		},
	})
	return span
}

func endCommand(span Span, ok bool) {
	status := SpanStatusError
	if ok {
		status = SpanStatusOK
	}
	span.End(SpanEnd{
		Status: status,
		Attributes: map[string]any{
			commandSuccessAttr: ok,
		},
	})
}

func startArchive(ctx context.Context) (context.Context, Span) {
	attributes := map[string]any{}
	if sessionID := currentGameSessionID(); sessionID != "" {
		attributes[gameSessionIDAttr] = sessionID
	}
	return startOperationSpan(ctx, SpanInfo{
		Name:       archiveSpanName,
		Operation:  archiveSpanName,
		Attributes: attributes,
	})
}

func endArchive(span Span, finish archiveFinish) {
	if finish.outcome == outcomeFailure {
		span.RecordError(ErrorInfo{Attributes: failureAttributes(finish.category, finish.reason)})
	}
	span.End(SpanEnd{
		Status: operationStatus(finish.outcome),
		Attributes: operationAttributes(finish.outcome, finish.category, finish.reason, map[string]any{
			attemptCountAttr: finish.attemptCount,
		}),
	})
}

func operationStatus(outcome string) SpanStatus {
	switch outcome {
	case outcomeSuccess:
		return SpanStatusOK
	case outcomeFailure:
		return SpanStatusError
	default:
		return SpanStatusUnset
	}
}

func failureAttributes(category, reason string) map[string]any {
	return map[string]any{
		categoryAttr: category,
		reasonAttr:   reason,
	}
}

func operationAttributes(outcome, category, reason string, attributes map[string]any) map[string]any {
	attributes[outcomeAttr] = outcome
	if category != "" {
		attributes[categoryAttr] = category
	}
	if reason != "" {
		attributes[reasonAttr] = reason
	}
	return attributes
}
