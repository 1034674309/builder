package ai

import (
	"errors"
	"testing"
)

func TestClassifyTransportStop(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantOutcome  string
		wantCategory string
		wantReason   string
	}{
		{
			name:        "Quota",
			err:         &QuotaExceededError{Err: errors.New("quota exceeded")},
			wantOutcome: outcomeQuotaExhausted,
		},
		{
			name:        "RateLimited",
			err:         &TooManyRequestsError{Err: errors.New("too many requests")},
			wantOutcome: outcomeRateLimited,
		},
		{
			name:         "Timeout",
			err:          &TimeoutError{Err: errors.New("deadline")},
			wantOutcome:  outcomeFailure,
			wantCategory: categoryTransportFailure,
			wantReason:   reasonTimeout,
		},
		{
			name:         "Network",
			err:          errors.New("failed to fetch"),
			wantOutcome:  outcomeFailure,
			wantCategory: categoryTransportFailure,
			wantReason:   reasonNetwork,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyTransportStop(tt.err)
			if got.outcome != tt.wantOutcome || got.category != tt.wantCategory || got.reason != tt.wantReason {
				t.Fatalf("got %+v, want outcome=%s category=%s reason=%s", got, tt.wantOutcome, tt.wantCategory, tt.wantReason)
			}
		})
	}
}
