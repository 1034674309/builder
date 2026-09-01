package ai

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
)

type operationTraceContextKey struct{}

type operationTraceRecorder struct {
	mu       sync.Mutex
	startErr error
	starts   int
	spans    []*operationRecordedSpan
}

type operationRecordedSpan struct {
	recorder *operationTraceRecorder
	info     SpanInfo
	parent   *operationRecordedSpan
	errors   []ErrorInfo
	ends     []SpanEnd
}

func (r *operationTraceRecorder) StartSpan(ctx context.Context, info SpanInfo) (context.Context, Span, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts++
	if r.startErr != nil {
		return ctx, nil, r.startErr
	}
	span := &operationRecordedSpan{
		recorder: r,
		info:     info,
		parent:   operationSpanFromContext(ctx),
	}
	r.spans = append(r.spans, span)
	return context.WithValue(ctx, operationTraceContextKey{}, span), span, nil
}

func (s *operationRecordedSpan) RecordError(info ErrorInfo) {
	s.recorder.mu.Lock()
	defer s.recorder.mu.Unlock()
	s.errors = append(s.errors, info)
}

func (s *operationRecordedSpan) End(info SpanEnd) {
	s.recorder.mu.Lock()
	defer s.recorder.mu.Unlock()
	s.ends = append(s.ends, info)
}

func operationSpanFromContext(ctx context.Context) *operationRecordedSpan {
	span, _ := ctx.Value(operationTraceContextKey{}).(*operationRecordedSpan)
	return span
}

func (r *operationTraceRecorder) spansNamed(name string) []*operationRecordedSpan {
	r.mu.Lock()
	defer r.mu.Unlock()
	var spans []*operationRecordedSpan
	for _, span := range r.spans {
		if span.info.Name == name {
			spans = append(spans, span)
		}
	}
	return spans
}

func setupOperationTraceTest(t *testing.T, recorder *operationTraceRecorder, transport Transport) {
	t.Helper()
	originalTracer := DefaultTracer()
	originalTransport := DefaultTransport()
	originalSessionID := currentGameSessionID()
	t.Cleanup(func() {
		SetDefaultTracer(originalTracer)
		SetDefaultTransport(originalTransport)
		SetGameSessionID(originalSessionID)
	})
	SetDefaultTracer(recorder)
	SetDefaultTransport(transport)
	SetGameSessionID("session-1")
}

func TestThinkTraceSuccessAndRetry(t *testing.T) {
	type stopCmd struct{}
	recorder := &operationTraceRecorder{}
	calls := 0
	base := &mockTransport{InteractFunc: func(context.Context, Request) (Response, error) {
		calls++
		if calls == 1 {
			return Response{}, errors.New("temporary transport failure")
		}
		return Response{CommandName: reflect.TypeOf(stopCmd{}).Name()}, nil
	}}
	setupOperationTraceTest(t, recorder, NewTraceTransport(base))

	p := &Player{errorHandler: func(err error) { t.Errorf("unexpected error: %v", err) }}
	PlayerOnCmd_(p, stopCmd{}, func(stopCmd) error { return Break })
	p.think(t.Context(), nil, "private prompt", nil)

	think := onlyOperationSpan(t, recorder, thinkSpanName)
	if think.parent != nil {
		t.Fatal("Think span must be a root")
	}
	if got := think.info.Attributes[gameSessionIDAttr]; got != "session-1" {
		t.Fatalf("game session ID = %v; want session-1", got)
	}
	assertOperationEnd(t, think, SpanStatusOK, outcomeSuccess, 0)
	if got := think.ends[0].Attributes[attemptCountAttr]; got != 2 {
		t.Fatalf("attempt count = %v; want 2", got)
	}

	clients := recorder.spansNamed(interactClientSpanName)
	if len(clients) != 2 {
		t.Fatalf("client spans = %d; want 2", len(clients))
	}
	if clients[0].parent != think || clients[1].parent != think {
		t.Fatal("client spans must be children of Think")
	}
	assertOperationEnd(t, clients[0], SpanStatusError, "", 0)
	assertOperationEnd(t, clients[1], SpanStatusOK, "", 0)

	command := onlyOperationSpan(t, recorder, commandSpanName)
	if command.parent != think {
		t.Fatal("command span must be a child of Think")
	}
	assertOperationEnd(t, command, SpanStatusOK, "", 0)
}

