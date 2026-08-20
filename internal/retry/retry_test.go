package retry

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestDoRetriesRetryableErrorThenSucceeds(t *testing.T) {
	attempts := 0
	err := Do(context.Background(), Policy{
		MaxAttempts:     3,
		InitialInterval: time.Nanosecond,
		MaxInterval:     time.Millisecond,
	}, func() error {
		attempts++
		if attempts < 3 {
			return WithStatus(http.StatusBadGateway, errors.New("upstream"))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestDoDoesNotRetryPermanentError(t *testing.T) {
	attempts := 0
	want := WithStatus(http.StatusUnauthorized, errors.New("denied"))
	err := Do(context.Background(), Policy{
		MaxAttempts:     3,
		InitialInterval: time.Nanosecond,
		MaxInterval:     time.Millisecond,
	}, func() error {
		attempts++
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("Do() error = %v, want %v", err, want)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestDoHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	attempts := 0
	err := Do(ctx, Policy{MaxAttempts: 3}, func() error {
		attempts++
		return WithStatus(http.StatusBadGateway, errors.New("upstream"))
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do() error = %v, want context.Canceled", err)
	}
	if attempts != 0 {
		t.Fatalf("attempts = %d, want 0", attempts)
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil"},
		{name: "canceled", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
		{name: "unauthorized", err: WithStatus(http.StatusUnauthorized, errors.New("denied"))},
		{name: "too many requests", err: WithStatus(http.StatusTooManyRequests, errors.New("slow")), want: true},
		{name: "bad gateway", err: WithStatus(http.StatusBadGateway, errors.New("upstream")), want: true},
		{name: "timeout", err: net.Error(timeoutError{}), want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsRetryable(test.err); got != test.want {
				t.Fatalf("IsRetryable() = %t, want %t", got, test.want)
			}
		})
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
