//go:build js && wasm

// Package wasmtrans provides a Transport implementation for AI interactions
// within a WebAssembly (Wasm) environment, typically running in a browser.
package wasmtrans

import (
	"context"
	"encoding/json"
	"fmt"
	"syscall/js"
	"time"

	"github.com/goplus/builder/tools/ai"
)

// wasmTransport implements [ai.Transport] using JavaScript's fetch API.
type wasmTransport struct {
	// endpoint is the URL for the AI interaction API.
	endpoint string

	// tokenProvider is a function that returns the auth token (without "Bearer ").
	// It is called before each request. If it returns "", no auth header is sent.
	tokenProvider func() string
}

// Option is a function type for configuring the [wasmTransport].
type Option func(*wasmTransport)

// WithEndpoint sets a custom endpoint for the AI interaction API.
func WithEndpoint(endpoint string) Option {
	return func(t *wasmTransport) {
		t.endpoint = endpoint
	}
}

// WithTokenProvider sets a function that provides the Bearer token for
// Authorization. The provider function will be called before each request to
// get the current token. If the provider returns an empty string, no
// Authorization header will be sent.
func WithTokenProvider(provider func() string) Option {
	return func(t *wasmTransport) {
		t.tokenProvider = provider
	}
}

// New creates a new [ai.Transport] suitable for Wasm environments. It uses
// JavaScript interop (syscall/js) to make network requests. By default, it uses
// "/api/ai-interaction" endpoint and sends no Authorization token.
func New(opts ...Option) ai.Transport {
	t := &wasmTransport{
		endpoint:      "/api/ai-interaction",
		tokenProvider: func() string { return "" },
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// Interact implements [ai.Transport].
func (t *wasmTransport) Interact(ctx context.Context, req ai.Request) (ai.Response, error) {
	reqBody, err := json.Marshal(req)
	if err != nil {
		return ai.Response{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	var resp ai.Response
	if err := t.fetchAndParse(ctx, "/turns", reqBody, &resp); err != nil {
		return ai.Response{}, err
	}
	return resp, nil
}

// Archive implements [ai.Transport].
func (t *wasmTransport) Archive(ctx context.Context, turns []ai.Turn, existingArchive string) (ai.ArchivedHistory, error) {
	reqBody, err := json.Marshal(map[string]any{
		"turns":           turns,
		"existingArchive": existingArchive,
	})
	if err != nil {
		return ai.ArchivedHistory{}, fmt.Errorf("failed to marshal archive request: %w", err)
	}

	var resp ai.ArchivedHistory
	if err := t.fetchAndParse(ctx, "/archives", reqBody, &resp); err != nil {
		return ai.ArchivedHistory{}, err
	}
	return resp, nil
}

// buildHeaders creates request headers with proper authentication. The parent
// page's fetchAI bridge creates the client span and adds trace headers.
func (t *wasmTransport) buildHeaders() map[string]any {
	headers := map[string]any{
		"Content-Type": "application/json",
	}
	if t.tokenProvider != nil {
		if token := t.tokenProvider(); token != "" {
			headers["Authorization"] = "Bearer " + token
		}
	}
	return headers
}

// fetchAndParse performs a fetch request and parses the JSON response into the target.
func (t *wasmTransport) fetchAndParse(ctx context.Context, path string, body []byte, result any) error {
	headers := t.buildHeaders()

	// Local AbortController covers the no-bridge native fetch path, where
	// AbortAI is a no-op. When the bridge exists, AfterFunc still aborts
	// this signal and also asks the parent page to abort.
	controller := js.Global().Get("AbortController").New()
	stopAbort := context.AfterFunc(ctx, func() {
		controller.Call("abort")
		ai.AbortAI(ctx)
	})
	defer stopAbort()

	jsResp, err := awaitPromise(ctx, ai.FetchAI(ctx, t.endpoint+path, map[string]any{
		"method":  "POST",
		"headers": headers,
		"body":    string(body),
		"signal":  controller.Get("signal"),
	}))
	if err != nil {
		return ai.AnnotateTransportError(ctx, fmt.Errorf("failed to fetch: %w", err))
	}

	if !jsResp.Get("ok").Bool() {
		status := jsResp.Get("status").Int()
		statusText := jsResp.Get("statusText").String()
		retryAfter := retryAfterFromResponse(jsResp)

		bodyText, bodyErr := responseBody(ctx, jsResp)
		if bodyErr != nil {
			fallback := fmt.Errorf("failed to fetch with status %d %s (and failed to read error body: %w)", status, statusText, bodyErr)
			return ai.AnnotateTransportError(ctx, ai.ErrorFromHTTPResponse(status, retryAfter, "", fallback))
		}
		fallback := fmt.Errorf("failed to fetch with status %d %s: %s", status, statusText, bodyText)
		return ai.AnnotateTransportError(ctx, ai.ErrorFromHTTPResponse(status, retryAfter, bodyText, fallback))
	}

	bodyText, err := responseBody(ctx, jsResp)
	if err != nil {
		return ai.AnnotateTransportError(ctx, fmt.Errorf("failed to process json response: %w", err))
	}
	if err := json.Unmarshal([]byte(bodyText), result); err != nil {
		return fmt.Errorf("failed to unmarshal response json: %w", err)
	}
	return nil
}

// responseBody accepts both the bridge's normalized response ({body: string})
// and the native Response returned by the no-bridge fallback. In either case
// the body is consumed at most once.
func responseBody(ctx context.Context, response js.Value) (string, error) {
	body := response.Get("body")
	if body.Type() == js.TypeString {
		return body.String(), nil
	}
	bodyText, err := awaitPromise(ctx, response.Call("text"))
	if err != nil {
		return "", err
	}
	return bodyText.String(), nil
}

// retryAfterFromResponse reads the bridge's retryAfter field, or the native
// Response Retry-After header used by the no-bridge fetch fallback.
func retryAfterFromResponse(jsResp js.Value) time.Duration {
	if value := jsResp.Get("retryAfter"); value.Type() == js.TypeString {
		return ai.RetryAfterFromHeader(value.String())
	}
	headers := jsResp.Get("headers")
	if !headers.Truthy() {
		return 0
	}
	got := headers.Call("get", "Retry-After")
	if got.Type() != js.TypeString {
		return 0
	}
	return ai.RetryAfterFromHeader(got.String())
}
