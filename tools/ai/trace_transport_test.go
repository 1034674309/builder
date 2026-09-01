package ai

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type traceTransportContextKey struct{}

type recordingTracer struct {
	span     *recordingSpan
	startErr error
	infos    []SpanInfo
}

func (t *recordingTracer) StartSpan(ctx context.Context, info SpanInfo) (context.Context, Span, error) {
	t.infos = append(t.infos, info)
	if t.startErr != nil {
		return ctx, nil, t.startErr
	}
	return context.WithValue(ctx, traceTransportContextKey{}, "derived"), t.span, nil
}

type recordingSpan struct {
	errors []ErrorInfo
	ends   []SpanEnd
}

func (s *recordingSpan) RecordError(info ErrorInfo) {
	s.errors = append(s.errors, info)
}

func (s *recordingSpan) End(info SpanEnd) {
	s.ends = append(s.ends, info)
}

func TestNewTraceTransportNil(t *testing.T) {
	if got := NewTraceTransport(nil); got != nil {
		t.Fatalf("NewTraceTransport(nil) = %T; want nil", got)
	}
}

func TestTraceTransportInteractCreatesClientSpan(t *testing.T) {
	span := &recordingSpan{}
	tracer := &recordingTracer{span: span}
	want := Response{Text: "response"}
	base := &mockTransport{
		InteractFunc: func(ctx context.Context, _ Request) (Response, error) {
			if got, want := ctx.Value(traceTransportContextKey{}), "derived"; got != want {
				t.Fatalf("Interact context value = %v; want %v", got, want)
			}
			return want, nil
		},
	}

	got, err := NewTraceTransport(base).Interact(withTracer(t.Context(), tracer), Request{})
	if err != nil {
		t.Fatalf("Interact() error = %v; want nil", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Interact() = %+v; want %+v", got, want)
	}
	assertSingleClientSpan(t, tracer, span, interactClientSpanName, SpanStatusOK)
}

func TestTraceTransportFailureEndsSpanWithoutRecordingError(t *testing.T) {
	span := &recordingSpan{}
	tracer := &recordingTracer{span: span}
	wantErr := errors.New("transport failed")
	base := &mockTransport{
		InteractFunc: func(context.Context, Request) (Response, error) {
			return Response{}, wantErr
		},
	}

	_, err := NewTraceTransport(base).Interact(withTracer(t.Context(), tracer), Request{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Interact() error = %v; want %v", err, wantErr)
	}
	assertSingleClientSpan(t, tracer, span, interactClientSpanName, SpanStatusError)
	if len(span.errors) != 0 {
		t.Fatalf("RecordError() calls = %d; want 0", len(span.errors))
	}
}

func TestTraceTransportStartFailureFallsBackToBaseTransport(t *testing.T) {
	tracer := &recordingTracer{
		span:     &recordingSpan{},
		startErr: errors.New("trace unavailable"),
	}
	baseCalled := false
	base := &mockTransport{
		InteractFunc: func(ctx context.Context, _ Request) (Response, error) {
			baseCalled = true
			if got := ctx.Value(traceTransportContextKey{}); got != nil {
				t.Fatalf("fallback context value = %v; want nil", got)
			}
			return Response{Text: "fallback"}, nil
		},
	}

	got, err := NewTraceTransport(base).Interact(withTracer(t.Context(), tracer), Request{})
	if err != nil {
		t.Fatalf("Interact() error = %v; want nil", err)
	}
	if !baseCalled {
		t.Fatal("base Interact() was not called")
	}
	if got.Text != "fallback" {
		t.Fatalf("Interact() text = %q; want fallback", got.Text)
	}
	if len(tracer.span.ends) != 0 {
		t.Fatalf("End() calls = %d; want 0", len(tracer.span.ends))
	}
}

func TestTraceTransportAnnotatesTimeoutAfterStartFailure(t *testing.T) {
	tracer := &recordingTracer{
		span:     &recordingSpan{},
		startErr: errors.New("trace unavailable"),
	}
	base := &mockTransport{
		InteractFunc: func(context.Context, Request) (Response, error) {
			return Response{}, errors.New("promise rejected: AbortError")
		},
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()

	_, err := NewTraceTransport(base).Interact(withTracer(ctx, tracer), Request{})
	if !isTransportTimeout(err) {
		t.Fatalf("Interact() error = %T %v; want TimeoutError", err, err)
	}
}

func TestTraceTransportArchiveCreatesClientSpan(t *testing.T) {
	span := &recordingSpan{}
	tracer := &recordingTracer{span: span}
	want := ArchivedHistory{Content: "summary"}
	base := &mockTransport{
		ArchiveFunc: func(ctx context.Context, _ []Turn, existingArchive string) (ArchivedHistory, error) {
			if got, want := ctx.Value(traceTransportContextKey{}), "derived"; got != want {
				t.Fatalf("Archive context value = %v; want %v", got, want)
			}
			if existingArchive != "existing" {
				t.Fatalf("existingArchive = %q; want existing", existingArchive)
			}
			return want, nil
		},
	}

	got, err := NewTraceTransport(base).Archive(withTracer(t.Context(), tracer), nil, "existing")
	if err != nil {
		t.Fatalf("Archive() error = %v; want nil", err)
	}
	if got != want {
		t.Fatalf("Archive() = %+v; want %+v", got, want)
	}
	assertSingleClientSpan(t, tracer, span, archiveClientSpanName, SpanStatusOK)
}

func assertSingleClientSpan(t *testing.T, tracer *recordingTracer, span *recordingSpan, name string, status SpanStatus) {
	t.Helper()
	if got, want := len(tracer.infos), 1; got != want {
		t.Fatalf("StartSpan() calls = %d; want %d", got, want)
	}
	if got, want := tracer.infos[0].Name, name; got != want {
		t.Fatalf("span name = %q; want %q", got, want)
	}
	if got, want := tracer.infos[0].Operation, clientSpanOperation; got != want {
		t.Fatalf("span operation = %q; want %q", got, want)
	}
	if got, want := len(span.ends), 1; got != want {
		t.Fatalf("End() calls = %d; want %d", got, want)
	}
	if got, want := span.ends[0].Status, status; got != want {
		t.Fatalf("span status = %q; want %q", got, want)
	}
}
