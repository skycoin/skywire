// Package clirewards cmd/skywire-cli/commands/rewards/main_test.go c4-vis-cli
package clirewards

import (
	"context"
	"errors"
	"os"
	"testing"

	deprewards "github.com/skycoin/skywire/pkg/deployment/rewards"
)

// TestMain keeps the tests off GitHub: with no release list the minimum
// version is not checked unless a test sets releasesFetch itself.
func TestMain(m *testing.M) {
	releasesFetch = func(context.Context) ([]deprewards.Release, error) {
		return nil, errors.New("no network in tests")
	}
	os.Exit(m.Run())
}
