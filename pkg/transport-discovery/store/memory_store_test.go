//go:build !no_ci
// +build !no_ci

package store

import (
	"testing"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"gorm.io/gorm"
)

func TestMemory(t *testing.T) {
	logger := &logging.Logger{}
	gormDB := &gorm.DB{}
	memoryStore := true
	s, err := New(logger, gormDB, memoryStore)
	require.NoError(t, err)

	suite.Run(t, &TransportSuite{TransportStore: s})
}
