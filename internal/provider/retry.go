package provider

import (
	"context"
	"time"

	hyprovider "github.com/Viking602/venat/provider"
)

// RetryConfig is Azem's single provider-stream retry policy. MaxRetries counts
// reopen attempts after the first physical request; zero disables retries.
type RetryConfig struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
	Observer   hyprovider.RetryObserver
}

// WithRetry wraps one provider driver with pre-emission retry semantics. The
// inner Stream call is one physical request, so metering must wrap the physical
// driver before WithRetry is applied.
func WithRetry(driver hyprovider.Driver, config RetryConfig) hyprovider.Driver {
	if driver == nil || config.MaxRetries <= 0 {
		return driver
	}
	return &retryDriver{inner: driver, config: config}
}

type retryDriver struct {
	inner  hyprovider.Driver
	config RetryConfig
}

func (driver *retryDriver) Metadata() hyprovider.Metadata { return driver.inner.Metadata() }

func (driver *retryDriver) Stream(ctx context.Context, request hyprovider.Request) (hyprovider.Stream, error) {
	return hyprovider.OpenRetryingStream(ctx, func() (hyprovider.Stream, error) {
		return driver.inner.Stream(ctx, request)
	}, hyprovider.StreamRetryOptions{
		Max:      driver.config.MaxRetries,
		Delay:    retryDelay(driver.config.BaseDelay, driver.config.MaxDelay),
		MaxDelay: driver.config.MaxDelay,
		Observer: driver.config.Observer,
	})
}

func retryDelay(base, maximum time.Duration) func(int) time.Duration {
	if base <= 0 {
		return func(int) time.Duration { return 0 }
	}
	const maxDuration = time.Duration(1<<63 - 1)
	return func(attempt int) time.Duration {
		delay := base
		if maximum > 0 && delay >= maximum {
			return maximum
		}
		for remaining := max(attempt-1, 0); remaining > 0; remaining-- {
			if delay > maxDuration/2 {
				if maximum > 0 {
					return maximum
				}
				return maxDuration
			}
			delay *= 2
			if maximum > 0 && delay >= maximum {
				return maximum
			}
		}
		return delay
	}
}
