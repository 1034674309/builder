package ai

import "errors"

// Frontend exception category / reason values from SENTRY_PLAN.
// Backend HTTP error category/reason live on http.server, not a frontend attempt span.
const (
	categoryTransportFailure    = "transport_failure"
	categoryModelQualityFailure = "model_quality_failure"
	categoryRuntimeFailure      = "runtime_failure"
	categoryArchiveFailure      = "archive_failure"

	outcomeSuccess        = "success"
	outcomeCancelled      = "cancelled"
	outcomeQuotaExhausted = "quota_exhausted"
	outcomeRateLimited    = "rate_limited"
	outcomeFailure        = "failure"

	reasonInvalidArguments      = "invalid_arguments"
	reasonHandlerPanic          = "handler_panic"
	reasonTimeout               = "timeout"
	reasonNetwork               = "network"
	reasonMissingInitialCommand = "missing_initial_command"
	reasonTurnLimit             = "turn_limit"
	reasonOther                 = "other"
	reasonRetriesExhausted      = "retries_exhausted"
)

// thinkFinish is the terminal state sent once through endThink.
type thinkFinish struct {
	outcome      string
	category     string
	reason       string
	turnCount    int
	attemptCount int
	turnIndex    int
	lastAttempt  int
	userMsg      string
	commandName  string
	commandArgs  map[string]any
	errMessage   string
}

func (f thinkFinish) withCounts(turnCount, attemptCount int) thinkFinish {
	f.turnCount = turnCount
	f.attemptCount = attemptCount
	return f
}

// invalidArgumentsError is returned when command args cannot populate the
// handler struct. Think stops; Sentry reason is invalid_arguments.
type invalidArgumentsError struct {
	command string
	err     error
}

func (e *invalidArgumentsError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *invalidArgumentsError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// handlerPanicError is returned when a command handler panics (other than
// AbortThread). Think stops; Sentry reason is handler_panic.
type handlerPanicError struct {
	command string
	err     error
}

func (e *handlerPanicError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *handlerPanicError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func isInvalidArguments(err error) bool {
	var typed *invalidArgumentsError
	return errors.As(err, &typed)
}

func isHandlerPanic(err error) bool {
	var typed *handlerPanicError
	return errors.As(err, &typed)
}

func commandNameFromError(err error) string {
	var argsErr *invalidArgumentsError
	if errors.As(err, &argsErr) {
		return argsErr.command
	}
	var panicErr *handlerPanicError
	if errors.As(err, &panicErr) {
		return panicErr.command
	}
	return ""
}

func classifyCommandStop(err error) (category, reason string, ok bool) {
	switch {
	case isInvalidArguments(err):
		return categoryModelQualityFailure, reasonInvalidArguments, true
	case isHandlerPanic(err):
		return categoryRuntimeFailure, reasonHandlerPanic, true
	default:
		return "", "", false
	}
}

func isTooManyRequests(err error) bool {
	var typed *TooManyRequestsError
	return errors.As(err, &typed)
}

func classifyTransportStop(err error) thinkFinish {
	switch {
	case isQuotaExceeded(err):
		return thinkFinish{outcome: outcomeQuotaExhausted}
	case isTooManyRequests(err):
		return thinkFinish{outcome: outcomeRateLimited}
	case isTransportTimeout(err):
		return thinkFinish{outcome: outcomeFailure, category: categoryTransportFailure, reason: reasonTimeout}
	default:
		return thinkFinish{outcome: outcomeFailure, category: categoryTransportFailure, reason: reasonNetwork}
	}
}

func classifyCommandStopFinish(err error) thinkFinish {
	category, reason, ok := classifyCommandStop(err)
	if !ok {
		return thinkFinish{outcome: outcomeFailure, category: categoryRuntimeFailure, reason: reasonOther}
	}
	return thinkFinish{outcome: outcomeFailure, category: category, reason: reason}
}
