package ai

import "context"

const (
	interactClientSpanName = "POST /ai-interaction/turns"
	archiveClientSpanName  = "POST /ai-interaction/archives"
	clientSpanOperation    = "http.client"
)

// traceTransport creates one client span around each real call to its wrapped
// Transport. It keeps no per-call state and is safe to reuse.
type traceTransport struct {
	next Transport
}

// NewTraceTransport wraps t with generic client tracing. A nil Transport stays
// nil so SetDefaultTransport retains its reset behavior.
func NewTraceTransport(t Transport) Transport {
	if t == nil {
		return nil
	}
	return &traceTransport{next: t}
}

func (t *traceTransport) Interact(ctx context.Context, req Request) (Response, error) {
	tracer := tracerFromContext(ctx)
	spanCtx, span, startErr := tracer.StartSpan(ctx, SpanInfo{
		Name:      interactClientSpanName,
		Operation: clientSpanOperation,
	})
	if startErr != nil {
		resp, err := t.next.Interact(ctx, req)
		return resp, AnnotateTransportError(ctx, err)
	}

	resp, err := t.next.Interact(spanCtx, req)
	err = AnnotateTransportError(spanCtx, err)
	span.End(SpanEnd{Status: spanStatusForError(err)})
	return resp, err
}

func (t *traceTransport) Archive(ctx context.Context, turns []Turn, existingArchive string) (ArchivedHistory, error) {
	tracer := tracerFromContext(ctx)
	spanCtx, span, startErr := tracer.StartSpan(ctx, SpanInfo{
		Name:      archiveClientSpanName,
		Operation: clientSpanOperation,
	})
	if startErr != nil {
		resp, err := t.next.Archive(ctx, turns, existingArchive)
		return resp, AnnotateTransportError(ctx, err)
	}

	resp, err := t.next.Archive(spanCtx, turns, existingArchive)
	err = AnnotateTransportError(spanCtx, err)
	span.End(SpanEnd{Status: spanStatusForError(err)})
	return resp, err
}

func spanStatusForError(err error) SpanStatus {
	if err != nil {
		return SpanStatusError
	}
	return SpanStatusOK
}
