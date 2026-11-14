// Package visor pkg/visor/survey_test.go c3-vis-core
package visor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// The daily survey routine waits a day between rounds. It must end with its
// context, or an in-process restart leaves one behind for every start.
func TestSurveyRoutineEndsWithItsContext(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	common := &visorconfig.Common{PK: pk, SK: sk}
	common.SetLogger(logging.NewMasterLogger())
	// No reward address, so the routine reaches its day long wait at once.
	v := &Visor{
		conf:   &visorconfig.V1{Common: common, LocalPath: t.TempDir()},
		survey: surveyState{mu: new(sync.RWMutex)},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		generateSurvey(ctx, v, logging.MustGetLogger("survey_test"), true)
	}()

	select {
	case <-done:
		t.Fatal("the routine returned before its context ended")
	case <-time.After(200 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the routine outlived its context")
	}
	require.Error(t, ctx.Err())
}
