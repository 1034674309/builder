package ai

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

type sentryTraceContextKey struct{}

type sentryTraceContext struct {
	id      string
	trace   string
	baggage string
}

var sentryTraceSeq atomic.Uint64

func newSentryTraceID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixMilli(), sentryTraceSeq.Add(1))
}

func withSentryTrace(ctx context.Context, id, trace, baggage string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if id == "" && trace == "" && baggage == "" {
		return ctx
	}
	return context.WithValue(ctx, sentryTraceContextKey{}, sentryTraceContext{
		id:      id,
		trace:   trace,
		baggage: baggage,
	})
}

func sentryTraceFromContext(ctx context.Context) (sentryTraceContext, bool) {
	if ctx == nil {
		return sentryTraceContext{}, false
	}
	value, ok := ctx.Value(sentryTraceContextKey{}).(sentryTraceContext)
	return value, ok
}

// TraceHeadersFromContext returns the Sentry-Trace and Baggage values bound to
// ctx by startThink or startArchive. Missing values yield empty strings.
func TraceHeadersFromContext(ctx context.Context) (string, string) {
	value, ok := sentryTraceFromContext(ctx)
	if !ok {
		return "", ""
	}
	return value.trace, value.baggage
}

func sentryTraceIDFromContext(ctx context.Context) string {
	value, ok := sentryTraceFromContext(ctx)
	if !ok {
		return ""
	}
	return value.id
}
