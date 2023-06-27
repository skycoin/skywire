//go:build !no_ci
// +build !no_ci

package store

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"gorm.io/gorm"

	"github.com/skycoin/skywire-utilities/pkg/logging"
)

func TestMemory(t *testing.T) {
	logger := &logging.Logger{}
	gormDB := &gorm.DB{}
	memoryStore := true
	s, err := New(logger, gormDB, memoryStore)
	require.NoError(t, err)

	suite.Run(t, &TransportSuite{TransportStore: s})
}
