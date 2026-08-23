package ai

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestClassifyTransportStop(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantOutcome  string
		wantCategory string
		wantReason   string
	}{
		{
			name:        "Quota",
			err:         &QuotaExceededError{Err: errors.New("quota exceeded")},
			wantOutcome: outcomeQuotaExhausted,
		},
		{
			name:        "RateLimited",
			err:         &TooManyRequestsError{Err: errors.New("too many requests")},
			wantOutcome: outcomeRateLimited,
		},
		{
			name:         "Timeout",
			err:          &TimeoutError{Err: errors.New("deadline")},
			wantOutcome:  outcomeFailure,
			wantCategory: categoryTransportFailure,
			wantReason:   reasonTimeout,
		},
		{
			name:         "Network",
			err:          errors.New("failed to fetch"),
			wantOutcome:  outcomeFailure,
			wantCategory: categoryTransportFailure,
			wantReason:   reasonNetwork,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyTransportStop(tt.err)
			if got.outcome != tt.wantOutcome || got.category != tt.wantCategory || got.reason != tt.wantReason {
				t.Fatalf("got %+v, want outcome=%s category=%s reason=%s", got, tt.wantOutcome, tt.wantCategory, tt.wantReason)
			}
		})
	}
}

func TestPlayerThinkFinishOutcomes(t *testing.T) {
	type breakCmd struct{}

	originalTransport := DefaultTransport()
	t.Cleanup(func() { SetDefaultTransport(originalTransport) })

	t.Run("SuccessBreak", func(t *testing.T) {
		lastThinkFinish = thinkFinish{}
		SetDefaultTransport(&mockTransport{
			InteractFunc: func(_ context.Context, _ Request) (Response, error) {
				return Response{CommandName: reflect.TypeOf(breakCmd{}).Name()}, nil
			},
		})
		p := &Player{errorHandler: func(err error) { t.Errorf("unexpected error: %v", err) }}
		PlayerOnCmd_(p, breakCmd{}, func(breakCmd) error { return Break })
		p.think(t.Context(), nil, "hi", nil)
		if lastThinkFinish.outcome != outcomeSuccess {
			t.Fatalf("outcome = %q, want %s", lastThinkFinish.outcome, outcomeSuccess)
		}
		if lastThinkFinish.turnCount != 1 || lastThinkFinish.attemptCount != 1 {
			t.Fatalf("counts = turn %d attempt %d", lastThinkFinish.turnCount, lastThinkFinish.attemptCount)
		}
	})

	t.Run("MissingInitialCommand", func(t *testing.T) {
		lastThinkFinish = thinkFinish{}
		SetDefaultTransport(&mockTransport{
			InteractFunc: func(_ context.Context, _ Request) (Response, error) {
				return Response{Text: "hello"}, nil
			},
		})
		p := &Player{errorHandler: func(error) {}}
		p.think(t.Context(), nil, "hi", nil)
		if lastThinkFinish.outcome != outcomeFailure || lastThinkFinish.reason != reasonMissingInitialCommand {
			t.Fatalf("got %+v", lastThinkFinish)
		}
		if lastThinkFinish.category != categoryModelQualityFailure {
			t.Fatalf("category = %q", lastThinkFinish.category)
		}
	})

	t.Run("Cancelled", func(t *testing.T) {
		lastThinkFinish = thinkFinish{}
		SetDefaultTransport(&mockTransport{
			InteractFunc: func(ctx context.Context, _ Request) (Response, error) {
				t.Fatal("Interact should not run on cancelled context")
				return Response{}, nil
			},
		})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		p := &Player{errorHandler: func(error) {}}
		p.think(ctx, nil, "hi", nil)
		if lastThinkFinish.outcome != outcomeCancelled {
			t.Fatalf("outcome = %q, want %s", lastThinkFinish.outcome, outcomeCancelled)
		}
	})

	t.Run("RateLimitedNoRetryExhaustedAsFailure", func(t *testing.T) {
		lastThinkFinish = thinkFinish{}
		interactCalls := 0
		SetDefaultTransport(&mockTransport{
			InteractFunc: func(_ context.Context, _ Request) (Response, error) {
				interactCalls++
				return Response{}, &TooManyRequestsError{RetryAfter: time.Millisecond, Err: errors.New("429")}
			},
		})
		p := &Player{errorHandler: func(error) {}}
		p.think(t.Context(), nil, "hi", nil)
		if interactCalls != 3 {
			t.Fatalf("got %d attempts, want 3", interactCalls)
		}
		if lastThinkFinish.outcome != outcomeRateLimited {
			t.Fatalf("outcome = %q, want %s", lastThinkFinish.outcome, outcomeRateLimited)
		}
		if lastThinkFinish.category != "" || lastThinkFinish.reason != "" {
			t.Fatalf("rate limited must not set failure category, got %+v", lastThinkFinish)
		}
		if lastThinkFinish.attemptCount != 3 {
			t.Fatalf("attemptCount = %d", lastThinkFinish.attemptCount)
		}
	})

	t.Run("Timeout", func(t *testing.T) {
		lastThinkFinish = thinkFinish{}
		SetDefaultTransport(&mockTransport{
			InteractFunc: func(_ context.Context, _ Request) (Response, error) {
				return Response{}, &TimeoutError{Err: errors.New("deadline")}
			},
		})
		p := &Player{errorHandler: func(error) {}}
		p.think(t.Context(), nil, "hi", nil)
		if lastThinkFinish.outcome != outcomeFailure || lastThinkFinish.reason != reasonTimeout {
			t.Fatalf("got %+v", lastThinkFinish)
		}
	})

	t.Run("InvalidArguments", func(t *testing.T) {
		type typedCmd struct {
			Steps int
		}
		lastThinkFinish = thinkFinish{}
		SetDefaultTransport(&mockTransport{
			InteractFunc: func(_ context.Context, _ Request) (Response, error) {
				return Response{
					CommandName: reflect.TypeOf(typedCmd{}).Name(),
					CommandArgs: map[string]any{"Steps": "nope"},
				}, nil
			},
		})
		p := &Player{errorHandler: func(error) {}}
		PlayerOnCmd_(p, typedCmd{}, func(typedCmd) error { return nil })
		p.think(t.Context(), nil, "hi", nil)
		if lastThinkFinish.outcome != outcomeFailure || lastThinkFinish.reason != reasonInvalidArguments {
			t.Fatalf("got %+v", lastThinkFinish)
		}
	})

	t.Run("HandlerPanic", func(t *testing.T) {
		type panicCmd struct{}
		lastThinkFinish = thinkFinish{}
		SetDefaultTransport(&mockTransport{
			InteractFunc: func(_ context.Context, _ Request) (Response, error) {
				return Response{CommandName: reflect.TypeOf(panicCmd{}).Name()}, nil
			},
		})
		p := &Player{errorHandler: func(error) {}}
		PlayerOnCmd_(p, panicCmd{}, func(panicCmd) error {
			panic("boom")
		})
		p.think(t.Context(), nil, "hi", nil)
		if lastThinkFinish.outcome != outcomeFailure || lastThinkFinish.reason != reasonHandlerPanic {
			t.Fatalf("got %+v", lastThinkFinish)
		}
	})

	t.Run("UnknownCommandContinuesThenSucceeds", func(t *testing.T) {
		lastThinkFinish = thinkFinish{}
		calls := 0
		SetDefaultTransport(&mockTransport{
			InteractFunc: func(_ context.Context, _ Request) (Response, error) {
				calls++
				if calls == 1 {
					return Response{CommandName: "NotRegistered"}, nil
				}
				return Response{Text: "done"}, nil
			},
		})
		p := &Player{errorHandler: func(err error) { t.Errorf("unexpected error: %v", err) }}
		p.think(t.Context(), nil, "hi", nil)
		if lastThinkFinish.outcome != outcomeSuccess {
			t.Fatalf("outcome = %q, want success after unknown command then stop", lastThinkFinish.outcome)
		}
		if lastThinkFinish.turnCount != 2 {
			t.Fatalf("turnCount = %d, want 2", lastThinkFinish.turnCount)
		}
	})
}
