package ai

import (
	"context"
	"testing"
	"time"
)

func TestTraceHeadersFromContextInheritsToChild(t *testing.T) {
	ctx := withSentryTrace(context.Background(), "id-1", "trace-1", "baggage-1")
	child, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	trace, baggage := TraceHeadersFromContext(child)
	if trace != "trace-1" || baggage != "baggage-1" {
		t.Fatalf("TraceHeadersFromContext(child) = %q, %q; want trace-1, baggage-1", trace, baggage)
	}
	if got := sentryTraceIDFromContext(child); got != "id-1" {
		t.Fatalf("sentryTraceIDFromContext(child) = %q; want id-1", got)
	}
}

func TestTraceHeadersFromContextEmpty(t *testing.T) {
	trace, baggage := TraceHeadersFromContext(context.Background())
	if trace != "" || baggage != "" {
		t.Fatalf("TraceHeadersFromContext(background) = %q, %q; want empty", trace, baggage)
	}
	if got := sentryTraceIDFromContext(nil); got != "" {
		t.Fatalf("sentryTraceIDFromContext(nil) = %q; want empty", got)
	}
}

func TestTraceHeadersFromContextIsolated(t *testing.T) {
	thinkCtx := withSentryTrace(context.Background(), "think", "think-trace", "think-baggage")
	archiveCtx := withSentryTrace(context.Background(), "archive", "archive-trace", "archive-baggage")

	thinkTrace, thinkBaggage := TraceHeadersFromContext(thinkCtx)
	archiveTrace, archiveBaggage := TraceHeadersFromContext(archiveCtx)
	if thinkTrace != "think-trace" || thinkBaggage != "think-baggage" {
		t.Fatalf("think headers = %q, %q", thinkTrace, thinkBaggage)
	}
	if archiveTrace != "archive-trace" || archiveBaggage != "archive-baggage" {
		t.Fatalf("archive headers = %q, %q", archiveTrace, archiveBaggage)
	}
	if sentryTraceIDFromContext(thinkCtx) == sentryTraceIDFromContext(archiveCtx) {
		t.Fatal("think and archive should not share the same id")
	}
}
