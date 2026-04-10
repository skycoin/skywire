// Package integration_test provides end-to-end tests for all skywire services.
//
//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
)

// DMSG discovery PKs for services — mirrors services-config.json test entries.
// Used to check service reachability over DMSG and list their DMSG sessions.
const (
	// setupNodePK is the route setup-node's DMSG public key in the test deployment.
	// This is the same PK listed in route_setup_nodes in services-config.json.
	setupNodePK = "02603d53d49b6575a0b8cee05b70dd23c86e42cd6cba99af769d61a6196ea2bcb1"
	// transportSetupPK is the transport setup-node's DMSG public key in the test
	// deployment. This is the same PK listed in transport_setup in
	// services-config.json and in each visor's transport_setup config.
	transportSetupPK = "0277dda8a284d43b4d5ee2a4152771e76131e9437c47be5d8e835aafe02c45a9ae"
	// DMSG PKs for services reachable on port 80 over DMSG.
	tpdDmsgPK = "02a5993cb6792eb18908cb7c6c9c742ef5fbe3dcfcce2862bbc4615a5f35cbed46"
	arDmsgPK  = "02252c40a1ac021dcf6d93f1aae38023e8fd20bb0d35b7d07cba9435a7cb7ef34f"
	rfDmsgPK  = "02b88fe426b2f6f83f19b151c427c155aae186d734cbd45f12b57ddbd237b49278"
)

// DiagContext bundles everything a diagnostic pass needs. The intent is that
// anytime a test operation is about to fail and retry, we run DiagnoseNetwork
// and compare the output — if something has genuinely changed for the better
// (a transport now exists, a session is now connected, a route now exists)
// we retry; if not, we surface the real blocker instead of blindly looping.
type DiagContext struct {
	Label    string   // short label for the retry attempt
	Src, Dst string   // source / destination visor hostnames (optional)
	Visors   []string // full list of visors to diagnose (defaults to env.visorNames if nil)
}

// DiagnoseNetwork performs a comprehensive check of the network state and
// logs the results. Call this between retries so each attempt produces
// actionable diagnostics rather than opaque "it failed, trying again".
//
// Checks performed:
//  1. Container health — any restarts or crashes since last check?
//  2. Each visor's DMSG discovery entry and delegated servers.
//  3. Each visor's local transport list vs TPD view of those transports.
//  4. Each visor's routing rules.
//  5. Visor→self /health reachability over DMSG (verifies local DMSG client).
//  6. Service /health reachability over DMSG (verifies service DMSG clients).
//  7. Route setup-node DMSG discovery entry and /health reachability.
//  8. Route finder query for src→dst (if provided).
//
// All failures are logged but the function never panics. It returns a
// boolean indicating whether the network looks healthy enough to retry
// with any chance of success.
func (env *TestEnv) DiagnoseNetwork(ctx DiagContext) (healthy bool) {
	label := ctx.Label
	if label == "" {
		label = "network_diagnosis"
	}

	env.logger.Infof("========== DIAGNOSTIC PASS: %s ==========", label)
	defer env.logger.Infof("========== END DIAGNOSTIC PASS: %s ==========", label)

	visors := ctx.Visors
	if len(visors) == 0 {
		for _, v := range env.visorNames {
			visors = append(visors, strings.TrimPrefix(v, "/"))
		}
	}

	// 1. Container health check.
	containersHealthy := env.checkAllContainersRunning()

	// 2. Visor DMSG discovery entries.
	dmsgEntriesOK := true
	for _, v := range visors {
		if !env.checkVisorDmsgEntry(v) {
			dmsgEntriesOK = false
		}
	}

	// 3. Visor transports (local vs TPD vs TPS remote view).
	for _, v := range visors {
		env.checkVisorTransports(v)
	}

	// 4. Visor routing rules.
	for _, v := range visors {
		env.checkVisorRoutingRules(v)
	}

	// 5. Visor-self DMSG /health (each visor fetches its own /health via its own dmsg client).
	visorDmsgHealthy := true
	for _, v := range visors {
		if !env.checkVisorSelfHealthOverDmsg(v) {
			visorDmsgHealthy = false
		}
	}

	// 6. Services reachable over DMSG.
	servicesDmsgHealthy := env.checkServicesDmsgHealth()

	// 7. Route setup-node status.
	rsnHealthy := env.checkRouteSetupNode()

	// 8. Transport setup-node status.
	tpsHealthy := env.checkTransportSetupNode()

	// 9. Route finder (if src/dst provided).
	if ctx.Src != "" && ctx.Dst != "" {
		env.DumpRouteFinderState(ctx.Src, ctx.Dst)
	}

	healthy = containersHealthy && dmsgEntriesOK && visorDmsgHealthy && servicesDmsgHealthy && rsnHealthy && tpsHealthy
	if healthy {
		env.logger.Infof("[%s] diagnosis: network appears healthy", label)
	} else {
		env.logger.Warnf("[%s] diagnosis: network has issues — see above", label)
	}
	return healthy
}

