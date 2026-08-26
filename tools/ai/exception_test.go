package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestThinkFinishShouldCaptureException(t *testing.T) {
	if (thinkFinish{outcome: outcomeFailure}).shouldCaptureException() != true {
		t.Fatal("failure must capture")
	}
	for _, outcome := range []string{outcomeSuccess, outcomeCancelled, outcomeQuotaExhausted, outcomeRateLimited} {
		if (thinkFinish{outcome: outcome}).shouldCaptureException() {
			t.Fatalf("%s must not capture", outcome)
		}
	}
}

func TestBridgeExceptionNilWhenNotFailure(t *testing.T) {
	finish := thinkFinish{outcome: outcomeRateLimited, errMessage: "429"}
	if payload := finish.bridgeException("sess"); payload != nil {
		t.Fatalf("got %#v", payload)
	}
}

func TestBridgeExceptionPayload(t *testing.T) {
	finish := thinkFinish{
		outcome:     outcomeFailure,
		category:    categoryTransportFailure,
		reason:      reasonTimeout,
		userMsg:     "hello",
		errMessage:  strings.Repeat("x", maxExceptionErrorBytes+50),
		turnIndex:   2,
		lastAttempt: 1,
	}
	payload := finish.bridgeException("game-1")
	if payload == nil {
		t.Fatal("expected payload")
	}
	if payload["gameSessionId"] != "game-1" {
		t.Fatalf("gameSessionId = %v", payload["gameSessionId"])
	}
	if payload["reason"] != reasonTimeout {
		t.Fatalf("reason = %v", payload["reason"])
	}
	msg, _ := payload["message"].(string)
	if len(msg) != maxExceptionErrorBytes {
		t.Fatalf("message len = %d, want %d", len(msg), maxExceptionErrorBytes)
	}
	if payload["userMsg"] != "hello" {
		t.Fatalf("userMsg = %v", payload["userMsg"])
	}
}

func TestArchiveFinishShouldCaptureException(t *testing.T) {
	if !(archiveFinish{captureException: true}).shouldCaptureException() {
		t.Fatal("retries exhausted must capture")
	}
	if (archiveFinish{}).shouldCaptureException() {
		t.Fatal("success / cancel / quota must not capture")
	}
	if (archiveFinish{ok: true, attemptCount: 1}).shouldCaptureException() {
		t.Fatal("success must not capture")
	}
}

func TestArchiveBridgeExceptionPayload(t *testing.T) {
	if payload := (archiveFinish{errMessage: "boom"}).bridgeException("sess"); payload != nil {
		t.Fatalf("non-failure got %#v", payload)
	}
	finish := archiveFinish{
		captureException: true,
		attemptCount:     3,
		errMessage:       strings.Repeat("y", maxExceptionErrorBytes+20),
	}
	payload := finish.bridgeException("game-2")
	if payload == nil {
		t.Fatal("expected payload")
	}
	if payload["category"] != categoryArchiveFailure {
		t.Fatalf("category = %v", payload["category"])
	}
	if payload["reason"] != reasonRetriesExhausted {
		t.Fatalf("reason = %v", payload["reason"])
	}
	if payload["attemptCount"] != 3 {
		t.Fatalf("attemptCount = %v", payload["attemptCount"])
	}
	if payload["gameSessionId"] != "game-2" {
		t.Fatalf("gameSessionId = %v", payload["gameSessionId"])
	}
	msg, _ := payload["message"].(string)
	if len(msg) != maxExceptionErrorBytes {
		t.Fatalf("message len = %d, want %d", len(msg), maxExceptionErrorBytes)
	}
}

func TestCommandDetailTruncation(t *testing.T) {
	args := map[string]any{"note": strings.Repeat("n", maxCommandDetailBytes)}
	name, encoded := commandDetail("GoTo", args)
	if name != "GoTo" {
		t.Fatalf("name = %q", name)
	}
	if len(name)+len(encoded) > maxCommandDetailBytes {
		t.Fatalf("command+args = %d, want <= %d", len(name)+len(encoded), maxCommandDetailBytes)
	}
}

func TestPlayerThinkFailureCapturesAndExpectedDoesNot(t *testing.T) {
	originalTransport := DefaultTransport()
	t.Cleanup(func() { SetDefaultTransport(originalTransport) })

	t.Run("MissingCommand", func(t *testing.T) {
		lastThinkFinish = thinkFinish{}
		SetDefaultTransport(&mockTransport{
			InteractFunc: func(_ context.Context, _ Request) (Response, error) {
				return Response{Text: "hi"}, nil
			},
		})
		p := &Player{errorHandler: func(error) {}}
		p.think(t.Context(), nil, "user says hi", nil)
		if !lastThinkFinish.shouldCaptureException() {
			t.Fatal("missing command should capture")
		}
		if lastThinkFinish.userMsg != "user says hi" {
			t.Fatalf("userMsg = %q", lastThinkFinish.userMsg)
		}
		if lastThinkFinish.bridgeException("s") == nil {
			t.Fatal("expected exception payload")
		}
	})

	t.Run("RateLimited", func(t *testing.T) {
		lastThinkFinish = thinkFinish{}
		SetDefaultTransport(&mockTransport{
			InteractFunc: func(_ context.Context, _ Request) (Response, error) {
				return Response{}, &TooManyRequestsError{Err: errors.New("429")}
			},
		})
		p := &Player{errorHandler: func(error) {}}
		p.think(t.Context(), nil, "hello", nil)
		if lastThinkFinish.shouldCaptureException() {
			t.Fatal("429 must not capture")
		}
		if lastThinkFinish.bridgeException("s") != nil {
			t.Fatal("429 must not have exception payload")
		}
	})
}
