// Package retry 提供带 context 的指数退避重试。
package retry

import (
	"context"
	"errors"
	"net"
	"time"
)

const (
	defaultMaxAttempts     = 3
	defaultInitialInterval = 200 * time.Millisecond
	defaultMaxInterval     = 2 * time.Second
	defaultMultiplier      = 2
)

// Policy 描述指数重试边界。MaxAttempts 包含首次调用。
type Policy struct {
	MaxAttempts     int
	InitialInterval time.Duration
	MaxInterval     time.Duration
}

// HTTPStatusError 暴露可供重试判断的 HTTP 状态码。
type HTTPStatusError interface {
	HTTPStatus() int
}

type statusError struct {
	status int
	err    error
}

// WithStatus 把 HTTP 状态码附加到错误上，便于识别 429/5xx。
func WithStatus(status int, err error) error {
	if err == nil {
		return nil
	}
	return &statusError{status: status, err: err}
}

func (err *statusError) Error() string {
	return err.err.Error()
}

func (err *statusError) Unwrap() error {
	return err.err
}

func (err *statusError) HTTPStatus() int {
	return err.status
}

// Normalize 填充默认重试参数。
func Normalize(policy Policy) Policy {
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = defaultMaxAttempts
	}
	if policy.InitialInterval <= 0 {
		policy.InitialInterval = defaultInitialInterval
	}
	if policy.MaxInterval <= 0 {
		policy.MaxInterval = defaultMaxInterval
	}
	if policy.MaxInterval < policy.InitialInterval {
		policy.MaxInterval = policy.InitialInterval
	}
	return policy
}

// Do 按指数间隔重试可恢复错误，直到成功、不可重试或次数用尽。
func Do(ctx context.Context, policy Policy, operation func() error) error {
	policy = Normalize(policy)
	interval := policy.InitialInterval
	var last error

	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		last = operation()
		if last == nil {
			return nil
		}
		if attempt == policy.MaxAttempts || !IsRetryable(last) {
			return last
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}

		next := interval * defaultMultiplier
		if next > policy.MaxInterval || next < interval {
			next = policy.MaxInterval
		}
		interval = next
	}

	return last
}

// IsRetryable 判断错误是否值得指数重试。
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var statusErr HTTPStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.HTTPStatus() {
		case 408, 429, 500, 502, 503, 504:
			return true
		default:
			return false
		}
	}

	var netErr net.Error
	return errors.As(err, &netErr)
}