// checkAllContainersRunning verifies every service and visor container is
// running and hasn't restarted. Returns false if any container is dead or has
// restarted since startup.
func (env *TestEnv) checkAllContainersRunning() bool {
	// Refresh container state
	containers, err := env.cli.ContainerList(env.ctx, container.ListOptions{All: true})
	if err != nil {
		env.logger.Warnf("checkAllContainersRunning: %v", err)
		return false
	}

	healthy := true
	for _, c := range containers {
		name := strings.TrimPrefix(c.Names[0], "/")
		// Skip anything that isn't a visor or service we care about.
		if !env.isRelevantContainer(name) {
			continue
		}
		if c.State != "running" {
			env.logger.Warnf("CONTAINER UNHEALTHY: %s state=%s status=%s", name, c.State, c.Status)
			env.logContainerTail(name, 50)
			healthy = false
			continue
		}
		// Detect restarts via RestartCount
		inspect, err := env.cli.ContainerInspect(env.ctx, c.ID)
		if err == nil && inspect.RestartCount > 0 {
			env.logger.Warnf("CONTAINER RESTARTED: %s RestartCount=%d", name, inspect.RestartCount)
			healthy = false
		}
	}
	return healthy
}

func (env *TestEnv) isRelevantContainer(name string) bool {
	for _, svc := range env.serviceNames {
		if strings.TrimPrefix(svc, "/") == name {
			return true
		}
	}
	for _, v := range env.visorNames {
		if strings.TrimPrefix(v, "/") == name {
			return true
		}
	}
	return false
}

// checkVisorDmsgEntry queries DMSG discovery for the visor's entry and logs
// its delegated servers and timestamp. Returns false if the entry is missing
// or has zero delegated servers (visor is not connected to any DMSG server).
func (env *TestEnv) checkVisorDmsgEntry(visor string) bool {
	pk, ok := env.visorPKs[visor]
	if !ok {
		env.logger.Warnf("checkVisorDmsgEntry: no PK for visor %s", visor)
		return false
	}

	cmd := fmt.Sprintf("/release/skywire cli mdisc entry %s --url http://dmsg-discovery:9090 --json", pk)

	type dmsgClient struct {
		DelegatedServers []string `json:"delegated_servers"`
	}
	type dmsgEntry struct {
		Static    string      `json:"static"`
		Client    *dmsgClient `json:"client,omitempty"`
		Timestamp int64       `json:"timestamp"`
	}
	cliOut := struct {
		Output *dmsgEntry `json:"output,omitempty"`
		Err    *string    `json:"error,omitempty"`
	}{}

	if err := env.ExecJSON(cmd, &cliOut); err != nil {
		env.logger.Warnf("VISOR DMSG ENTRY [%s]: mdisc query failed: %v", visor, err)
		return false
	}
	if cliOut.Err != nil {
		env.logger.Warnf("VISOR DMSG ENTRY [%s]: mdisc returned error: %s", visor, *cliOut.Err)
		return false
	}
	if cliOut.Output == nil || cliOut.Output.Client == nil {
		env.logger.Warnf("VISOR DMSG ENTRY [%s]: no client entry in discovery", visor)
		return false
	}
	delegated := cliOut.Output.Client.DelegatedServers
	// Timestamp is nanoseconds since epoch — pass as the nsec arg, not sec.
	age := time.Since(time.Unix(0, cliOut.Output.Timestamp)).Round(time.Second)
	env.logger.Infof("VISOR DMSG ENTRY [%s]: %d delegated servers, entry age %v", visor, len(delegated), age)
	for i, srv := range delegated {
		env.logger.Infof("   [%d] %s", i, truncatePK(srv))
	}
	return len(delegated) > 0
}

