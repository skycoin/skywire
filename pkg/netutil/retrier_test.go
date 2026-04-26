// Package netutil pkg/netutil/retrier_test.go
package netutil

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestRetrier_Do(t *testing.T) {
	r := NewRetrier(logrus.New(), time.Millisecond*100, 0, 3, 2)
	c := 0
	threshold := 2
	f := func() error {
		c++
		if c >= threshold {
			return nil
		}

		return errors.New("foo")
	}

	t.Run("should retry", func(t *testing.T) {
		c = 0

		err := r.Do(context.TODO(), f)
		require.NoError(t, err)
	})

	t.Run("if retry reaches max number of times should error", func(t *testing.T) {
		c = 0
		threshold = 4
		defer func() {
			threshold = 2
		}()

		err := r.Do(context.TODO(), f)
		require.Error(t, err)
	})

	t.Run("should return whitelisted errors if any instead of retry", func(t *testing.T) {
		bar := errors.New("bar")
		wR := NewRetrier(logrus.New(), 50*time.Millisecond, 0, 1, 2).WithErrWhitelist(bar)
		barF := func() error {
			return bar
		}

		err := wR.Do(context.TODO(), barF)
		require.EqualError(t, err, bar.Error())
	})

	t.Run("if times is 0, should retry until success", func(t *testing.T) {
		c = 0
		loopR := NewRetrier(logrus.New(), 50*time.Millisecond, 0, 0, 1)
		err := loopR.Do(context.TODO(), f)
		require.NoError(t, err)

		require.Equal(t, threshold, c)
	})
}
