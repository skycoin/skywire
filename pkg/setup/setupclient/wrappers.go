package setupclient

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/snet"
	"github.com/skycoin/skywire/pkg/snet/snettest"
)

// RouteGroupDialer is an interface for RouteGroup dialers
type RouteGroupDialer interface {
	Dial(
		ctx context.Context,
		log *logging.Logger,
		n *snet.Network,
		setupNodes []cipher.PubKey,
		req routing.BidirectionalRoute,
	) (routing.EdgeRules, error)
}

type setupNodeDialer struct{}

// NewSetupNodeDialer returns a wrapper for (*Client).DialRouteGroup.
func NewSetupNodeDialer() RouteGroupDialer {
	return new(setupNodeDialer)
}

// Dial dials RouteGroup.
func (d *setupNodeDialer) Dial(
	ctx context.Context,
	log *logging.Logger,
	n *snet.Network,
	setupNodes []cipher.PubKey,
	req routing.BidirectionalRoute,
) (routing.EdgeRules, error) {
	client, err := NewClient(ctx, log, n, setupNodes)
	if err != nil {
		return routing.EdgeRules{}, err
	}

	defer func() {
		if err := client.Close(); err != nil {
			log.Warn(err)
		}
	}()

	resp, err := client.DialRouteGroup(ctx, req)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("route setup: %s", err)
	}

	return resp, nil
}

type mockDialer struct{}

// NewMockDialer returns a mock for (*Client).DialRouteGroup.
func NewMockDialer() RouteGroupDialer {
	return new(mockDialer)
}

// Dial dials RouteGroup.
func (d *mockDialer) Dial(
	context.Context,
	*logging.Logger,
	*snet.Network,
	[]cipher.PubKey,
	routing.BidirectionalRoute,
) (routing.EdgeRules, error) {
	keys := snettest.GenKeyPairs(2)

	srcPK, _ := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()

	var srcPort, dstPort routing.Port = 1, 2

	desc := routing.NewRouteDescriptor(srcPK, dstPK, srcPort, dstPort)

	fwdRule := routing.ForwardRule(1*time.Hour, 1, routing.RouteID(3), uuid.UUID{}, keys[0].PK, keys[1].PK, 4, 5)
	cnsmRule := routing.ConsumeRule(1*time.Hour, 2, keys[1].PK, keys[0].PK, 5, 4)

	rules := routing.EdgeRules{
		Desc:    desc,
		Forward: fwdRule,
		Reverse: cnsmRule,
	}

	return rules, nil
}
