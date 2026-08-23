package ai

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func assertOneThinkFinish(t *testing.T, capture bool) {
	t.Helper()
	if thinkFinishCount != 1 {
		t.Fatalf("endThink calls = %d, want 1", thinkFinishCount)
	}
	if lastThinkFinish.shouldCaptureException() != capture {
		t.Fatalf("capture = %v, want %v, finish=%+v", lastThinkFinish.shouldCaptureException(), capture, lastThinkFinish)
	}
	payload := lastThinkFinish.bridgeException("sess")
	if capture && payload == nil {
		t.Fatal("expected one exception payload")
	}
	if !capture && payload != nil {
		t.Fatalf("unexpected exception payload %#v", payload)
	}
}

func assertOneArchiveFinish(t *testing.T, capture bool) {
	t.Helper()
	if archiveFinishCount != 1 {
		t.Fatalf("endArchive calls = %d, want 1", archiveFinishCount)
	}
	if lastArchiveFinish.shouldCaptureException() != capture {
		t.Fatalf("archive capture = %v, want %v, finish=%+v", lastArchiveFinish.shouldCaptureException(), capture, lastArchiveFinish)
	}
	payload := lastArchiveFinish.bridgeException("sess")
	if capture && payload == nil {
		t.Fatal("expected one archive exception payload")
	}
	if !capture && payload != nil {
		t.Fatalf("unexpected archive exception payload %#v", payload)
	}
}