// checkVisorTransports lists the visor's local transports and cross-references
// them with the transport discovery view and the TPS remote-query view.
// Three-way cross-check:
//   - local: what the visor itself reports via its own RPC
//   - tpd:   what transport-discovery has registered for this PK
//   - tps:   what transport-setup-node sees when asked to list transports
//     on this visor via DMSG RPC (this is how an external observer
//     sees the visor's state and bypasses the local RPC path)
func (env *TestEnv) checkVisorTransports(visor string) {
	tps, err := env.VisorTpLs(visor)
	if err != nil {
		env.logger.Warnf("VISOR TRANSPORTS [%s]: local tp list failed: %v", visor, err)
		return
	}
	env.logger.Infof("VISOR TRANSPORTS [%s]: %d local transports", visor, len(tps))
	localIDs := make(map[string]bool)
	for _, tp := range tps {
		localIDs[tp.ID.String()] = true
		env.logger.Infof("   local: id=%s type=%s remote=%s", truncateID(tp.ID.String()), tp.Type, truncatePK(tp.Remote.Hex()))
	}

	pk, ok := env.visorPKs[visor]
	if !ok {
		return
	}
	tpdEntries, err := env.QueryTPDByPK(pk)
	if err != nil {
		env.logger.Warnf("VISOR TRANSPORTS [%s]: TPD query failed: %v", visor, err)
	} else {
		env.logger.Infof("VISOR TRANSPORTS [%s]: %d TPD entries", visor, len(tpdEntries))
		tpdIDs := make(map[string]bool)
		for _, e := range tpdEntries {
			tpdIDs[e.ID] = true
			env.logger.Infof("   tpd:   id=%s type=%s edges=%s,%s", truncateID(e.ID), e.Type, truncatePK(e.Edge1), truncatePK(e.Edge2))
		}

		// Diff local vs TPD.
		for id := range localIDs {
			if !tpdIDs[id] {
				env.logger.Warnf("VISOR TRANSPORTS [%s]: local TP %s NOT registered in TPD", visor, truncateID(id))
			}
		}
		for id := range tpdIDs {
			if !localIDs[id] {
				env.logger.Warnf("VISOR TRANSPORTS [%s]: TPD entry %s NOT present locally (stale?)", visor, truncateID(id))
			}
		}
	}

	// TPS remote-query view: ask the transport setup-node via DMSG RPC
	// to list this visor's transports. This is independent of the visor's
	// local RPC and the TPD's cached state — it's what an external
	// observer sees when reaching the visor over DMSG.
	env.checkVisorTransportsViaTPS(visor, pk, localIDs)
}

