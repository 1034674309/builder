package ai

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewSentryTransportNil(t *testing.T) {
	if got := NewSentryTransport(nil); got != nil {
		t.Fatalf("NewSentryTransport(nil) = %T, want nil", got)
	}
}

func TestSentryTransportAnnotatesInteractError(t *testing.T) {
	base := &mockTransport{
		InteractFunc: func(context.Context, Request) (Response, error) {
			return Response{}, errors.New("promise rejected: AbortError")
		},
	}
	transport := NewSentryTransport(base)
	ctx, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()

	_, err := transport.Interact(ctx, Request{})
	if !isTransportTimeout(err) {
		t.Fatalf("Interact error = %T %v, want TimeoutError", err, err)
	}
}

func TestSentryTransportDelegatesArchive(t *testing.T) {
	want := ArchivedHistory{Content: "summary"}
	base := &mockTransport{
		ArchiveFunc: func(_ context.Context, _ []Turn, existingArchive string) (ArchivedHistory, error) {
			if existingArchive != "existing" {
				t.Fatalf("existingArchive = %q, want existing", existingArchive)
			}
			return want, nil
		},
	}

	got, err := NewSentryTransport(base).Archive(t.Context(), nil, "existing")
	if err != nil {
		t.Fatalf("Archive returned error: %v", err)
	}
	if got != want {
		t.Fatalf("Archive = %+v, want %+v", got, want)
	}
}
