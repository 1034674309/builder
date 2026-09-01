package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/goplus/builder/tools/ai"
	"github.com/goplus/builder/tools/ispx/internal/rpc"
	"github.com/goplus/xgo/x/jsonrpc2"
)

type traceContextKey struct{}

func TestRPCTracerStartsSpanWithPropagationHeaders(t *testing.T) {
	var messages []*jsonrpc2.Request
	var client *rpc.Client
	client = rpc.NewClient(rpc.MessageSenderFunc(func(message jsonrpc2.Message) error {
		request := message.(*jsonrpc2.Request)
		messages = append(messages, request)
		if request.Method != traceSpanStartMethod {
			return nil
		}
		response, err := jsonrpc2.NewResponse(request.ID, traceSpanStartResult{
			SpanID: "span-1",
			Headers: map[string]string{
				"X-Trace-Test": "trace-value",
				"X-Trace-Meta": "meta-value",
			},
		}, nil)
		if err != nil {
			t.Fatalf("NewResponse() error = %v", err)
		}
		return client.HandleMessage(response)
	}))
	tracer := NewRPCTracer(client)
	ctx := context.WithValue(t.Context(), traceContextKey{}, "parent")

	spanCtx, span, err := tracer.StartSpan(ctx, ai.SpanInfo{
		Name:      "POST /example",
		Operation: "http.client",
		Attributes: map[string]any{
			"request.kind": "example",
		},
	})
	if err != nil {
		t.Fatalf("StartSpan() error = %v", err)
	}
	if spanCtx.Err() != nil {
		t.Fatalf("derived context error = %v; want nil", spanCtx.Err())
	}
	if got := spanCtx.Value(traceContextKey{}); got != "parent" {
		t.Fatalf("derived context value = %v; want parent", got)
	}
	headers := ai.ExtraHeadersFromContext(spanCtx)
	if got := headers["X-Trace-Test"]; got != "trace-value" {
		t.Fatalf("X-Trace-Test = %q; want trace-value", got)
	}
	if got := headers["X-Trace-Meta"]; got != "meta-value" {
		t.Fatalf("X-Trace-Meta = %q; want meta-value", got)
	}
	if got := ai.ExtraHeadersFromContext(ctx); got != nil {
		t.Fatalf("original context headers = %v; want nil", got)
	}

	if len(messages) != 1 {
		t.Fatalf("messages after StartSpan() = %d; want 1", len(messages))
	}
	var start traceSpanStartParams
	if err := json.Unmarshal(messages[0].Params, &start); err != nil {
		t.Fatalf("decode start params: %v", err)
	}
	if start.Name != "POST /example" || start.Operation != "http.client" {
		t.Fatalf("start params = %+v; want POST /example http.client", start)
	}

	span.RecordError(ai.ErrorInfo{Attributes: map[string]any{
		"category": "transport_failure",
		"reason":   "network",
	}})
	span.End(ai.SpanEnd{Status: ai.SpanStatusError})
	if len(messages) != 3 {
		t.Fatalf("messages after span finish = %d; want 3", len(messages))
	}
	if messages[1].Method != traceSpanErrorMethod {
		t.Fatalf("error method = %q; want %q", messages[1].Method, traceSpanErrorMethod)
	}
	var recordedError traceSpanErrorParams
	if err := json.Unmarshal(messages[1].Params, &recordedError); err != nil {
		t.Fatalf("decode error params: %v", err)
	}
	if recordedError.SpanID != "span-1" || recordedError.Attributes["reason"] != "network" {
		t.Fatalf("error params = %+v; want span-1/network", recordedError)
	}
	if messages[2].Method != traceSpanEndMethod {
		t.Fatalf("end method = %q; want %q", messages[2].Method, traceSpanEndMethod)
	}
	var ended traceSpanEndParams
	if err := json.Unmarshal(messages[2].Params, &ended); err != nil {
		t.Fatalf("decode end params: %v", err)
	}
	if ended.SpanID != "span-1" || ended.Status != ai.SpanStatusError {
		t.Fatalf("end params = %+v; want span-1/error", ended)
	}
}

func TestRPCTracerStartTimeoutUsesOnlyCallContext(t *testing.T) {
	client := rpc.NewClient(rpc.MessageSenderFunc(func(jsonrpc2.Message) error { return nil }))
	tracer := &rpcTracer{client: client, startTimeout: time.Nanosecond}
	ctx := t.Context()

	spanCtx, span, err := tracer.StartSpan(ctx, ai.SpanInfo{Name: "operation"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StartSpan() error = %v; want context.DeadlineExceeded", err)
	}
	if spanCtx != ctx {
		t.Fatalf("StartSpan() context changed on failure; want original context")
	}
	if span != nil {
		t.Fatalf("StartSpan() span = %T; want nil", span)
	}
	if ctx.Err() != nil {
		t.Fatalf("original context error = %v; want nil", ctx.Err())
	}
}

func TestRPCTracerCarriesParentSpanThroughContext(t *testing.T) {
	var starts []traceSpanStartParams
	var client *rpc.Client
	client = rpc.NewClient(rpc.MessageSenderFunc(func(message jsonrpc2.Message) error {
		request := message.(*jsonrpc2.Request)
		var params traceSpanStartParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			t.Fatalf("decode start params: %v", err)
		}
		starts = append(starts, params)
		response, err := jsonrpc2.NewResponse(request.ID, traceSpanStartResult{
			SpanID: "span-" + params.Name,
		}, nil)
		if err != nil {
			t.Fatalf("NewResponse() error = %v", err)
		}
		return client.HandleMessage(response)
	}))
	tracer := NewRPCTracer(client)

	parentCtx, _, err := tracer.StartSpan(t.Context(), ai.SpanInfo{Name: "parent"})
	if err != nil {
		t.Fatalf("StartSpan(parent) error = %v", err)
	}
	if _, _, err := tracer.StartSpan(parentCtx, ai.SpanInfo{Name: "child"}); err != nil {
		t.Fatalf("StartSpan(child) error = %v", err)
	}
	if len(starts) != 2 {
		t.Fatalf("start calls = %d; want 2", len(starts))
	}
	if starts[0].ParentSpanID != "" {
		t.Fatalf("root parent span id = %q; want empty", starts[0].ParentSpanID)
	}
	if starts[1].ParentSpanID != "span-parent" {
		t.Fatalf("child parent span id = %q; want span-parent", starts[1].ParentSpanID)
	}
}

func TestNewRPCTracerNilReturnsNoop(t *testing.T) {
	ctx := t.Context()
	gotCtx, span, err := NewRPCTracer(nil).StartSpan(ctx, ai.SpanInfo{})
	if err != nil {
		t.Fatalf("StartSpan() error = %v; want nil", err)
	}
	if gotCtx != ctx {
		t.Fatalf("StartSpan() context changed; want original context")
	}
	span.End(ai.SpanEnd{})
}
