package ai

import (
	"context"
	"errors"
	"testing"
	"time"
)

func playerReadyToArchive() *Player {
	history := make([]Turn, 30)
	for i := range history {
		history[i].IsInitial = i%10 == 0
	}
	return &Player{
		errorHandler: func(error) {},
		history:      history,
	}
}

func TestPlayerArchiveFinish(t *testing.T) {
	originalTransport := DefaultTransport()
	t.Cleanup(func() { SetDefaultTransport(originalTransport) })

	t.Run("Success", func(t *testing.T) {
		lastArchiveFinish = archiveFinish{}
		archiveCalls := 0
		SetDefaultTransport(&mockTransport{
			ArchiveFunc: func(_ context.Context, _ []Turn, _ string) (ArchivedHistory, error) {
				archiveCalls++
				return ArchivedHistory{Content: "ok"}, nil
			},
		})
		p := playerReadyToArchive()
		p.manageHistory(t.Context())
		if archiveCalls != 1 {
			t.Fatalf("archive calls = %d, want 1", archiveCalls)
		}
		if !lastArchiveFinish.ok || lastArchiveFinish.shouldCaptureException() {
			t.Fatalf("got %+v", lastArchiveFinish)
		}
		if lastArchiveFinish.attemptCount != 1 {
			t.Fatalf("attemptCount = %d", lastArchiveFinish.attemptCount)
		}
		if p.archivedHistory != "ok" {
			t.Fatalf("archivedHistory = %q", p.archivedHistory)
		}
	})

	t.Run("RetriesExhaustedCapturesOnce", func(t *testing.T) {
		lastArchiveFinish = archiveFinish{}
		archiveCalls := 0
		SetDefaultTransport(&mockTransport{
			ArchiveFunc: func(_ context.Context, _ []Turn, _ string) (ArchivedHistory, error) {
				archiveCalls++
				return ArchivedHistory{}, errors.New("archive boom")
			},
		})
		p := playerReadyToArchive()
		p.manageHistory(t.Context())
		if archiveCalls != 3 {
			t.Fatalf("archive calls = %d, want 3", archiveCalls)
		}
		if lastArchiveFinish.ok || !lastArchiveFinish.shouldCaptureException() {
			t.Fatalf("got %+v", lastArchiveFinish)
		}
		if lastArchiveFinish.attemptCount != 3 {
			t.Fatalf("attemptCount = %d", lastArchiveFinish.attemptCount)
		}
		payload := lastArchiveFinish.bridgeException("s")
		if payload == nil {
			t.Fatal("expected exception payload")
		}
		if payload["category"] != categoryArchiveFailure || payload["reason"] != reasonRetriesExhausted {
			t.Fatalf("payload = %#v", payload)
		}
		if p.archiveInProgress {
			t.Fatal("archiveInProgress should be cleared")
		}
	})

	t.Run("QuotaNoException", func(t *testing.T) {
		lastArchiveFinish = archiveFinish{}
		archiveCalls := 0
		SetDefaultTransport(&mockTransport{
			ArchiveFunc: func(_ context.Context, _ []Turn, _ string) (ArchivedHistory, error) {
				archiveCalls++
				return ArchivedHistory{}, &QuotaExceededError{Err: errors.New("quota exceeded")}
			},
		})
		p := playerReadyToArchive()
		p.manageHistory(t.Context())
		if archiveCalls != 1 {
			t.Fatalf("archive calls = %d, want 1", archiveCalls)
		}
		if lastArchiveFinish.ok || lastArchiveFinish.shouldCaptureException() {
			t.Fatalf("quota must not capture, got %+v", lastArchiveFinish)
		}
		if lastArchiveFinish.bridgeException("s") != nil {
			t.Fatal("quota must not have exception payload")
		}
		if lastArchiveFinish.attemptCount != 1 {
			t.Fatalf("attemptCount = %d", lastArchiveFinish.attemptCount)
		}
	})

	t.Run("CancelledNoException", func(t *testing.T) {
		lastArchiveFinish = archiveFinish{}
		SetDefaultTransport(&mockTransport{
			ArchiveFunc: func(_ context.Context, _ []Turn, _ string) (ArchivedHistory, error) {
				t.Fatal("Archive should not run on cancelled context")
				return ArchivedHistory{}, nil
			},
		})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		p := playerReadyToArchive()
		p.manageHistory(ctx)
		if lastArchiveFinish.ok || lastArchiveFinish.shouldCaptureException() {
			t.Fatalf("cancel must not capture, got %+v", lastArchiveFinish)
		}
		if lastArchiveFinish.bridgeException("s") != nil {
			t.Fatal("cancel must not have exception payload")
		}
	})

	t.Run("RateLimitedNoException", func(t *testing.T) {
		lastArchiveFinish = archiveFinish{}
		archiveCalls := 0
		SetDefaultTransport(&mockTransport{
			ArchiveFunc: func(_ context.Context, _ []Turn, _ string) (ArchivedHistory, error) {
				archiveCalls++
				return ArchivedHistory{}, &TooManyRequestsError{RetryAfter: time.Millisecond, Err: errors.New("429")}
			},
		})
		p := playerReadyToArchive()
		p.manageHistory(t.Context())
		if archiveCalls != 3 {
			t.Fatalf("archive calls = %d, want 3", archiveCalls)
		}
		if lastArchiveFinish.shouldCaptureException() {
			t.Fatal("archive 429 must not capture")
		}
		if lastArchiveFinish.attemptCount != 3 {
			t.Fatalf("attemptCount = %d", lastArchiveFinish.attemptCount)
		}
		if lastArchiveFinish.outcome != outcomeRateLimited {
			t.Fatalf("outcome = %q; want rate_limited", lastArchiveFinish.outcome)
		}
	})
}
