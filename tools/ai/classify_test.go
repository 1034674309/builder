package ai

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

type classifyMoveCmd struct {
	Direction string
	Steps     int
	Speed     float64
}

func classifyMoveInfo(handler any) commandInfo {
	typ := reflect.TypeOf(classifyMoveCmd{})
	if handler == nil {
		handler = func(classifyMoveCmd) error { return nil }
	}
	return commandInfo{
		typ:     typ,
		handler: handler,
		spec:    extractCommandSpec(typ),
	}
}

func TestClassifyCommandStopFromHandler(t *testing.T) {
	t.Run("InvalidArguments", func(t *testing.T) {
		_, err := callCommandHandler(t.Context(), nil, classifyMoveInfo(nil), map[string]any{
			"Direction": "up",
			"Steps":     "1",
			"Speed":     1.0,
		}, 0)
		if err == nil {
			t.Fatal("expected error")
		}
		if !isInvalidArguments(err) {
			t.Fatalf("isInvalidArguments() = false, err=%v", err)
		}
		if isHandlerPanic(err) {
			t.Fatal("typed as handler panic")
		}
		category, reason, ok := classifyCommandStop(err)
		if !ok || category != categoryModelQualityFailure || reason != reasonInvalidArguments {
			t.Fatalf("classifyCommandStop() = %q, %q, %v", category, reason, ok)
		}
	})

	t.Run("HandlerPanic", func(t *testing.T) {
		info := classifyMoveInfo(func(classifyMoveCmd) error {
			panic("intentional panic in test")
		})
		_, err := callCommandHandler(t.Context(), nil, info, map[string]any{
			"Direction": "up",
			"Steps":     1,
			"Speed":     1.0,
		}, 0)
		if err == nil {
			t.Fatal("expected error")
		}
		if !isHandlerPanic(err) {
			t.Fatalf("isHandlerPanic() = false, err=%v", err)
		}
		if isInvalidArguments(err) {
			t.Fatal("typed as invalid arguments")
		}
		category, reason, ok := classifyCommandStop(err)
		if !ok || category != categoryRuntimeFailure || reason != reasonHandlerPanic {
			t.Fatalf("classifyCommandStop() = %q, %q, %v", category, reason, ok)
		}
	})

	t.Run("HandlerErrorIsNotAStopType", func(t *testing.T) {
		info := classifyMoveInfo(func(classifyMoveCmd) error {
			return errors.New("test handler error")
		})
		result, err := callCommandHandler(t.Context(), nil, info, map[string]any{
			"Direction": "up",
			"Steps":     1,
			"Speed":     1.0,
		}, 0)
		if err != nil {
			t.Fatalf("unexpected error %v", err)
		}
		if result == nil || result.Success {
			t.Fatalf("want unsuccessful result, got %#v", result)
		}
		if _, _, ok := classifyCommandStop(err); ok {
			t.Fatal("handler error must not classify as a Think stop")
		}
	})
}

func TestClassifyCommandStopUnwraps(t *testing.T) {
	inner := &invalidArgumentsError{
		command: "GoTo",
		err:     errors.New("failed to populate command fields for GoTo: type mismatch"),
	}
	wrapped := fmt.Errorf("failed to execute command GoTo: %w", inner)
	if !isInvalidArguments(wrapped) {
		t.Fatal("isInvalidArguments() must see through fmt.Errorf %%w")
	}
	category, reason, ok := classifyCommandStop(wrapped)
	if !ok || category != categoryModelQualityFailure || reason != reasonInvalidArguments {
		t.Fatalf("classifyCommandStop() = %q, %q, %v", category, reason, ok)
	}
}

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
