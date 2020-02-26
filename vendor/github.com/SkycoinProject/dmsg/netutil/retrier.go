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

// Default values for retrier.
const (
	DefaultBackoff    = 100 * time.Millisecond
	DefaultMaxBackoff = time.Minute * 5
	DefaultTries      = 0
	DefaultFactor     = 2
)

// RetryFunc is a function used as argument of (*Retrier).Do(), which will retry on error unless it is whitelisted
type RetryFunc func() error

// Retrier holds a configuration for how retries should be performed
type Retrier struct {
	initBO time.Duration      // initial backoff duration
	maxBO  time.Duration      // maximum backoff duration
	factor int                // multiplier for the backoff duration that is applied on every retry
	times  int                // number of times that the given function is going to be retried until success, if 0 it will be retried forever until success
	errWl  map[error]struct{} // list of errors which will always trigger retirer to return
	log    logrus.FieldLogger
}

// NewRetrier returns a retrier that is ready to call Do() method
func NewRetrier(log logrus.FieldLogger, initBackoff, maxBackoff time.Duration, times, factor int) *Retrier {
	return &Retrier{
		initBO: initBackoff,
		maxBO:  maxBackoff,
		times:  times,
		factor: factor,
		errWl:  make(map[error]struct{}),
		log:    log,
	}
}

// NewDefaultRetrier creates a retrier with default values.
func NewDefaultRetrier(log logrus.FieldLogger) *Retrier {
	return NewRetrier(log, DefaultBackoff, DefaultMaxBackoff, DefaultTries, DefaultFactor)
}

// WithErrWhitelist sets a list of errors into the retrier, if the RetryFunc provided to Do() fails with one of them it will return inmediatelly with such error. Calling
// this function is not thread-safe, and is advised to only use it when initializing the Retrier
func (r *Retrier) WithErrWhitelist(errors ...error) *Retrier {
	for _, err := range errors {
		r.errWl[err] = struct{}{}
	}
	return r
}

// Do takes a RetryFunc and attempts to execute it.
// If it fails with an error it will be retried a maximum of given times with an initBO
// until it returns nil or an error that is whitelisted
func (r *Retrier) Do(ctx context.Context, f RetryFunc) error {
	bo := r.initBO

	for i := 0; r.times == 0 || i < r.times; i++ {
		if err := f(); err != nil {
			if _, ok := r.errWl[err]; ok {
				return err
			}
			if newBO := bo * time.Duration(r.factor); r.maxBO == 0 || newBO <= r.maxBO {
				bo = newBO
			}
			if r.log != nil {
				r.log.
					WithError(err).
					WithField("current_backoff", bo).
					Warn("Retrier: retrying...")
			}
			t := time.NewTimer(bo)
			select {
			case <-t.C:
				continue
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			}
		}
		return nil
	}
	return ErrMaximumRetriesReached
}
