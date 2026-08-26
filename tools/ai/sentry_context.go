package ai

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

type interactionContextKey struct{}

type attemptContextKey struct{}

var bridgeIDSeq atomic.Uint64

func newBridgeID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixMilli(), bridgeIDSeq.Add(1))
}

func withInteractionID(ctx context.Context, id string) context.Context {
	return withContextString(ctx, interactionContextKey{}, id)
}

func interactionIDFromContext(ctx context.Context) string {
	return contextString(ctx, interactionContextKey{})
}

func withAttemptID(ctx context.Context, attemptID string) context.Context {
	return withContextString(ctx, attemptContextKey{}, attemptID)
}

func attemptIDFromContext(ctx context.Context) string {
	return contextString(ctx, attemptContextKey{})
}

func withContextString(ctx context.Context, key any, value string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if value == "" {
		return ctx
	}
	return context.WithValue(ctx, key, value)
}

func contextString(ctx context.Context, key any) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(key).(string)
	return id
}
