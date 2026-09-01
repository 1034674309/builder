package ai

import (
	"context"
	"testing"
)

func TestNoopTracer(t *testing.T) {
	ctx := t.Context()
	gotCtx, span, err := NewNoopTracer().StartSpan(ctx, SpanInfo{Name: "operation"})
	if err != nil {
		t.Fatalf("StartSpan() error = %v; want nil", err)
	}
	if gotCtx != ctx {
		t.Fatalf("StartSpan() context changed; want original context")
	}
	if span == nil {
		t.Fatal("StartSpan() span is nil; want Noop span")
	}
	span.RecordError(ErrorInfo{Attributes: map[string]any{"category": "failure"}})
	span.End(SpanEnd{Status: SpanStatusOK})
}

func TestSetDefaultTracerNilRestoresNoop(t *testing.T) {
	original := DefaultTracer()
	t.Cleanup(func() { SetDefaultTracer(original) })

	SetDefaultTracer(nil)
	_, span, err := DefaultTracer().StartSpan(t.Context(), SpanInfo{})
	if err != nil {
		t.Fatalf("StartSpan() error = %v; want nil", err)
	}
	if _, ok := span.(noopSpan); !ok {
		t.Fatalf("StartSpan() span = %T; want noopSpan", span)
	}
}

func TestTracerContextKeepsSnapshot(t *testing.T) {
	original := DefaultTracer()
	t.Cleanup(func() { SetDefaultTracer(original) })

	first := &recordingTracer{span: &recordingSpan{}}
	second := &recordingTracer{span: &recordingSpan{}}
	ctx := withTracer(context.Background(), first)

	if got := tracerFromContext(ctx); got != first {
		t.Fatalf("tracerFromContext() = %p; want first tracer %p", got, first)
	}
	SetDefaultTracer(second)
	if got := tracerFromContext(ctx); got != first {
		t.Fatalf("tracerFromContext() after default change = %p; want first tracer %p", got, first)
	}
}
