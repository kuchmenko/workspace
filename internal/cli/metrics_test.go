package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/kuchmenko/workspace/internal/metrics"
)

func TestCommandOutcome(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want metrics.Outcome
	}{
		{name: "success", want: metrics.Success},
		{name: "failure", err: errors.New("failed"), want: metrics.Failure},
		{name: "context cancellation", err: context.Canceled, want: metrics.Canceled},
		{name: "deadline", err: context.DeadlineExceeded, want: metrics.Canceled},
		{name: "cancellation exit", err: ExitError{Code: 130}, want: metrics.Canceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := commandOutcome(tt.err); got != tt.want {
				t.Fatalf("commandOutcome(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
