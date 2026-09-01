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