func TestThinkTraceFinalFailureRecordsCategoryAndReasonOnce(t *testing.T) {
	recorder := &operationTraceRecorder{}
	base := &mockTransport{InteractFunc: func(context.Context, Request) (Response, error) {
		return Response{}, errors.New("private upstream detail")
	}}
	setupOperationTraceTest(t, recorder, NewTraceTransport(base))

	p := &Player{errorHandler: func(error) {}}
	p.think(t.Context(), nil, "private prompt", map[string]any{"secret": "value"})

	think := onlyOperationSpan(t, recorder, thinkSpanName)
	assertOperationEnd(t, think, SpanStatusError, outcomeFailure, 1)
	if got, want := think.errors[0].Attributes, map[string]any{
		categoryAttr: categoryTransportFailure,
		reasonAttr:   reasonNetwork,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RecordError attributes = %#v; want %#v", got, want)
	}
	if got := think.ends[0].Attributes[attemptCountAttr]; got != 3 {
		t.Fatalf("attempt count = %v; want 3", got)
	}
	clients := recorder.spansNamed(interactClientSpanName)
	if len(clients) != 3 {
		t.Fatalf("client spans = %d; want 3", len(clients))
	}
	for _, client := range clients {
		assertOperationEnd(t, client, SpanStatusError, "", 0)
	}
	if traceContainsValue(recorder, "private upstream detail") || traceContainsValue(recorder, "private prompt") {
		t.Fatal("trace must not contain transport errors, prompts, or request context")
	}
}

func TestThinkTraceExpectedStopsDoNotRecordError(t *testing.T) {
	tests := []struct {
		name    string
		ctx     func(*testing.T) context.Context
		err     error
		outcome string
		calls   int
	}{
		{
			name:    "Quota",
			ctx:     func(t *testing.T) context.Context { return t.Context() },
			err:     &QuotaExceededError{Err: errors.New("quota")},
			outcome: outcomeQuotaExhausted,
			calls:   1,
		},
		{
			name:    "RateLimited",
			ctx:     func(t *testing.T) context.Context { return t.Context() },
			err:     &TooManyRequestsError{Err: errors.New("429")},
			outcome: outcomeRateLimited,
			calls:   3,
		},
		{
			name: "Cancelled",
			ctx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
			outcome: outcomeCancelled,
			calls:   0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &operationTraceRecorder{}
			calls := 0
			base := &mockTransport{InteractFunc: func(context.Context, Request) (Response, error) {
				calls++
				return Response{}, tt.err
			}}
			setupOperationTraceTest(t, recorder, NewTraceTransport(base))
			p := &Player{errorHandler: func(error) {}}
			p.think(tt.ctx(t), nil, "message", nil)

			if calls != tt.calls {
				t.Fatalf("transport calls = %d; want %d", calls, tt.calls)
			}
			think := onlyOperationSpan(t, recorder, thinkSpanName)
			assertOperationEnd(t, think, SpanStatusUnset, tt.outcome, 0)
		})
	}
}

