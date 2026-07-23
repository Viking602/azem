package provider

import "time"

// RetryProgress describes a provider connection retry before its backoff starts.
type RetryProgress struct {
	Attempt int
	Max     int
	Delay   time.Duration
	Cause   error
}

// RetryObserver delivers retry progress to the application event stream.
// Returning an error stops retrying instead of allowing retries to become silent.
type RetryObserver func(RetryProgress) error

// RetryObservable is implemented by any provider driver that retries connections.
type RetryObservable interface {
	SetRetryObserver(RetryObserver)
}
