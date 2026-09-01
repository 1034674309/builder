//go:build js && wasm

package ai

import (
	"context"
	"syscall/js"
	"time"
)

var (
	sentryBridge js.Value
)

// SetSentryBridge stores the parent-page Sentry hook object.
func SetSentryBridge(bridge js.Value) {
	sentryBridge = bridge
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

func startArchiveAttempt(ctx context.Context) context.Context {
	return startNamedAttempt(ctx, "archiveId")
}

func observeThinkFinish(thinkFinish) {}

func observeArchiveFinish(archiveFinish) {}

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