func TestSlice3FinishOnce(t *testing.T) {
	originalTransport := DefaultTransport()
	t.Cleanup(func() { SetDefaultTransport(originalTransport) })

	t.Run("Quota40301NoRetryNoException", func(t *testing.T) {
		resetSentryStub()
		interactCalls := 0
		SetDefaultTransport(&mockTransport{
			InteractFunc: func(_ context.Context, _ Request) (Response, error) {
				interactCalls++
				return Response{}, ErrorFromHTTPResponse(403, 0, `{"code":40301,"msg":"Quota exceeded","request_id":"q-1"}`, errors.New("quota exceeded"))
			},
		})
		p := &Player{errorHandler: func(error) {}}
		p.think(t.Context(), nil, "hello", nil)
		if interactCalls != 1 {
			t.Fatalf("interact calls = %d, want 1", interactCalls)
		}
		if lastThinkFinish.outcome != outcomeQuotaExhausted {
			t.Fatalf("outcome = %q", lastThinkFinish.outcome)
		}
		if lastThinkFinish.category != "" || lastThinkFinish.reason != "" {
			t.Fatalf("quota must not set failure category, got %+v", lastThinkFinish)
		}
		assertOneThinkFinish(t, false)
		if len(lastAttempts) != 1 {
			t.Fatalf("attempts = %d, want 1", len(lastAttempts))
		}
		if lastAttempts[0].ok {
			t.Fatal("quota attempt should be marked error")
		}
	})

	t.Run("Other403RetriesAsNetworkFailure", func(t *testing.T) {
		resetSentryStub()
		interactCalls := 0
		SetDefaultTransport(&mockTransport{
			InteractFunc: func(_ context.Context, _ Request) (Response, error) {
				interactCalls++
				return Response{}, ErrorFromHTTPResponse(403, 0, `{"code":40300,"msg":"Forbidden"}`, errors.New("forbidden"))
			},
		})
		p := &Player{errorHandler: func(error) {}}
		p.think(t.Context(), nil, "hello", nil)
		if interactCalls != 3 {
			t.Fatalf("interact calls = %d, want 3", interactCalls)
		}
		if lastThinkFinish.outcome != outcomeFailure || lastThinkFinish.reason != reasonNetwork {
			t.Fatalf("got %+v", lastThinkFinish)
		}
		if isQuotaExceeded(ErrorFromHTTPResponse(403, 0, `{"code":40300,"msg":"Forbidden"}`, errors.New("forbidden"))) {
			t.Fatal("40300 must not be quota")
		}
		assertOneThinkFinish(t, true)
	})

	t.Run("BackendTagsStayOnAttempt", func(t *testing.T) {
		resetSentryStub()
		body := `{"code":50000,"msg":"Internal","category":"upstream_failure","reason":"timeout","request_id":"req-xyz"}`
		SetDefaultTransport(&mockTransport{
			InteractFunc: func(_ context.Context, _ Request) (Response, error) {
				return Response{}, ErrorFromHTTPResponse(500, 0, body, errors.New("upstream"))
			},
		})
		p := &Player{errorHandler: func(error) {}}
		p.think(t.Context(), nil, "hello", nil)
		if len(lastAttempts) != 3 {
			t.Fatalf("attempts = %d, want 3", len(lastAttempts))
		}
		for i, attempt := range lastAttempts {
			if attempt.ok {
				t.Fatalf("attempt %d unexpectedly ok", i)
			}
			if attempt.category != "upstream_failure" || attempt.reason != "timeout" || attempt.requestID != "req-xyz" {
				t.Fatalf("attempt %d backend tags = %+v", i, attempt)
			}
		}
		if lastThinkFinish.category != categoryTransportFailure || lastThinkFinish.reason != reasonNetwork {
			t.Fatalf("Think must use frontend category, got %+v", lastThinkFinish)
		}
		payload := lastThinkFinish.bridgeException("sess")
		if payload["category"] != categoryTransportFailure {
			t.Fatalf("exception category = %v", payload["category"])
		}
		assertOneThinkFinish(t, true)
	})

	t.Run("HandlerErrorContinuesThenSucceeds", func(t *testing.T) {
		type workCmd struct{}
		type stopCmd struct{}
		resetSentryStub()
		calls := 0
		SetDefaultTransport(&mockTransport{
			InteractFunc: func(_ context.Context, _ Request) (Response, error) {
				calls++
				if calls == 1 {
					return Response{CommandName: reflect.TypeOf(workCmd{}).Name()}, nil
				}
				return Response{CommandName: reflect.TypeOf(stopCmd{}).Name()}, nil
			},
		})
		p := &Player{errorHandler: func(err error) { t.Errorf("unexpected error: %v", err) }}
		PlayerOnCmd_(p, workCmd{}, func(workCmd) error { return errors.New("game handler failed") })
		PlayerOnCmd_(p, stopCmd{}, func(stopCmd) error { return Break })
		p.think(t.Context(), nil, "hello", nil)
		if calls != 2 {
			t.Fatalf("interact calls = %d, want 2", calls)
		}
		if lastThinkFinish.outcome != outcomeSuccess {
			t.Fatalf("outcome = %q, want success after handler error", lastThinkFinish.outcome)
		}
		assertOneThinkFinish(t, false)
	})

	t.Run("UnknownCommandContinuesThenSucceeds", func(t *testing.T) {
		type stopCmd struct{}
		resetSentryStub()
		calls := 0
		SetDefaultTransport(&mockTransport{
			InteractFunc: func(_ context.Context, _ Request) (Response, error) {
				calls++
				if calls == 1 {
					return Response{CommandName: "NotRegistered"}, nil
				}
				return Response{CommandName: reflect.TypeOf(stopCmd{}).Name()}, nil
			},
		})
		p := &Player{errorHandler: func(err error) { t.Errorf("unexpected error: %v", err) }}
		PlayerOnCmd_(p, stopCmd{}, func(stopCmd) error { return Break })
		p.think(t.Context(), nil, "hello", nil)
		if lastThinkFinish.outcome != outcomeSuccess {
			t.Fatalf("outcome = %q", lastThinkFinish.outcome)
		}
		assertOneThinkFinish(t, false)
	})

	t.Run("TurnLimitOneException", func(t *testing.T) {
		type loopCmd struct{}
		resetSentryStub()
		SetDefaultTransport(&mockTransport{
			InteractFunc: func(_ context.Context, _ Request) (Response, error) {
				return Response{CommandName: reflect.TypeOf(loopCmd{}).Name()}, nil
			},
		})
		p := &Player{errorHandler: func(error) {}}
		PlayerOnCmd_(p, loopCmd{}, func(loopCmd) error { return nil })
		p.think(t.Context(), nil, "loop", nil)
		if lastThinkFinish.reason != reasonTurnLimit || lastThinkFinish.category != categoryModelQualityFailure {
			t.Fatalf("got %+v", lastThinkFinish)
		}
		assertOneThinkFinish(t, true)
	})

	t.Run("TimeoutOneException", func(t *testing.T) {
		resetSentryStub()
		SetDefaultTransport(&mockTransport{
			InteractFunc: func(_ context.Context, _ Request) (Response, error) {
				return Response{}, &TimeoutError{Err: errors.New("deadline")}
			},
		})
		p := &Player{errorHandler: func(error) {}}
		p.think(t.Context(), nil, "hello", nil)
		if lastThinkFinish.reason != reasonTimeout {
			t.Fatalf("got %+v", lastThinkFinish)
		}
		assertOneThinkFinish(t, true)
	})

	t.Run("RateLimitedOneFinishNoException", func(t *testing.T) {
		resetSentryStub()
		SetDefaultTransport(&mockTransport{
			InteractFunc: func(_ context.Context, _ Request) (Response, error) {
				return Response{}, &TooManyRequestsError{RetryAfter: time.Millisecond, Err: errors.New("429")}
			},
		})
		p := &Player{errorHandler: func(error) {}}
		p.think(t.Context(), nil, "hello", nil)
		if lastThinkFinish.outcome != outcomeRateLimited {
			t.Fatalf("outcome = %q", lastThinkFinish.outcome)
		}
		assertOneThinkFinish(t, false)
		if len(lastAttempts) != 3 {
			t.Fatalf("attempts = %d, want 3", len(lastAttempts))
		}
	})

	t.Run("CancelledOneFinishNoException", func(t *testing.T) {
		resetSentryStub()
		SetDefaultTransport(&mockTransport{
			InteractFunc: func(context.Context, Request) (Response, error) {
				t.Fatal("Interact should not run")
				return Response{}, nil
			},
		})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		p := &Player{errorHandler: func(error) {}}
		p.think(ctx, nil, "hello", nil)
		if lastThinkFinish.outcome != outcomeCancelled {
			t.Fatalf("outcome = %q", lastThinkFinish.outcome)
		}
		assertOneThinkFinish(t, false)
	})

	t.Run("InvalidArgumentsOneException", func(t *testing.T) {
		type typedCmd struct {
			Steps int
		}
		resetSentryStub()
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
		p.think(t.Context(), nil, "hello", nil)
		if lastThinkFinish.reason != reasonInvalidArguments {
			t.Fatalf("got %+v", lastThinkFinish)
		}
		assertOneThinkFinish(t, true)
	})

	t.Run("HandlerPanicOneException", func(t *testing.T) {
		type panicCmd struct{}
		resetSentryStub()
		SetDefaultTransport(&mockTransport{
			InteractFunc: func(_ context.Context, _ Request) (Response, error) {
				return Response{CommandName: reflect.TypeOf(panicCmd{}).Name()}, nil
			},
		})
		p := &Player{errorHandler: func(error) {}}
		PlayerOnCmd_(p, panicCmd{}, func(panicCmd) error { panic("boom") })
		p.think(t.Context(), nil, "hello", nil)
		if lastThinkFinish.reason != reasonHandlerPanic {
			t.Fatalf("got %+v", lastThinkFinish)
		}
		assertOneThinkFinish(t, true)
	})

	t.Run("ArchiveRetriesExhaustedOneException", func(t *testing.T) {
		resetSentryStub()
		SetDefaultTransport(&mockTransport{
			ArchiveFunc: func(_ context.Context, _ []Turn, _ string) (ArchivedHistory, error) {
				return ArchivedHistory{}, errors.New("archive boom")
			},
		})
		playerReadyToArchive().manageHistory(t.Context())
		assertOneArchiveFinish(t, true)
		if lastArchiveAttempts[0].ok {
			t.Fatal("failed archive attempt should be marked error")
		}
		if thinkFinishCount != 0 {
			t.Fatalf("archive must not call endThink, got %d", thinkFinishCount)
		}
	})

	t.Run("ArchiveQuotaNoException", func(t *testing.T) {
		resetSentryStub()
		archiveCalls := 0
		SetDefaultTransport(&mockTransport{
			ArchiveFunc: func(_ context.Context, _ []Turn, _ string) (ArchivedHistory, error) {
				archiveCalls++
				return ArchivedHistory{}, &QuotaExceededError{Err: errors.New("quota exceeded")}
			},
		})
		playerReadyToArchive().manageHistory(t.Context())
		if archiveCalls != 1 {
			t.Fatalf("archive calls = %d, want 1", archiveCalls)
		}
		assertOneArchiveFinish(t, false)
	})

	t.Run("ArchiveCancelNoException", func(t *testing.T) {
		resetSentryStub()
		SetDefaultTransport(&mockTransport{
			ArchiveFunc: func(context.Context, []Turn, string) (ArchivedHistory, error) {
				t.Fatal("Archive should not run")
				return ArchivedHistory{}, nil
			},
		})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		playerReadyToArchive().manageHistory(ctx)
		assertOneArchiveFinish(t, false)
	})

	t.Run("ArchiveDoesNotChangeThinkFinish", func(t *testing.T) {
		resetSentryStub()
		lastThinkFinish = thinkFinish{outcome: outcomeSuccess, turnCount: 2}
		thinkFinishCount = 1
		SetDefaultTransport(&mockTransport{
			ArchiveFunc: func(_ context.Context, _ []Turn, _ string) (ArchivedHistory, error) {
				return ArchivedHistory{}, &QuotaExceededError{Err: errors.New("quota exceeded")}
			},
		})
		playerReadyToArchive().manageHistory(t.Context())
		if lastThinkFinish.outcome != outcomeSuccess || lastThinkFinish.turnCount != 2 {
			t.Fatalf("Think finish mutated by archive: %+v", lastThinkFinish)
		}
		if lastThinkFinish.shouldCaptureException() {
			t.Fatal("archive quota must not make Think capture")
		}
		assertOneArchiveFinish(t, false)
		if thinkFinishCount != 1 {
			t.Fatalf("endThink calls = %d, want unchanged 1", thinkFinishCount)
		}
	})
}
