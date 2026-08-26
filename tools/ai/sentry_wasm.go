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
	id := interactionIDFromContext(ctx)
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

func startAttempt(ctx context.Context) context.Context {
	return startNamedAttempt(ctx, "thinkId")
}

func startNamedAttempt(ctx context.Context, idKey string) context.Context {
	ownerID := interactionIDFromContext(ctx)
	if ownerID == "" {
		return ctx
	}
	attemptID := newBridgeID()
	callBridge("startAttempt", map[string]any{
		"attemptId": attemptID,
		idKey:       ownerID,
	})
	return withAttemptID(ctx, attemptID)
}

func startCommand(ctx context.Context, turnIndex int, commandName string) {
	thinkID := interactionIDFromContext(ctx)
	if thinkID == "" {
		return
	}
	callBridge("startCommand", map[string]any{
		"thinkId":     thinkID,
		"startTime":   unixSeconds(),
		"turnIndex":   turnIndex,
		"commandName": commandName,
	})
}

func endCommand(ctx context.Context, ok bool, errorMessage string) {
	thinkID := interactionIDFromContext(ctx)
	if thinkID == "" {
		return
	}
	callBridge("endCommand", map[string]any{
		"thinkId":      thinkID,
		"endTime":      unixSeconds(),
		"ok":           ok,
		"errorMessage": errorMessage,
	})
}

func startArchive(ctx context.Context) context.Context {
	return startRoot(ctx, "startArchive", "archiveId", gameSessionID)
}

func endArchive(ctx context.Context, finish archiveFinish) {
	id := interactionIDFromContext(ctx)
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

func startArchiveAttempt(ctx context.Context) context.Context {
	return startNamedAttempt(ctx, "archiveId")
}

func startRoot(ctx context.Context, method, idKey, gameSessionId string) (out context.Context) {
	out = ctx
	if !hasSentryBridge() {
		return ctx
	}
	defer func() {
		if recover() != nil {
			out = ctx
		}
	}()
	id := newBridgeID()
	result := sentryBridge.Call(method, map[string]any{
		idKey:           id,
		"startTime":     unixSeconds(),
		"gameSessionId": gameSessionId,
	})
	if !result.Truthy() {
		return ctx
	}
	return withInteractionID(ctx, id)
}

func callBridge(method string, args map[string]any) {
	if !hasSentryBridge() {
		return
	}
	defer func() { recover() }()
	sentryBridge.Call(method, args)
}

func hasSentryBridge() (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return sentryBridge.Truthy()
}

// FetchAI asks the parent page to create the client span and perform one AI
// request. The returned value is a Promise. If the bridge is missing, the
// request falls back to window.fetch with the same init, including signal.
func FetchAI(ctx context.Context, url string, init map[string]any) (result js.Value) {
	nativeFetch := func() js.Value {
		return js.Global().Call("fetch", url, init)
	}
	if !hasSentryBridge() {
		return nativeFetch()
	}
	attemptID := attemptIDFromContext(ctx)
	if attemptID == "" {
		return nativeFetch()
	}
	args := map[string]any{
		"attemptId": attemptID,
		"url":       url,
		"method":    init["method"],
		"headers":   init["headers"],
		"body":      init["body"],
	}
	if signal, ok := init["signal"]; ok && signal != nil {
		args["signal"] = signal
	}
	defer func() {
		if recover() != nil {
			result = nativeFetch()
		}
	}()
	bridged := sentryBridge.Call("fetchAI", args)
	if !bridged.Truthy() {
		return nativeFetch()
	}
	return bridged
}

// AbortAI cancels the browser request associated with ctx, if one exists.
func AbortAI(ctx context.Context) {
	attemptID := attemptIDFromContext(ctx)
	if attemptID == "" {
		return
	}
	callBridge("abortAI", map[string]any{
		"attemptId": attemptID,
	})
}

func unixSeconds() float64 {
	return float64(time.Now().UnixMilli()) / 1000
}