func TestThinkTraceSnapshotsTracerAndFailsOpen(t *testing.T) {
	t.Run("Snapshot", func(t *testing.T) {
		type stopCmd struct{}
		first := &operationTraceRecorder{}
		second := &operationTraceRecorder{}
		base := &mockTransport{InteractFunc: func(context.Context, Request) (Response, error) {
			SetDefaultTracer(second)
			return Response{CommandName: reflect.TypeOf(stopCmd{}).Name()}, nil
		}}
		setupOperationTraceTest(t, first, NewTraceTransport(base))
		p := &Player{errorHandler: func(error) {}}
		PlayerOnCmd_(p, stopCmd{}, func(stopCmd) error { return Break })
		p.think(t.Context(), nil, "message", nil)

		if got := len(first.spansNamed(thinkSpanName)); got != 1 {
			t.Fatalf("first tracer Think spans = %d; want 1", got)
		}
		if got := len(first.spansNamed(interactClientSpanName)); got != 1 {
			t.Fatalf("first tracer client spans = %d; want 1", got)
		}
		if got := len(first.spansNamed(commandSpanName)); got != 1 {
			t.Fatalf("first tracer command spans = %d; want 1", got)
		}
		if got := len(second.spansNamed(thinkSpanName)) + len(second.spansNamed(interactClientSpanName)) + len(second.spansNamed(commandSpanName)); got != 0 {
			t.Fatalf("new default tracer spans = %d; want 0", got)
		}
	})

	t.Run("StartFailure", func(t *testing.T) {
		type stopCmd struct{}
		recorder := &operationTraceRecorder{startErr: errors.New("trace unavailable")}
		calls := 0
		base := &mockTransport{InteractFunc: func(context.Context, Request) (Response, error) {
			calls++
			return Response{CommandName: reflect.TypeOf(stopCmd{}).Name()}, nil
		}}
		setupOperationTraceTest(t, recorder, NewTraceTransport(base))
		p := &Player{errorHandler: func(error) {}}
		PlayerOnCmd_(p, stopCmd{}, func(stopCmd) error { return Break })
		p.think(t.Context(), nil, "message", nil)

		if calls != 1 {
			t.Fatalf("transport calls = %d; want 1", calls)
		}
		if recorder.starts != 1 {
			t.Fatalf("tracer starts = %d; want only the failed root start", recorder.starts)
		}
	})
}

func TestArchiveTraceFinalFailureRecordsOnce(t *testing.T) {
	recorder := &operationTraceRecorder{}
	base := &mockTransport{ArchiveFunc: func(context.Context, []Turn, string) (ArchivedHistory, error) {
		return ArchivedHistory{}, errors.New("private archive detail")
	}}
	setupOperationTraceTest(t, recorder, NewTraceTransport(base))

	playerReadyToArchive().manageHistory(t.Context())

	archive := onlyOperationSpan(t, recorder, archiveSpanName)
	assertOperationEnd(t, archive, SpanStatusError, outcomeFailure, 1)
	if got, want := archive.errors[0].Attributes, map[string]any{
		categoryAttr: categoryArchiveFailure,
		reasonAttr:   reasonRetriesExhausted,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RecordError attributes = %#v; want %#v", got, want)
	}
	clients := recorder.spansNamed(archiveClientSpanName)
	if len(clients) != 3 {
		t.Fatalf("archive client spans = %d; want 3", len(clients))
	}
	if traceContainsValue(recorder, "private archive detail") {
		t.Fatal("trace must not contain archive error text")
	}
}

func onlyOperationSpan(t *testing.T, recorder *operationTraceRecorder, name string) *operationRecordedSpan {
	t.Helper()
	spans := recorder.spansNamed(name)
	if len(spans) != 1 {
		t.Fatalf("%s spans = %d; want 1", name, len(spans))
	}
	return spans[0]
}

func assertOperationEnd(t *testing.T, span *operationRecordedSpan, status SpanStatus, outcome string, errorCount int) {
	t.Helper()
	if len(span.ends) != 1 {
		t.Fatalf("%s End calls = %d; want 1", span.info.Name, len(span.ends))
	}
	if got := span.ends[0].Status; got != status {
		t.Fatalf("%s status = %q; want %q", span.info.Name, got, status)
	}
	if outcome != "" && span.ends[0].Attributes[outcomeAttr] != outcome {
		t.Fatalf("%s outcome = %v; want %s", span.info.Name, span.ends[0].Attributes[outcomeAttr], outcome)
	}
	if got := len(span.errors); got != errorCount {
		t.Fatalf("%s RecordError calls = %d; want %d", span.info.Name, got, errorCount)
	}
}

func traceContainsValue(recorder *operationTraceRecorder, forbidden string) bool {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	for _, span := range recorder.spans {
		for _, attributes := range []map[string]any{span.info.Attributes} {
			for _, value := range attributes {
				if value == forbidden {
					return true
				}
			}
		}
		for _, recorded := range span.errors {
			for _, value := range recorded.Attributes {
				if value == forbidden {
					return true
				}
			}
		}
		for _, end := range span.ends {
			for _, value := range end.Attributes {
				if value == forbidden {
					return true
				}
			}
		}
	}
	return false
}
