//go:build !js || !wasm

package ai

import "context"

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

func observeThinkFinish(finish thinkFinish) {
	thinkFinishCount++
	lastThinkFinish = finish
}

func observeArchiveFinish(finish archiveFinish) {
	archiveFinishCount++
	lastArchiveFinish = finish
}

func startAttempt(ctx context.Context) context.Context { return ctx }

func startArchiveAttempt(ctx context.Context) context.Context { return ctx }
