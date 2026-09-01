package ai

import (
	"context"
	"sync"
)

// SpanStatus describes the final state of a trace span.
type SpanStatus string

const (
	// SpanStatusUnset leaves the final status unspecified.
	SpanStatusUnset SpanStatus = ""
	// SpanStatusOK indicates that the traced operation completed successfully.
	SpanStatusOK SpanStatus = "ok"
	// SpanStatusError indicates that the traced operation failed.
	SpanStatusError SpanStatus = "error"
)

// SpanInfo describes a span when it starts.
type SpanInfo struct {
	Name       string
	Operation  string
	Attributes map[string]any
}

// SpanEnd describes the final state and attributes of a span.
type SpanEnd struct {
	Status     SpanStatus
	Attributes map[string]any
}

// ErrorInfo describes a recorded error without requiring a concrete tracing
// backend or carrying raw error text.
type ErrorInfo struct {
	Attributes map[string]any
}

// Span is an active trace span.
type Span interface {
	RecordError(info ErrorInfo)
	End(info SpanEnd)
}

// Tracer starts trace spans and returns a context derived for child work.
type Tracer interface {
	StartSpan(ctx context.Context, info SpanInfo) (context.Context, Span, error)
}

type noopTracer struct{}

type noopSpan struct{}

// NewNoopTracer returns a Tracer that records nothing.
func NewNoopTracer() Tracer {
	return noopTracer{}
}

func (noopTracer) StartSpan(ctx context.Context, _ SpanInfo) (context.Context, Span, error) {
	return ctx, noopSpan{}, nil
}

func (noopSpan) RecordError(ErrorInfo) {}

func (noopSpan) End(SpanEnd) {}

var (
	defaultTracer   Tracer = NewNoopTracer()
	defaultTracerMu sync.RWMutex
)

// DefaultTracer returns the default Tracer used by AI operations.
func DefaultTracer() Tracer {
	defaultTracerMu.RLock()
	defer defaultTracerMu.RUnlock()
	return defaultTracer
}

// SetDefaultTracer sets the default Tracer. Passing nil restores the Noop
// implementation.
func SetDefaultTracer(tracer Tracer) {
	defaultTracerMu.Lock()
	defer defaultTracerMu.Unlock()
	if tracer == nil {
		tracer = NewNoopTracer()
	}
	defaultTracer = tracer
}

type tracerContextKey struct{}

func withTracer(ctx context.Context, tracer Tracer) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if tracer == nil {
		tracer = NewNoopTracer()
	}
	return context.WithValue(ctx, tracerContextKey{}, tracer)
}

func tracerFromContext(ctx context.Context) Tracer {
	if ctx != nil {
		if tracer, ok := ctx.Value(tracerContextKey{}).(Tracer); ok && tracer != nil {
			return tracer
		}
	}
	return DefaultTracer()
}
