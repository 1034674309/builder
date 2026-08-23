//go:build js && wasm

package ai

import (
	"context"
	"syscall/js"
	"time"
)

var (
	gameSessionID string
	sentryBridge  js.Value
)

// SetGameSessionID stores the current game run id injected by the parent page.
func SetGameSessionID(id string) {
	gameSessionID = id
}

// SetSentryBridge stores the parent-page Sentry hook object.
func SetSentryBridge(bridge js.Value) {
	sentryBridge = bridge
}

func startThink(ctx context.Context) context.Context {
	return startRoot(ctx, "startThink", "thinkId", gameSessionID)
}

func endThink(ctx context.Context, finish thinkFinish) {
	id := sentryTraceIDFromContext(ctx)
	if id == "" {
		return
	}
	callArgs := map[string]any{
		"thinkId":      id,
		"endTime":      unixSeconds(),
		"outcome":      finish.outcome,
		"category":     finish.category,
		"reason":       finish.reason,
		"turnCount":    finish.turnCount,
		"attemptCount": finish.attemptCount,
	}
	if payload := finish.bridgeException(gameSessionID); payload != nil {
		callArgs["exception"] = payload
	}
	callBridge("endThink", callArgs)
}

func startAttempt(ctx context.Context, turnIndex, attempt int) {
	callBridge("startAttempt", map[string]any{
		"thinkId":   sentryTraceIDFromContext(ctx),
		"startTime": unixSeconds(),
		"turnIndex": turnIndex,
		"attempt":   attempt,
	})
}

func endAttempt(ctx context.Context, ok bool, err error) {
	category, reason, requestID := backendTagsFromError(err)
	callBridge("endAttempt", map[string]any{
		"thinkId":         sentryTraceIDFromContext(ctx),
		"endTime":         unixSeconds(),
		"ok":              ok,
		"backendCategory": category,
		"backendReason":   reason,
		"requestId":       requestID,
	})
}

func startCommand(ctx context.Context, turnIndex int, commandName string) {
	callBridge("startCommand", map[string]any{
		"thinkId":     sentryTraceIDFromContext(ctx),
		"startTime":   unixSeconds(),
		"turnIndex":   turnIndex,
		"commandName": commandName,
	})
}

func endCommand(ctx context.Context, ok bool, errorMessage string) {
	callBridge("endCommand", map[string]any{
		"thinkId":      sentryTraceIDFromContext(ctx),
		"endTime":      unixSeconds(),
		"ok":           ok,
		"errorMessage": errorMessage,
	})
}

func startArchive(ctx context.Context) context.Context {
	return startRoot(ctx, "startArchive", "archiveId", gameSessionID)
}

func endArchive(ctx context.Context, finish archiveFinish) {
	id := sentryTraceIDFromContext(ctx)
	if id == "" {
		return
	}
	callArgs := map[string]any{
		"archiveId":    id,
		"endTime":      unixSeconds(),
		"attemptCount": finish.attemptCount,
	}
	if finish.ok {
		callArgs["ok"] = true
	}
	if payload := finish.bridgeException(gameSessionID); payload != nil {
		callArgs["exception"] = payload
		callArgs["category"] = categoryArchiveFailure
		callArgs["reason"] = reasonRetriesExhausted
		callArgs["ok"] = false
	}
	callBridge("endArchive", callArgs)
}

func startArchiveAttempt(ctx context.Context, attempt int) {
	callBridge("startArchiveAttempt", map[string]any{
		"archiveId": sentryTraceIDFromContext(ctx),
		"startTime": unixSeconds(),
		"attempt":   attempt,
	})
}

func endArchiveAttempt(ctx context.Context, ok bool, err error) {
	category, reason, requestID := backendTagsFromError(err)
	callBridge("endArchiveAttempt", map[string]any{
		"archiveId":       sentryTraceIDFromContext(ctx),
		"endTime":         unixSeconds(),
		"ok":              ok,
		"backendCategory": category,
		"backendReason":   reason,
		"requestId":       requestID,
	})
}

func startRoot(ctx context.Context, method, idKey, gameSessionId string) (out context.Context) {
	out = ctx
	if !sentryBridge.Truthy() {
		return ctx
	}
	defer func() {
		if recover() != nil {
			out = ctx
		}
	}()
	id := newSentryTraceID()
	result := sentryBridge.Call(method, map[string]any{
		idKey:           id,
		"startTime":     unixSeconds(),
		"gameSessionId": gameSessionId,
	})
	if !result.Truthy() {
		return ctx
	}
	trace := result.Get("sentryTrace").String()
	baggage := result.Get("baggage").String()
	if trace == "" {
		return ctx
	}
	return withSentryTrace(ctx, id, trace, baggage)
}

func callBridge(method string, args map[string]any) {
	if !sentryBridge.Truthy() {
		return
	}
	id := ""
	if raw, ok := args["thinkId"]; ok {
		id, _ = raw.(string)
	} else if raw, ok := args["archiveId"]; ok {
		id, _ = raw.(string)
	}
	if id == "" {
		return
	}
	defer func() { recover() }()
	sentryBridge.Call(method, args)
}

func unixSeconds() float64 {
	return float64(time.Now().UnixMilli()) / 1000
}
