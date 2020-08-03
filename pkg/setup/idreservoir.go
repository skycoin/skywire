package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/routing"
)

// ErrNoKey is returned when key is not found.
var ErrNoKey = errors.New("id reservoir has no key")

type idReservoir struct {
	rec map[cipher.PubKey]uint8
	ids map[cipher.PubKey][]routing.RouteID
	mx  sync.Mutex
}

func newIDReservoir(paths ...routing.Path) (*idReservoir, int) {
	rec := make(map[cipher.PubKey]uint8)

	var total int

	for _, path := range paths {
		if len(path) == 0 {
			continue
		}

		rec[path[0].From]++

		for _, hop := range path {
			rec[hop.To]++
		}

		total += len(path) + 1
	}

	return &idReservoir{
		rec: rec,
		ids: make(map[cipher.PubKey][]routing.RouteID),
	}, total
}

type reserveFunc func(
	ctx context.Context,
	log *logging.Logger,
	dmsgC *dmsg.Client,
	pk cipher.PubKey,
	n uint8,
) ([]routing.RouteID, error)

func (idr *idReservoir) ReserveIDs(
	ctx context.Context,
	log *logging.Logger,
	dmsgC *dmsg.Client,
	reserve reserveFunc,
) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, len(idr.rec))
	defer close(errCh)

	for pk, n := range idr.rec {
		go func(pk cipher.PubKey, n uint8) {
			ids, err := reserve(ctx, log, dmsgC, pk, n)
			if err != nil {
				errCh <- fmt.Errorf("reserve routeID from %s failed: %v", pk, err)
				return
			}
			idr.mx.Lock()
			idr.ids[pk] = ids
			idr.mx.Unlock()
			errCh <- nil
		}(pk, n)
	}

	return finalError(len(idr.rec), errCh)
}

func (idr *idReservoir) PopID(pk cipher.PubKey) (routing.RouteID, bool) {
	idr.mx.Lock()
	defer idr.mx.Unlock()

	ids, ok := idr.ids[pk]
	if !ok || len(ids) == 0 {
		return 0, false
	}

	idr.ids[pk] = ids[1:]

	return ids[0], true
}

func (idr *idReservoir) String() string {
	idr.mx.Lock()
	defer idr.mx.Unlock()

	b, _ := json.MarshalIndent(idr.ids, "", "\t") //nolint:errcheck

	return string(b)
}

// RuleMap associates a rule to a visor's public key.
type RuleMap map[cipher.PubKey]routing.Rule

// RulesMap associates a slice of rules to a visor's public key.
type RulesMap map[cipher.PubKey][]routing.Rule

func (rm RulesMap) String() string {
	out := make(map[cipher.PubKey][]string, len(rm))

	for pk, rules := range rm {
		str := make([]string, len(rules))
		for i, rule := range rules {
			str[i] = rule.String()
		}

		out[pk] = str
	}

	jb, err := json.MarshalIndent(out, "", "\t")
	if err != nil {
		panic(err)
	}

	return string(jb)
}

// GenerateRules generates rules for given forward and reverse routes.
// The outputs are as follows:
// - maps that relate slices of forward, consume and intermediary routing rules to a given visor's public key.
// - an error (if any).
func (idr *idReservoir) GenerateRules(fwd, rev routing.Route) (
	forwardRules, consumeRules RuleMap,
	intermediaryRules RulesMap,
	err error,
) {
	forwardRules = make(RuleMap)
	consumeRules = make(RuleMap)
	intermediaryRules = make(RulesMap)

	for _, route := range []routing.Route{fwd, rev} {
		// 'firstRID' is the first visor's key routeID
		firstRID, ok := idr.PopID(route.Path[0].From)
		if !ok {
			return nil, nil, nil, ErrNoKey
		}

		desc := route.Desc
		srcPK := desc.SrcPK()
		dstPK := desc.DstPK()
		srcPort := desc.SrcPort()
		dstPort := desc.DstPort()

		var rID = firstRID

		for i, hop := range route.Path {
			nxtRID, ok := idr.PopID(hop.To)
			if !ok {
				return nil, nil, nil, ErrNoKey
			}

			if i == 0 {
				rule := routing.ForwardRule(route.KeepAlive, rID, nxtRID, hop.TpID, srcPK, dstPK, srcPort, dstPort)
				forwardRules[hop.From] = rule
			} else {
				rule := routing.IntermediaryForwardRule(route.KeepAlive, rID, nxtRID, hop.TpID)
				intermediaryRules[hop.From] = append(intermediaryRules[hop.From], rule)
			}

			rID = nxtRID
		}

		fmt.Printf("GENERATING CONSUME RULE WITH SRC %s\n", srcPK)
		rule := routing.ConsumeRule(route.KeepAlive, rID, srcPK, dstPK, srcPort, dstPort)
		consumeRules[dstPK] = rule
	}

	return forwardRules, consumeRules, intermediaryRules, nil
}

func finalError(n int, errCh <-chan error) error {
	var finalErr error

	for i := 0; i < n; i++ {
		if err := <-errCh; err != nil {
			finalErr = err
		}
	}

	return finalErr
}
