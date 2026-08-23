//go:build !js || !wasm

package ai

import "context"

// SetGameSessionID is a no-op outside the WASM runner.
func SetGameSessionID(string) {}

type recordedAttempt struct {
	ok        bool
	category  string
	reason    string
	requestID string
}

var (
	lastThinkFinish     thinkFinish
	thinkFinishCount    int
	lastArchiveFinish   archiveFinish
	archiveFinishCount  int
	lastAttempts        []recordedAttempt
	lastArchiveAttempts []recordedAttempt
)

func resetSentryStub() {
	lastThinkFinish = thinkFinish{}
	thinkFinishCount = 0
	lastArchiveFinish = archiveFinish{}
	archiveFinishCount = 0
	lastAttempts = nil
	lastArchiveAttempts = nil
}

func recordAttempt(ok bool, err error) recordedAttempt {
	category, reason, requestID := backendTagsFromError(err)
	return recordedAttempt{ok: ok, category: category, reason: reason, requestID: requestID}
}

func startThink(ctx context.Context) context.Context { return ctx }

func endThink(_ context.Context, finish thinkFinish) {
	thinkFinishCount++
	lastThinkFinish = finish
}

func startAttempt(context.Context, int, int) {}

func endAttempt(_ context.Context, ok bool, err error) {
	lastAttempts = append(lastAttempts, recordAttempt(ok, err))
}

func startCommand(context.Context, int, string) {}

func endCommand(context.Context, bool, string) {}

func startArchive(ctx context.Context) context.Context { return ctx }

func endArchive(_ context.Context, finish archiveFinish) {
	archiveFinishCount++
	lastArchiveFinish = finish
}

func startArchiveAttempt(context.Context, int) {}

func endArchiveAttempt(_ context.Context, ok bool, err error) {
	lastArchiveAttempts = append(lastArchiveAttempts, recordAttempt(ok, err))
}
