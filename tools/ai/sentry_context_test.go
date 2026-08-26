package ai

import (
	"context"
	"testing"
	"time"
)

func TestInteractionIDInheritsToChild(t *testing.T) {
	ctx := withInteractionID(context.Background(), "id-1")
	child, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	if got := interactionIDFromContext(child); got != "id-1" {
		t.Fatalf("interactionIDFromContext(child) = %q; want id-1", got)
	}
}

func TestInteractionIDEmpty(t *testing.T) {
	if got := interactionIDFromContext(context.Background()); got != "" {
		t.Fatalf("interactionIDFromContext(background) = %q; want empty", got)
	}
	if got := interactionIDFromContext(nil); got != "" {
		t.Fatalf("interactionIDFromContext(nil) = %q; want empty", got)
	}
}

func TestInteractionIDIsolated(t *testing.T) {
	thinkCtx := withInteractionID(context.Background(), "think")
	archiveCtx := withInteractionID(context.Background(), "archive")

	if interactionIDFromContext(thinkCtx) == interactionIDFromContext(archiveCtx) {
		t.Fatal("think and archive should not share the same id")
	}
}

func TestAttemptIDInheritsToChild(t *testing.T) {
	ctx := withAttemptID(context.Background(), "attempt-1")
	child, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	if got := attemptIDFromContext(child); got != "attempt-1" {
		t.Fatalf("attemptIDFromContext(child) = %q; want attempt-1", got)
	}
}

func TestAttemptIDIsolated(t *testing.T) {
	first := withAttemptID(context.Background(), "attempt-1")
	second := withAttemptID(context.Background(), "attempt-2")
	if got := attemptIDFromContext(first); got != "attempt-1" {
		t.Fatalf("first attempt id = %q; want attempt-1", got)
	}
	if got := attemptIDFromContext(second); got != "attempt-2" {
		t.Fatalf("second attempt id = %q; want attempt-2", got)
	}
}
