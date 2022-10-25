// Package routing pkg/routing/table.go
package routing

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/logging"
)

var (
	// ErrRuleNotFound is returned while trying to access non-existing rule
	ErrRuleNotFound = errors.New("rule not found")
	// ErrRuleTimedOut is being returned while trying to access the rule which timed out
	ErrRuleTimedOut = errors.New("rule keep-alive timeout exceeded")
	// ErrNoAvailableRoutes is returned when there're no more available routeIDs
	ErrNoAvailableRoutes = errors.New("no available routeIDs")
)

// Table represents a routing table implementation.
type Table interface {
	// ReserveKeys reserves n RouteIDs.
	ReserveKeys(n int) ([]RouteID, error)

	// SaveRule sets RoutingRule for a given RouteID.
	SaveRule(Rule) error

	// Rule returns RoutingRule with a given RouteID.
	Rule(RouteID) (Rule, error)

	UpdateActivity(RouteID) error

	// AllRules returns all non timed out rules with a given route descriptor.
	RulesWithDesc(RouteDescriptor) []Rule

	// AllRules returns all non timed out rules.
	AllRules() []Rule

	// DelRules removes RoutingRules with a given a RouteIDs.
	DelRules([]RouteID)

	// Count returns the number of RoutingRule entries stored.
	Count() int

	// CollectGarbage checks all the stored rules, removes and returns ones that timed out.
	CollectGarbage() []Rule
}

type memTable struct {
	sync.RWMutex

	nextID   RouteID
	rules    map[RouteID]Rule
	activity map[RouteID]time.Time
	log      *logging.Logger
}

// NewTable returns an in-memory routing table implementation with a specified configuration.
func NewTable(log *logging.Logger) Table {
	mt := &memTable{
		rules:    map[RouteID]Rule{},
		activity: make(map[RouteID]time.Time),
		log:      log,
	}

	return mt
}

func (mt *memTable) ReserveKeys(n int) ([]RouteID, error) {
	first, last, err := mt.reserveKeysImpl(n)
	if err != nil {
		return nil, err
	}

	routes := make([]RouteID, 0, n)
	for id := first; id <= last; id++ {
		routes = append(routes, id)
	}

	return routes, nil
}

func (mt *memTable) reserveKeysImpl(n int) (first, last RouteID, err error) {
	mt.Lock()
	defer mt.Unlock()

	if int64(mt.nextID)+int64(n) >= math.MaxUint32 {
		return 0, 0, ErrNoAvailableRoutes
	}

	first = mt.nextID + 1
	mt.nextID += RouteID(n)
	last = mt.nextID

	return first, last, nil
}

func (mt *memTable) SaveRule(rule Rule) error {
	key := rule.KeyRouteID()
	now := time.Now()

	mt.Lock()
	defer mt.Unlock()

	mt.rules[key] = rule
	mt.log.Debugf("ROUTING TABLE CONTENTS: %v", mt.rules)
	mt.activity[key] = now

	return nil
}

// Rule fetches rule with the `key` route ID. It updates rule activity
// ONLY for the consume type of rules.
func (mt *memTable) Rule(key RouteID) (Rule, error) {
	mt.Lock()
	defer mt.Unlock()

	rule, ok := mt.rules[key]
	if !ok {
		return nil, ErrRuleNotFound
	}

	if mt.ruleIsTimedOut(key, rule) {
		return nil, ErrRuleTimedOut
	}

	// crucial, we do this when we have nowhere in the network to forward packet to.
	// In this case we update activity immediately not to acquire the lock for the second time
	ruleType := rule.Type()
	if ruleType == RuleReverse {
		mt.activity[key] = time.Now()
	}

	return rule, nil
}

func (mt *memTable) UpdateActivity(key RouteID) error {
	mt.Lock()
	defer mt.Unlock()

	rule, ok := mt.rules[key]
	if !ok {
		return fmt.Errorf("rule of id %v not found", key)
	}

	if mt.ruleIsTimedOut(key, rule) {
		return ErrRuleTimedOut
	}

	mt.activity[key] = time.Now()

	return nil
}

func (mt *memTable) RulesWithDesc(desc RouteDescriptor) []Rule {
	mt.RLock()
	defer mt.RUnlock()

	rules := make([]Rule, 0, len(mt.rules))
	for k, v := range mt.rules {
		if !mt.ruleIsTimedOut(k, v) && v.RouteDescriptor() == desc {
			rules = append(rules, v)
		}
	}

	return rules
}

func (mt *memTable) AllRules() []Rule {
	mt.RLock()
	defer mt.RUnlock()

	rules := make([]Rule, 0, len(mt.rules))
	for k, v := range mt.rules {
		if !mt.ruleIsTimedOut(k, v) {
			rules = append(rules, v)
		}
	}

	return rules
}

func (mt *memTable) DelRules(keys []RouteID) {
	for _, key := range keys {
		mt.Lock()
		mt.delRule(key)
		mt.Unlock()
	}
}

func (mt *memTable) delRule(key RouteID) {
	delete(mt.rules, key)
	delete(mt.activity, key)
}

func (mt *memTable) Count() int {
	mt.RLock()
	defer mt.RUnlock()

	return len(mt.rules)
}

func (mt *memTable) CollectGarbage() []Rule {
	mt.Lock()
	defer mt.Unlock()

	var timedOutRules []Rule
	for routeID, rule := range mt.rules {
		if mt.ruleIsTimedOut(routeID, rule) {
			timedOutRules = append(timedOutRules, rule)
			mt.delRule(routeID)
		}
	}

	return timedOutRules
}

// ruleIsExpired checks whether rule's keep alive timeout is exceeded.
// NOTE: for internal use, is NOT thread-safe, object lock should be acquired outside
func (mt *memTable) ruleIsTimedOut(key RouteID, rule Rule) bool {
	lastActivity, ok := mt.activity[key]
	idling := time.Since(lastActivity)
	keepAlive := rule.KeepAlive()

	return !ok || idling > keepAlive
}
