//go:build !js || !wasm

package ai

import "context"

// SetGameSessionID is a no-op outside the WASM runner.
func SetGameSessionID(string) {}

var (
	lastThinkFinish    thinkFinish
	thinkFinishCount   int
	lastArchiveFinish  archiveFinish
	archiveFinishCount int
)

func resetSentryStub() {
	lastThinkFinish = thinkFinish{}
	thinkFinishCount = 0
	lastArchiveFinish = archiveFinish{}
	archiveFinishCount = 0
}

func startThink(ctx context.Context) context.Context { return ctx }

func endThink(_ context.Context, finish thinkFinish) {
	thinkFinishCount++
	lastThinkFinish = finish
}

func startAttempt(ctx context.Context) context.Context { return ctx }

func startCommand(context.Context, int, string) {}

func endCommand(context.Context, bool, string) {}

func startArchive(ctx context.Context) context.Context { return ctx }

func endArchive(_ context.Context, finish archiveFinish) {
	archiveFinishCount++
	lastArchiveFinish = finish
}

func startArchiveAttempt(ctx context.Context) context.Context { return ctx }
