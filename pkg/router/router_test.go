package router

import (
	"fmt"
	"log"
	"os"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/transport"
)

func TestMain(m *testing.M) {
	loggingLevel, ok := os.LookupEnv("TEST_LOGGING_LEVEL")
	if ok {
		lvl, err := logging.LevelFromString(loggingLevel)
		if err != nil {
			log.Fatal(err)
		}

		logging.SetLevel(lvl)
	} else {
		logging.SetLevel(logrus.TraceLevel)
	}

	os.Exit(m.Run())
}

type TestEnv struct {
	TpD transport.DiscoveryClient

	TpMngrConfs []*transport.ManagerConfig
	TpMngrs     []*transport.Manager

	teardown func()
}

func (e *TestEnv) GenRouterConfig(i int) *Config {
	return &Config{
		Logger:           logging.MustGetLogger(fmt.Sprintf("router_%d", i)),
		PubKey:           e.TpMngrConfs[i].PubKey,
		SecKey:           e.TpMngrConfs[i].SecKey,
		TransportManager: e.TpMngrs[i],
		SetupNodes:       nil, // TODO
	}
}

func (e *TestEnv) Teardown() {
	e.teardown()
}