// checkVisorTransportsViaTPS queries the transport setup-node for a visor's
// transports. Uses skywire cli tp --remote <pk> which goes through the TPS
// RPC over DMSG.
//
// The CLI output matches []remoteResult in cmd/skywire-cli/commands/tp/tp.go:
//
//	{
//	  "output": [
//	    {"TargetPK": "...", "Transports": [...], "Error": ""}
//	  ]
//	}
func (env *TestEnv) checkVisorTransportsViaTPS(visor, pk string, localIDs map[string]bool) {
	cmd := fmt.Sprintf("/release/skywire cli tp --remote %s --json", pk)
	type remoteTP struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		Remote string `json:"remote"`
	}
	type remoteResult struct {
		TargetPK   string     `json:"TargetPK"`
		Transports []remoteTP `json:"Transports"`
		Error      string     `json:"Error"`
	}
	cliOut := struct {
		Output []remoteResult `json:"output,omitempty"`
		Err    *string        `json:"error,omitempty"`
	}{}
	if err := env.ExecJSON(cmd, &cliOut); err != nil {
		env.logger.Warnf("VISOR TRANSPORTS [%s]: TPS remote query failed: %v", visor, err)
		return
	}
	if cliOut.Err != nil {
		env.logger.Warnf("VISOR TRANSPORTS [%s]: TPS remote query error: %s", visor, *cliOut.Err)
		return
	}
	if len(cliOut.Output) == 0 {
		env.logger.Warnf("VISOR TRANSPORTS [%s]: TPS remote query returned no results", visor)
		return
	}
	res := cliOut.Output[0]
	if res.Error != "" {
		env.logger.Warnf("VISOR TRANSPORTS [%s]: TPS remote query for target reported error: %s", visor, res.Error)
		return
	}
	env.logger.Infof("VISOR TRANSPORTS [%s]: %d transports via TPS remote query", visor, len(res.Transports))
	tpsIDs := make(map[string]bool)
	for _, tp := range res.Transports {
		tpsIDs[tp.ID] = true
		env.logger.Infof("   tps:   id=%s type=%s remote=%s", truncateID(tp.ID), tp.Type, truncatePK(tp.Remote))
	}
	// Diff local vs TPS remote view.
	for id := range localIDs {
		if !tpsIDs[id] {
			env.logger.Warnf("VISOR TRANSPORTS [%s]: local TP %s NOT visible via TPS (visor unreachable from TPS?)", visor, truncateID(id))
		}
	}
	for id := range tpsIDs {
		if !localIDs[id] {
			env.logger.Warnf("VISOR TRANSPORTS [%s]: TPS view has extra TP %s (stale local?)", visor, truncateID(id))
		}
	}
}

// checkVisorRoutingRules lists the current routing rules on a visor.
func (env *TestEnv) checkVisorRoutingRules(visor string) {
	rules, err := env.VisorRouteLsRules(visor)
	if err != nil {
		env.logger.Warnf("VISOR ROUTING RULES [%s]: list failed: %v", visor, err)
		return
	}
	env.logger.Infof("VISOR ROUTING RULES [%s]: %d rules", visor, len(rules))
	for i, r := range rules {
		// Dump the raw struct; exact fields vary.
		b, _ := json.Marshal(r) //nolint:errcheck
		if len(b) > 300 {
			b = b[:300]
		}
		env.logger.Infof("   [%d] %s", i, string(b))
	}
}

// checkVisorSelfHealthOverDmsg verifies the visor can reach its own /health
// endpoint via its own DMSG client — this is the cleanest test of whether the
// visor has a working DMSG session.
func (env *TestEnv) checkVisorSelfHealthOverDmsg(visor string) bool {
	pk, ok := env.visorPKs[visor]
	if !ok {
		return false
	}
	dmsgURL := fmt.Sprintf("dmsg://%s:80/health", pk)
	cmd := fmt.Sprintf("/release/skywire cli --rpc %s:3435 dmsg curl -l fatal %s", visor, dmsgURL)
	out, err := env.Exec(cmd)
	if err != nil {
		env.logger.Warnf("VISOR SELF HEALTH [%s]: dmsg curl failed: %v", visor, err)
		return false
	}
	if !strings.Contains(out, "build_info") && !strings.Contains(out, "started_at") {
		env.logger.Warnf("VISOR SELF HEALTH [%s]: unexpected response: %.200s", visor, out)
		return false
	}
	env.logger.Infof("VISOR SELF HEALTH [%s]: OK (dmsg://%s:80/health reachable)", visor, truncatePK(pk))
	return true
}

