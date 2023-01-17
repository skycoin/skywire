// Package netutil pkg/netutil/retrier.go
package netutil

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
)

// Package errors
var (
	ErrMaximumRetriesReached = errors.New("maximum retries attempted without success")
)

// Default values for retrier.
const (
	DefaultInitBackoff = time.Second
	DefaultMaxBackoff  = time.Second * 20
	DefaultTries       = int64(0)
	DefaultFactor      = float64(1.3)
)

// RetryFunc is a function used as argument of (*Retrier).Do(), which will retry on error unless it is whitelisted
type RetryFunc func() error

// Retrier holds a configuration for how retries should be performed
type Retrier struct {
	initBO time.Duration      // initial backoff duration
	maxBO  time.Duration      // maximum backoff duration
	tries  int64              // number of times the given function is to be retried until success, if 0 it will be retried forever until success
	factor float64            // multiplier for the backoff duration that is applied on every retry
	errWl  map[error]struct{} // list of errors which will always trigger retirer to return
	log    logrus.FieldLogger
}

// NewRetrier returns a retrier that is ready to call Do() method
func NewRetrier(log logrus.FieldLogger, initBO, maxBO time.Duration, tries int64, factor float64) *Retrier {
	if log != nil {
		log = log.WithField("func", "retrier")
	}
	return &Retrier{
		initBO: initBO,
		maxBO:  maxBO,
		tries:  tries,
		factor: factor,
		errWl:  make(map[error]struct{}),
		log:    log,
	}
}

// NewDefaultRetrier creates a retrier with default values.
func NewDefaultRetrier(log logrus.FieldLogger) *Retrier {
	return NewRetrier(log, DefaultInitBackoff, DefaultMaxBackoff, DefaultTries, DefaultFactor)
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

	t := time.NewTimer(bo)
	defer t.Stop()

	for i := int64(0); r.tries == 0 || i < r.tries; i++ {
		if err := f(); err != nil {
			if _, ok := r.errWl[err]; ok {
				return err
			}
			if newBO := time.Duration(float64(bo) * r.factor); r.maxBO == 0 || newBO <= r.maxBO {
				bo = newBO
			}
			select {
			case <-t.C:
				if r.log != nil {
					r.log.WithError(err).WithField("current_backoff", bo).Warn("Retrying...")
				} else {
					fmt.Printf("func = retrier, current_backoff = %v Retrying...\n", bo)
				}
				t.Reset(bo)
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	return ErrMaximumRetriesReached
}
