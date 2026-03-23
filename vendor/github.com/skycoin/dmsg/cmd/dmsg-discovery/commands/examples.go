// Package commands cmd/dmsg-discovery/commands/examples.go
package commands

import (
	"encoding/json"
	"fmt"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/tidwall/pretty"

	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
)

// exampleJSON marshals v to indented JSON with color, returning empty string on error
func exampleJSON(v interface{}) string {
	b, err := json.MarshalIndent(v, "    ", "  ")
	if err != nil {
		return ""
	}
	return string(pretty.Color(b, nil))
}

// generateExamples creates example responses from actual struct types
func generateExamples() string {
	// Use actual build info with fallbacks
	bi := buildinfo.Get()
	version := bi.Version
	if version == "" || version == "unknown" {
		version = "v1.3.29"
	}
	commit := bi.Commit
	if commit == "" || commit == "unknown" {
		commit = "abc1234"
	}
	date := bi.Date
	if date == "" || date == "unknown" {
		date = "2024-01-15T10:30:00Z"
	}

	// Use actual DMSG servers from embedded deployment config
	var serverEntries []disc.Entry
	var serverPKs []string
	if len(dmsg.Prod.DmsgServers) > 0 {
		// Use up to 2 real servers for examples
		limit := 2
		if len(dmsg.Prod.DmsgServers) < limit {
			limit = len(dmsg.Prod.DmsgServers)
		}
		for i := 0; i < limit; i++ {
			serverEntries = append(serverEntries, dmsg.Prod.DmsgServers[i])
			serverPKs = append(serverPKs, dmsg.Prod.DmsgServers[i].Static.Hex())
		}
	}

	// Fallback example PKs if no servers available
	exClientPK := "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"
	exClientPK2 := "024ec47420176680816e0406250e7156465e4531f5b26057c9f6297bb0303558c7"

	// GET /health - use first real server PK if available
	dmsgAddrPK := exClientPK
	if len(serverPKs) > 0 {
		dmsgAddrPK = serverPKs[0]
	}
	healthExample := map[string]interface{}{
		"build_info": map[string]interface{}{
			"version": version,
			"commit":  commit,
			"date":    date,
		},
		"started_at":   "2024-01-15T10:00:00Z",
		"dmsg_address": dmsgAddrPK + ":80",
		"dmsg_servers": serverPKs,
	}

	// disc.Entry (client) - use real server PKs for delegated_servers
	delegatedServers := serverPKs
	if len(delegatedServers) == 0 {
		delegatedServers = []string{"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"}
	}
	clientEntryExample := map[string]interface{}{
		"version":   "1.0",
		"sequence":  1,
		"timestamp": 1705315200,
		"static":    exClientPK,
		"client": map[string]interface{}{
			"delegated_servers": delegatedServers,
		},
	}

	// POST response - disc.HTTPMessage
	entrySetExample := map[string]interface{}{
		"code":    200,
		"message": "wrote a new entry",
	}
	entryUpdatedExample := map[string]interface{}{
		"code":    200,
		"message": "wrote new entry iteration",
	}
	entryDeletedExample := map[string]interface{}{
		"code":    200,
		"message": "deleted entry",
	}

	// GET /dmsg-discovery/servers/clients - map[server_pk][]client_pk
	clientsByServerExample := make(map[string][]string)
	if len(serverPKs) > 0 {
		clientsByServerExample[serverPKs[0]] = []string{exClientPK, exClientPK2}
	} else {
		clientsByServerExample["03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"] = []string{exClientPK, exClientPK2}
	}

	// GET /dmsg-discovery/server/{pk}/clients - []client_pk
	clientsForServerExample := []string{exClientPK, exClientPK2}

	// Use real server entries if available, otherwise use fallback
	var serverEntryForExample interface{}
	var serverEntriesForList []interface{}
	if len(serverEntries) > 0 {
		serverEntryForExample = serverEntries[0]
		for _, entry := range serverEntries {
			serverEntriesForList = append(serverEntriesForList, entry)
		}
	} else {
		// Fallback server entry
		serverEntryForExample = map[string]interface{}{
			"version":   "1.0",
			"sequence":  1,
			"timestamp": 1705315200,
			"static":    "03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e",
			"server": map[string]interface{}{
				"address":           "192.168.1.100:8081",
				"available_streams": 100,
				"max_streams":       200,
				"server_type":       "official",
			},
		}
		serverEntriesForList = []interface{}{serverEntryForExample}
	}

	// Arrays for list endpoints
	entriesExample := append([]interface{}{clientEntryExample}, serverEntriesForList...)
	visorEntriesExample := []interface{}{clientEntryExample}

	return fmt.Sprintf(`
Response Examples:

GET /health
%s

GET /dmsg-discovery/entry/{pk} (client entry)
%s

GET /dmsg-discovery/entry/{pk} (server entry)
%s

POST /dmsg-discovery/entry/ (new entry)
%s

POST /dmsg-discovery/entry/ (update entry)
%s

DEL /dmsg-discovery/entry
%s

GET /dmsg-discovery/entries (all client and server entries)
%s

GET /dmsg-discovery/visorEntries (client entries only)
%s

GET /dmsg-discovery/available_servers (servers with available_streams > 0)
%s

GET /dmsg-discovery/all_servers (all server entries)
%s

GET /dmsg-discovery/servers/clients
%s

GET /dmsg-discovery/server/{pk}/clients
%s`,
		exampleJSON(healthExample),
		exampleJSON(clientEntryExample),
		exampleJSON(serverEntryForExample),
		exampleJSON(entrySetExample),
		exampleJSON(entryUpdatedExample),
		exampleJSON(entryDeletedExample),
		exampleJSON(entriesExample),
		exampleJSON(visorEntriesExample),
		exampleJSON(serverEntriesForList),
		exampleJSON(serverEntriesForList),
		exampleJSON(clientsByServerExample),
		exampleJSON(clientsForServerExample))
}
