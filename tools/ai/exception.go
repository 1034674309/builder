package ai

import (
	"encoding/json"
	"unicode/utf8"
)

const (
	maxExceptionErrorBytes = 1024
	maxCommandDetailBytes  = 4096
)

func (f thinkFinish) shouldCaptureException() bool {
	return f.outcome == outcomeFailure
}

func (f thinkFinish) withException(userMsg, errMessage string, turnIndex, lastAttempt int, commandName string, commandArgs map[string]any) thinkFinish {
	f.userMsg = userMsg
	f.errMessage = errMessage
	f.turnIndex = turnIndex
	f.lastAttempt = lastAttempt
	f.commandName = commandName
	f.commandArgs = commandArgs
	return f
}

func (f thinkFinish) bridgeException(gameSessionID string) map[string]any {
	if !f.shouldCaptureException() {
		return nil
	}
	commandName, argsJSON := commandDetail(f.commandName, f.commandArgs)
	return map[string]any{
		"message":       truncateBytes(f.errMessage, maxExceptionErrorBytes),
		"userMsg":       f.userMsg,
		"commandName":   commandName,
		"commandArgs":   argsJSON,
		"turnIndex":     f.turnIndex,
		"attempt":       f.lastAttempt,
		"category":      f.category,
		"reason":        f.reason,
		"gameSessionId": gameSessionID,
	}
}

func truncateBytes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func commandDetail(name string, args map[string]any) (string, string) {
	var argsJSON string
	if args != nil {
		raw, err := json.Marshal(args)
		if err != nil {
			argsJSON = "{}"
		} else {
			argsJSON = string(raw)
		}
	}
	if len(name)+len(argsJSON) <= maxCommandDetailBytes {
		return name, argsJSON
	}
	room := maxCommandDetailBytes - len(name)
	if room < 0 {
		return truncateBytes(name, maxCommandDetailBytes), ""
	}
	return name, truncateBytes(argsJSON, room)
}

// archiveFinish is the terminal state sent once through endArchive.
// Archive has no outcome field.
type archiveFinish struct {
	ok               bool
	captureException bool
	attemptCount     int
	errMessage       string
}

func (f archiveFinish) shouldCaptureException() bool {
	return f.captureException
}

func (f archiveFinish) bridgeException(gameSessionID string) map[string]any {
	if !f.shouldCaptureException() {
		return nil
	}
	errText := truncateBytes(f.errMessage, maxExceptionErrorBytes)
	return map[string]any{
		"message":       errText,
		"attemptCount":  f.attemptCount,
		"category":      categoryArchiveFailure,
		"reason":        reasonRetriesExhausted,
		"gameSessionId": gameSessionID,
	}
}