// checkServicesDmsgHealth verifies TPD / AR / RF are reachable over DMSG
// using the e2e-test container's standalone dmsg client. Queries run in
// parallel so one slow service doesn't serialize the whole diagnostic pass.
func (env *TestEnv) checkServicesDmsgHealth() bool {
	services := []struct {
		name, pk string
	}{
		{"transport-discovery", tpdDmsgPK},
		{"address-resolver", arDmsgPK},
		{"route-finder", rfDmsgPK},
	}
	type result struct {
		name    string
		out     string
		err     error
		healthy bool
	}
	results := make(chan result, len(services))
	for _, svc := range services {
		go func(name, pk string) {
			dmsgURL := fmt.Sprintf("dmsg://%s:80/health", pk)
			cmd := fmt.Sprintf("/release/skywire dmsg curl -Z -l fatal %s", dmsgURL)
			out, err := env.Exec(cmd)
			healthy := err == nil && strings.Contains(out, "build_info")
			results <- result{name: name, out: out, err: err, healthy: healthy}
		}(svc.name, svc.pk)
	}
	healthy := true
	for i := 0; i < len(services); i++ {
		r := <-results
		if !r.healthy {
			env.logger.Warnf("SERVICE DMSG HEALTH [%s]: not reachable: err=%v out=%.120s", r.name, r.err, r.out)
			healthy = false
			continue
		}
		env.logger.Infof("SERVICE DMSG HEALTH [%s]: OK", r.name)
	}
	return healthy
}

// checkRouteSetupNode verifies the route setup-node is registered in DMSG
// discovery and its /health endpoint is reachable.
func (env *TestEnv) checkRouteSetupNode() bool {
	return env.checkDmsgServiceNode("RSN", setupNodePK)
}

// checkTransportSetupNode verifies the transport setup-node is registered in
// DMSG discovery and its /health endpoint is reachable.
func (env *TestEnv) checkTransportSetupNode() bool {
	return env.checkDmsgServiceNode("TPS", transportSetupPK)
}

// checkDmsgServiceNode is the shared implementation for RSN/TPS discovery
// checks. Logs its results with the given label prefix. Unlike skywire
// services, RSN and TPS do not serve an HTTP /health endpoint over DMSG —
// their presence in DMSG discovery with delegated servers is the health
// signal. Previous attempts to dmsg curl /health timed out for ~1:48s per
// check and dominated the diagnostic pass runtime; they're removed.
func (env *TestEnv) checkDmsgServiceNode(label, pk string) bool {
	cmd := fmt.Sprintf("/release/skywire cli mdisc entry %s --url http://dmsg-discovery:9090 --json", pk)

	type dmsgClient struct {
		DelegatedServers []string `json:"delegated_servers"`
	}
	type dmsgEntry struct {
		Static    string      `json:"static"`
		Client    *dmsgClient `json:"client,omitempty"`
		Timestamp int64       `json:"timestamp"`
	}
	cliOut := struct {
		Output *dmsgEntry `json:"output,omitempty"`
		Err    *string    `json:"error,omitempty"`
	}{}

	if err := env.ExecJSON(cmd, &cliOut); err != nil {
		env.logger.Warnf("%s DMSG ENTRY: query failed: %v", label, err)
		return false
	}
	if cliOut.Err != nil || cliOut.Output == nil || cliOut.Output.Client == nil {
		env.logger.Warnf("%s DMSG ENTRY: no client entry in discovery", label)
		return false
	}
	delegated := cliOut.Output.Client.DelegatedServers
	// Timestamp is nanoseconds — pass as the nsec arg, not sec.
	age := time.Since(time.Unix(0, cliOut.Output.Timestamp)).Round(time.Second)
	env.logger.Infof("%s DMSG ENTRY: %d delegated servers, entry age %v", label, len(delegated), age)
	return len(delegated) > 0
}
