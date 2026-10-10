// Package cliconfig cmd/skywire-cli/commands/config/deployment.go c4-vis-cli
//
// A whole deployment in one visor. `config gen --deployment <host>` writes
// the embedded service blocks and points the visor at them instead of at
// prod. `config deployment` prints the services-config other visors join
// that deployment with.
package cliconfig

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	svcblock "github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// Ports of a generated deployment, from the dmsg server's TCP port. Only
// that port and the address resolver's UDP port must be reachable from
// outside. The dmsg server serves its health on loopback, the port after its own.
const (
	deployDmsgPort   = 8080
	deployDiscOffset = 10
	// The status page of the services, for a reverse proxy, on loopback.
	deployStatusOffset = 2
	deployAROffset     = 13
)

var (
	deploymentHost  string
	deploymentRedis string
	deploymentPath  string
)

func init() {
	deploymentCmd.Flags().StringVarP(&deploymentPath, "input", "i", "", "visor config that runs the deployment (default: this install's config path)")
	RootCmd.AddCommand(deploymentCmd)
}

var deploymentCmd = &cobra.Command{
	Use:   "deployment",
	Short: "Print the services-config that joins another visor to this visor's deployment",
	Long: `Print a services-config.json for the deployment that this visor runs
in its embedded_services, as written by 'config gen --deployment'.
Another visor joins the deployment with it:

  skywire cli config gen --nofetch --svcconf services-config.json`,
	Run: func(cmd *cobra.Command, _ []string) {
		path := deploymentPath
		if path == "" {
			path = visorconfig.SkywireConfig()
		}
		raw, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		var c struct {
			PK               cipher.PubKey    `json:"pk"`
			EmbeddedServices []svcblock.Block `json:"embedded_services"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("%s: %w", path, err))
		}
		svc, err := deploymentServices(c.PK, c.EmbeddedServices)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("%s: %w", path, err))
		}
		conf := servicesConf{Prod: svc, Test: svc}
		out, _ := json.MarshalIndent(conf, "", "  ") //nolint:errcheck
		internal.PrintOutput(cmd.Flags(), conf, string(out)+"\n")
	},
}

// configureDeployment writes the deployment blocks into conf and sets the
// services the rest of config gen reads to that deployment. A block the old
// config already has is kept as it is, with its key and any edits.
func configureDeployment(pk cipher.PubKey) error {
	var old []svcblock.Block
	if oldConfCache != nil {
		old = oldConfCache.EmbeddedServices
	}
	blocks, err := deploymentBlocks(deploymentHost, deploymentRedis, pk, old)
	if err != nil {
		return err
	}
	svc, err := deploymentServices(pk, blocks)
	if err != nil {
		return err
	}
	conf.EmbeddedServices = blocks
	conf.DeploymentStatusAddr = statusAddr(deploymentHost)
	if oldConfCache != nil && oldConfCache.DeploymentStatusAddr != "" {
		conf.DeploymentStatusAddr = oldConfCache.DeploymentStatusAddr
	}
	services = svc
	// The deployment is prod for the rest of config gen, as SKYDEPLOY makes it
	// for the visor, so no field falls back to prod's services.
	deployment.Prod = svc
	serviceConfURL = ""
	return nil
}

// deploymentBlocks returns old with a block added for each deployment service
// it lacks. Transport and service discovery, the route finder and the address
// resolver run under the visor's key, pk, behind path prefixes. The others need
// keys of their own.
func deploymentBlocks(host, redis string, pk cipher.PubKey, old []svcblock.Block) ([]svcblock.Block, error) {
	host, port, err := deploymentAddr(host)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, b := range old {
		have[b.Type] = true
	}
	type keypair struct {
		pk cipher.PubKey
		sk cipher.SecKey
	}
	keys := map[string]keypair{}
	for _, t := range ownKeyTypes {
		bpk, bsk := blockKey(old, t)
		keys[t] = keypair{bpk, bsk}
	}

	srvAddr := net.JoinHostPort(host, strconv.Itoa(port))
	if pub := blockString(old, "dmsg-server", "public_address"); pub != "" {
		srvAddr = pub
	}
	entry := disc.Entry{Static: keys["dmsg-server"].pk, Server: &disc.Server{Address: srvAddr}}
	discURL := fmt.Sprintf("dmsg://%s:80", keys["dmsg-discovery"].pk.Hex())
	// Each service reaches the deployment through its own discovery and
	// server. Without these settings it would dial prod's.
	dmsgConf := map[string]any{"sessions_count": 1, "discovery_dmsg": discURL, "servers": []disc.Entry{entry}}
	arPort := strconv.Itoa(port + deployAROffset)
	tpdURL := fmt.Sprintf("dmsg://%s:80/tpd", pk.Hex())
	for _, b := range old {
		if _, tpk, ok, err := svcblock.OwnKey(b.Raw); b.Type == "transport-discovery" && err == nil && ok {
			tpdURL = fmt.Sprintf("dmsg://%s:80", tpk.Hex())
		}
	}

	fields := map[string]map[string]any{
		"dmsg-server": {"public_address": srvAddr, "local_address": fmt.Sprintf(":%d", port),
			"health_endpoint_address": fmt.Sprintf("127.0.0.1:%d", port+1),
			"discovery_dmsg":          discURL, "max_sessions": 2048, "log_level": "info"},
		"dmsg-discovery": {"addr": fmt.Sprintf("127.0.0.1:%d", port+deployDiscOffset),
			"dmsg_servers": []disc.Entry{entry}},
		"setup-node": {"dmsg": dmsgConf, "log_level": "info",
			"transport_discovery_dmsg": tpdURL},
		"transport-setup":     {"dmsg": dmsgConf},
		"transport-discovery": {},
		"route-finder":        {},
		"service-discovery":   {},
		"address-resolver": {"udp_addr": ":" + arPort,
			"public_udp_addr": net.JoinHostPort(host, arPort)},
	}
	// The entry timeouts the standalone services default to.
	for t, d := range map[string]string{"dmsg-discovery": "60m", "transport-discovery": "5m",
		"route-finder": "10m", "service-discovery": "5m", "address-resolver": "5m"} {
		fields[t]["entry_timeout"] = d
	}
	// Route finder reads the transport graph transport discovery stores.
	redisDBs := map[string]int{"dmsg-discovery": 0, "transport-discovery": 1, "route-finder": 1,
		"service-discovery": 2, "address-resolver": 3}

	blocks := append([]svcblock.Block(nil), old...)
	for _, t := range deploymentTypes {
		if have[t] {
			continue
		}
		f := fields[t]
		f["type"], f["name"] = t, deploymentNames[t]
		if k, ok := keys[t]; ok {
			f["public_key"], f["secret_key"] = k.pk, k.sk
		}
		if db, ok := redisDBs[t]; ok {
			if redis == "" {
				f["testing"] = true
			} else {
				f["redis"] = redisDB(redis, db)
			}
		}
		raw, err := json.Marshal(f)
		if err != nil {
			return nil, err
		}
		var b svcblock.Block
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		blocks = append(blocks, b)
	}
	return blocks, nil
}

// deploymentTypes are the services of a generated deployment, in start order.
var deploymentTypes = []string{"dmsg-server", "dmsg-discovery", "setup-node", "transport-setup",
	"transport-discovery", "route-finder", "service-discovery", "address-resolver"}

// ownKeyTypes are the services that cannot run under the visor's key.
var ownKeyTypes = []string{"dmsg-server", "dmsg-discovery", "setup-node", "transport-setup"}

var deploymentNames = map[string]string{"dmsg-server": "dmsgs", "dmsg-discovery": "dmsgd",
	"setup-node": "sn", "transport-setup": "tps", "transport-discovery": "tpd",
	"route-finder": "rf", "service-discovery": "sd", "address-resolver": "ar"}

// deploymentServices is the services-config of the deployment in blocks,
// which a visor with key pk runs.
func deploymentServices(pk cipher.PubKey, blocks []svcblock.Block) (visorconfig.Services, error) {
	var svc visorconfig.Services
	find := func(t string) (svcblock.Block, bool) {
		for _, b := range blocks {
			if b.Type == t {
				return b, true
			}
		}
		return svcblock.Block{}, false
	}
	keyOf := func(t string) (cipher.PubKey, error) {
		b, ok := find(t)
		if !ok {
			return cipher.PubKey{}, fmt.Errorf("no %s in embedded_services", t)
		}
		k := svcblock.ConfigPubKey(b.Raw)
		if k.Null() {
			_, k, _, _ = svcblock.OwnKey(b.Raw) //nolint:errcheck
		}
		if k.Null() {
			return k, fmt.Errorf("%s has no key of its own", t)
		}
		return k, nil
	}
	srvPK, err := keyOf("dmsg-server")
	if err != nil {
		return svc, err
	}
	discPK, err := keyOf("dmsg-discovery")
	if err != nil {
		return svc, err
	}
	snPK, err := keyOf("setup-node")
	if err != nil {
		return svc, err
	}
	tpsPK, err := keyOf("transport-setup")
	if err != nil {
		return svc, err
	}
	srvAddr := blockString(blocks, "dmsg-server", "public_address")
	if srvAddr == "" {
		return svc, fmt.Errorf("dmsg-server has no public_address")
	}
	var srv deployment.DmsgServerEntry
	srv.Static = srvPK.Hex()
	srv.Server.Address = srvAddr
	// A browser visor can only reach the server over wss, at the name the
	// server derives from its key and wss_domain_suffix.
	if suffix := strings.TrimPrefix(blockString(blocks, "dmsg-server", "wss_domain_suffix"), "."); suffix != "" {
		srv.Server.AddressWS = "wss://" + srvPK.DNSLabel() + "." + suffix + "/dmsg"
		svc.WSSDomainSuffix = suffix
	}
	svc.DmsgServers = []deployment.DmsgServerEntry{srv}
	svc.DmsgDiscoveryDmsg = fmt.Sprintf("dmsg://%s:80", discPK.Hex())
	svc.RouteSetupNodes = []cipher.PubKey{snPK}
	svc.TransportSetupPKs = []cipher.PubKey{tpsPK}
	// The browse origin serves any deployment's visors, so a private one keeps
	// prod's.
	svc.BrowseOriginSuffix = deployment.Prod.BrowseOriginSuffix
	at := func(t string) (string, error) {
		b, ok := find(t)
		if !ok {
			return "", fmt.Errorf("no %s in embedded_services", t)
		}
		if k, err := keyOf(t); err == nil {
			return fmt.Sprintf("dmsg://%s:80", k.Hex()), nil
		}
		return fmt.Sprintf("dmsg://%s:80%s", pk.Hex(), b.Prefix()), nil
	}
	for t, dst := range map[string]*string{
		"transport-discovery": &svc.TransportDiscoveryDmsg,
		"address-resolver":    &svc.AddressResolverDmsg,
		"route-finder":        &svc.RouteFinderDmsg,
		"service-discovery":   &svc.ServiceDiscoveryDmsg,
	} {
		if *dst, err = at(t); err != nil {
			return svc, err
		}
	}
	return svc, nil
}

// blockKey is the keypair of the old block of type t, or a new one.
func blockKey(old []svcblock.Block, t string) (cipher.PubKey, cipher.SecKey) {
	for _, b := range old {
		if b.Type != t {
			continue
		}
		if raw, pk, ok, err := svcblock.OwnKey(b.Raw); err == nil && ok {
			var probe struct {
				SK cipher.SecKey `json:"secret_key"`
			}
			if json.Unmarshal(raw, &probe) == nil {
				return pk, probe.SK
			}
		}
	}
	return cipher.GenerateKeyPair()
}

// blockString is field f of the first block of type t, read from the block
// or from the file its config_path names.
func blockString(blocks []svcblock.Block, t, f string) string {
	for _, b := range blocks {
		if b.Type != t {
			continue
		}
		var m map[string]any
		if json.Unmarshal(b.Raw, &m) != nil {
			return ""
		}
		if s, ok := m[f].(string); ok && s != "" {
			return s
		}
		if p, ok := m["config_path"].(string); ok && p != "" {
			data, err := os.ReadFile(p) //nolint:gosec
			if err != nil || json.Unmarshal(data, &m) != nil {
				return ""
			}
			s, _ := m[f].(string) //nolint:errcheck
			return s
		}
		return ""
	}
	return ""
}

// redisDB is the redis at base, a URL or a socket path, with database db.
func redisDB(base string, db int) string {
	if strings.HasPrefix(base, "/") {
		base = "unix://" + base
	}
	u := (&svcblock.Common{Redis: base}).RedisURL()
	if strings.HasPrefix(u, "unix://") {
		return u + "?db=" + strconv.Itoa(db)
	}
	return strings.TrimSuffix(u, "/") + "/" + strconv.Itoa(db)
}

// deploymentAddr splits the --deployment value, host or host:port, into the
// public host and the dmsg server's port.
func deploymentAddr(v string) (string, int, error) {
	if v == "" {
		return "", 0, fmt.Errorf("--deployment needs this host's public address")
	}
	host, p, err := net.SplitHostPort(v)
	if err != nil {
		return v, deployDmsgPort, nil
	}
	port, err := strconv.Atoi(p)
	if err != nil || port < 1 || port > 65535-deployAROffset {
		return "", 0, fmt.Errorf("--deployment %q: bad port", v)
	}
	return host, port, nil
}

// statusAddr is the loopback address of the deployment's status page.
func statusAddr(host string) string {
	_, port, err := deploymentAddr(host)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("127.0.0.1:%d", port+deployStatusOffset)
}
