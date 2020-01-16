package netutil

import (
	"context"
	"errors"
	"time"

	"github.com/sirupsen/logrus"
)

// Package errors
var (
	ErrMaximumRetriesReached = errors.New("maximum retries attempted without success")
)

// RetryFunc is a function used as argument of (*Retrier).Do(), which will retry on error unless it is whitelisted
type RetryFunc func() error

// Retrier holds a configuration for how retries should be performed
type Retrier struct {
	expBackoff   time.Duration // multiplied on every retry by a expFactor
	expFactor    uint32        // multiplier for the backoff duration that is applied on every retry
	times        uint32        // number of times that the given function is going to be retried until success, if 0 it will be retried forever until success
	errWhitelist map[error]struct{}
	log          logrus.FieldLogger
}

// NewRetrier returns a retrier that is ready to call Do() method
func NewRetrier(log logrus.FieldLogger, backOff time.Duration, times, factor uint32) *Retrier {
	return &Retrier{
		expBackoff:   backOff,
		times:        times,
		expFactor:    factor,
		errWhitelist: make(map[error]struct{}),
		log:          log,
	}
}

// Default values for retrier.
const (
	DefaultBackOff = 100 * time.Millisecond
	DefaultTries   = 0
	DefaultFactor  = 2
)

// NewDefaultRetrier creates a retrier with default values.
func NewDefaultRetrier(log logrus.FieldLogger) *Retrier {
	return NewRetrier(log, DefaultBackOff, DefaultTries, DefaultFactor)
}

// WithErrWhitelist sets a list of errors into the retrier, if the RetryFunc provided to Do() fails with one of them it will return inmediatelly with such error. Calling
// this function is not thread-safe, and is advised to only use it when initializing the Retrier
func (r *Retrier) WithErrWhitelist(errors ...error) *Retrier {
	m := make(map[error]struct{})
	for _, err := range errors {
		m[err] = struct{}{}
	}

	r.errWhitelist = m
	return r
}

// Do takes a RetryFunc and attempts to execute it, if it fails with an error it will be retried a maximum of given times with an expBackoff, until it returns
// nil or an error that is whitelisted
func (r *Retrier) Do(ctx context.Context, f RetryFunc) error {
	if r.times == 0 {
		return r.retryUntilSuccess(ctx, f)
	}

	return r.retryNTimes(f)
}

func (r *Retrier) retryNTimes(f RetryFunc) error {
	currentBackoff := r.expBackoff

	for i := uint32(0); i < r.times; i++ {
		err := f()
		if err != nil {
			if r.isWhitelisted(err) {
				return err
			}

			r.log.WithError(err).Warn()
			currentBackoff *= time.Duration(r.expFactor)
			time.Sleep(currentBackoff)
			continue
		}

		return nil
	}

	return ErrMaximumRetriesReached
}

func (r *Retrier) retryUntilSuccess(ctx context.Context, f RetryFunc) error {
	currentBackoff := r.expBackoff

	for {
		if err := f(); err != nil {
			if r.isWhitelisted(err) {
				return err
			}

			r.log.WithError(err).Warn()
			currentBackoff *= time.Duration(r.expFactor)

			//time.Sleep(currentBackoff)
			ticker := time.NewTicker(currentBackoff)
			select {
			case <-ticker.C:
				continue

			case <-ctx.Done():
				ticker.Stop()
				return ctx.Err()
			}
		}
		return nil
	}
}

func (r *Retrier) isWhitelisted(err error) bool {
	_, ok := r.errWhitelist[err]
	return ok
}
