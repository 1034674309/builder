package main

import (
	"context"
	"errors"
	"time"

	"github.com/goplus/builder/tools/ai"
	"github.com/goplus/builder/tools/ispx/internal/rpc"
)

const (
	traceSpanStartMethod = "trace/span.start"
	traceSpanErrorMethod = "trace/span.error"
	traceSpanEndMethod   = "trace/span.end"
	traceStartTimeout    = 500 * time.Millisecond
)

type rpcTracer struct {
	client       *rpc.Client
	startTimeout time.Duration
}

type rpcSpanContextKey struct{}

type traceSpanStartParams struct {
	ParentSpanID string         `json:"parentSpanId,omitempty"`
	Name         string         `json:"name"`
	Operation    string         `json:"operation"`
	Attributes   map[string]any `json:"attributes,omitempty"`
}

type traceSpanStartResult struct {
	SpanID  string            `json:"spanId"`
	Headers map[string]string `json:"headers,omitempty"`
}

type rpcSpan struct {
	client *rpc.Client
	spanID string
}

type traceSpanErrorParams struct {
	SpanID     string         `json:"spanId"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type traceSpanEndParams struct {
	SpanID     string         `json:"spanId"`
	Status     ai.SpanStatus  `json:"status,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// NewRPCTracer returns a generic AI tracer backed by the iSPX JSON-RPC client.
// A nil client falls back to the Noop tracer.
func NewRPCTracer(client *rpc.Client) ai.Tracer {
	if client == nil {
		return ai.NewNoopTracer()
	}
	return &rpcTracer{client: client, startTimeout: traceStartTimeout}
}

func (t *rpcTracer) StartSpan(ctx context.Context, info ai.SpanInfo) (context.Context, ai.Span, error) {
	if ctx == nil {
		return nil, nil, errors.New("rpc tracer: nil context")
	}
	callCtx, cancel := context.WithTimeout(ctx, t.startTimeout)
	defer cancel()

	var result traceSpanStartResult
	err := t.client.Call(callCtx, traceSpanStartMethod, traceSpanStartParams{
		ParentSpanID: rpcSpanIDFromContext(ctx),
		Name:         info.Name,
		Operation:    info.Operation,
		Attributes:   info.Attributes,
	}, &result)
	if err != nil {
		return ctx, nil, err
	}
	if result.SpanID == "" {
		return ctx, nil, errors.New("rpc tracer: empty span id")
	}

	spanCtx := context.WithValue(ctx, rpcSpanContextKey{}, result.SpanID)
	spanCtx = ai.WithExtraHeaders(spanCtx, result.Headers)
	return spanCtx, &rpcSpan{client: t.client, spanID: result.SpanID}, nil
}

func rpcSpanIDFromContext(ctx context.Context) string {
	spanID, _ := ctx.Value(rpcSpanContextKey{}).(string)
	return spanID
}

func (s *rpcSpan) RecordError(info ai.ErrorInfo) {
	_ = s.client.Notify(traceSpanErrorMethod, traceSpanErrorParams{
		SpanID:     s.spanID,
		Attributes: info.Attributes,
	})
}

func (s *rpcSpan) End(info ai.SpanEnd) {
	_ = s.client.Notify(traceSpanEndMethod, traceSpanEndParams{
		SpanID:     s.spanID,
		Status:     info.Status,
		Attributes: info.Attributes,
	})
}
