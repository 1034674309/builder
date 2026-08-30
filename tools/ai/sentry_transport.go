package ai

import "context"

// sentryTransport adds per-request Sentry bookkeeping around another
// Transport. It keeps no request state of its own, so one wrapped Transport can
// safely serve concurrent AI interactions.
type sentryTransport struct {
	next Transport
}

// NewSentryTransport wraps t with the Sentry bookkeeping shared by Interact
// and Archive calls. A nil transport stays nil so SetDefaultTransport keeps its
// existing reset behavior.
func NewSentryTransport(t Transport) Transport {
	if t == nil {
		return nil
	}
	return &sentryTransport{next: t}
}

func (t *sentryTransport) Interact(ctx context.Context, req Request) (Response, error) {
	ctx = startAttempt(ctx)
	resp, err := t.next.Interact(ctx, req)
	return resp, AnnotateTransportError(ctx, err)
}

func (t *sentryTransport) Archive(ctx context.Context, turns []Turn, existingArchive string) (ArchivedHistory, error) {
	ctx = startArchiveAttempt(ctx)
	resp, err := t.next.Archive(ctx, turns, existingArchive)
	return resp, AnnotateTransportError(ctx, err)
}
