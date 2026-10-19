// Package clivisor cmd/skywire-cli/commands/visor/hv_map.go c4-vis-cli
package clivisor

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/0magnet/bitree"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

var (
	hvMapEnv     string
	hvMapLinode  bool
	hvMapAllApps bool
)

func init() {
	hvMapCmd.Flags().StringVar(&hvMapEnv, "deployment", "prod", "name the keys the prod or test deployment publishes; none skips it")
	hvMapCmd.Flags().BoolVar(&hvMapLinode, "linode", false, "name each visor by the Linode with its public IP (runs linode-cli)")
	hvMapCmd.Flags().BoolVar(&hvMapAllApps, "all-apps", false, "also list stopped apps")
	hvCmd.AddCommand(hvMapCmd)
}

var hvMapCmd = &cobra.Command{
	Use:   "map [visor-pk...]",
	Short: "Show the visors of this hypervisor with their services, apps and forwarded ports",
	Long: `Show every visor attached to this hypervisor as a tree, asking each one
live for the deployment services it embeds, its apps and its forwarded ports.

The root is this hypervisor. A visor attached through a sub-hypervisor hangs
under that hypervisor. Keys that the chosen deployment publishes are named by
their role, and a final branch lists the published keys no visor here runs.

Given keys, the tree shows only those visors, in that order, and the final
branch lists the published keys none of them embeds.`,
	Run: func(cmd *cobra.Command, args []string) {
		only := map[cipher.PubKey]bool{}
		for _, a := range args {
			var pk cipher.PubKey
			if err := pk.Set(a); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid visor key %q: %w", a, err))
			}
			only[pk] = true
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		tree, err := rpcClient.HVListVisorsTree()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to list visors: %w", err))
		}
		m := collectHVMap(rpcClient, *tree, only)
		order := map[cipher.PubKey]int{}
		for i, a := range args {
			var pk cipher.PubKey
			_ = pk.Set(a) //nolint:errcheck // checked above
			order[pk] = i
		}
		sort.SliceStable(m.Visors, func(i, j int) bool { return order[m.Visors[i].PK] < order[m.Visors[j].PK] })
		roles := map[cipher.PubKey]string{}
		switch hvMapEnv {
		case "prod":
			roles = deploymentRoles(deployment.Prod)
		case "none", "":
		default:
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--deployment must be prod or none"))
		}
		m.applyRoles(roles)
		if hvMapLinode {
			if err := m.applyLinodes(); err != nil {
				fmt.Fprintln(os.Stderr, "linode:", err) //nolint:errcheck
			}
		}
		internal.PrintOutput(cmd.Flags(), m, renderHVMap(m))
	},
}

// hvMap is a hypervisor and what every visor attached to it runs.
type hvMap struct {
	Hypervisor cipher.PubKey `json:"hypervisor"`
	// HypervisorIP and HypervisorVersion describe the hypervisor's own visor,
	// even when the map is limited to other visors.
	HypervisorIP      string     `json:"hypervisor_ip,omitempty"`
	HypervisorVersion string     `json:"hypervisor_version,omitempty"`
	Visors            []mapVisor `json:"visors"`
	// Unplaced are keys the deployment publishes that no visor here runs.
	Unplaced []mapKey `json:"unplaced,omitempty"`
}

type mapVisor struct {
	PK             cipher.PubKey            `json:"pk"`
	Hypervisor     cipher.PubKey            `json:"hypervisor"`
	Local          bool                     `json:"local,omitempty"`
	Online         bool                     `json:"online"`
	Hostname       string                   `json:"hostname,omitempty"`
	Version        string                   `json:"version,omitempty"`
	PublicIP       string                   `json:"public_ip,omitempty"`
	CountryCode    string                   `json:"country_code,omitempty"`
	Role           string                   `json:"role,omitempty"`
	DmsgServer     bool                     `json:"dmsg_server,omitempty"`
	DmsgServerPK   cipher.PubKey            `json:"dmsg_server_pk,omitempty"`
	DmsgServerAddr string                   `json:"dmsg_server_addr,omitempty"`
	Linode         *mapLinode               `json:"linode,omitempty"`
	Services       []mapService             `json:"services,omitempty"`
	Apps           []mapApp                 `json:"apps,omitempty"`
	Forwards       []visorapi.ForwardedPort `json:"forwards,omitempty"`
	Error          string                   `json:"error,omitempty"`
}

type mapService struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	PK     string `json:"pk,omitempty"`
	OwnKey bool   `json:"own_key,omitempty"`
	State  string `json:"state"`
	Role   string `json:"role,omitempty"`
	Listen string `json:"listen,omitempty"`
}

type mapApp struct {
	Name   string       `json:"name"`
	Port   routing.Port `json:"port"`
	Status string       `json:"status"`
	Listen string       `json:"listen,omitempty"`
	// Whitelist is the number of keys the app lets connect, "open" when it
	// takes a whitelist but has none, or "N/A" when it takes none.
	Whitelist string `json:"whitelist"`
}

type mapLinode struct {
	Label  string `json:"label"`
	Region string `json:"region"`
}

type mapKey struct {
	PK   cipher.PubKey `json:"pk"`
	Role string        `json:"role"`
}

// collectHVMap asks every visor in tree, at once, what it runs, or only the
// visors in only when it is not empty. A visor listed under several
// hypervisors is shown under the first.
func collectHVMap(local visorapi.API, tree visorapi.HVVisorTree, only map[cipher.PubKey]bool) hvMap {
	var m hvMap
	if len(tree.Sections) == 0 {
		return m
	}
	m.Hypervisor = tree.Sections[0].HypervisorPK
	seen := map[cipher.PubKey]bool{}
	for _, s := range tree.Sections {
		for _, e := range s.Visors {
			if e.PK == m.Hypervisor {
				m.HypervisorIP, m.HypervisorVersion = e.PublicIP, e.Version
			}
			if seen[e.PK] || len(only) > 0 && !only[e.PK] {
				continue
			}
			seen[e.PK] = true
			m.Visors = append(m.Visors, mapVisor{PK: e.PK, Hypervisor: s.HypervisorPK, Local: e.IsLocal,
				Online: e.Online, Hostname: e.Hostname, Version: e.Version, PublicIP: e.PublicIP,
				CountryCode: e.CountryCode, Error: e.Error})
		}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i := range m.Visors {
		v := &m.Visors[i]
		if !v.Online {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			client := local
			if !v.Local {
				conn, err := clirpc.BridgeConn(0, v.PK, skyenv.DmsgVisorRPCPort)
				if err != nil {
					v.Error = err.Error()
					return
				}
				quiet := logrus.New()
				quiet.SetOutput(io.Discard)
				client = visorapi.NewRPCClient(quiet, conn, visorapi.RPCPrefix, time.Duration(clirpc.Timeout)*time.Second)
				defer client.Close() //nolint:errcheck
			}
			v.fill(client)
		}()
	}
	wg.Wait()
	return m
}

func (v *mapVisor) fill(c visorapi.API) {
	snap, err := c.StateSnapshotProjected([]string{visorapi.SelectApps, visorapi.SelectServices, visorapi.SelectRoles})
	if err != nil {
		v.Error = err.Error()
		return
	}
	for _, s := range snap.Services {
		st := "running"
		switch {
		case s.Stopped:
			st = "stopped"
		case !s.Running:
			st = "down"
		}
		ms := mapService{Name: s.Name, Type: s.Type, OwnKey: s.OwnKey, State: st, Listen: s.PlainHTTP}
		if pk, ok := dmsgURLKey(s.URL); ok {
			ms.PK = pk.Hex()
		}
		v.Services = append(v.Services, ms)
	}
	for _, a := range snap.Apps {
		if !hvMapAllApps && a.Status == appserver.AppStatusStopped {
			continue
		}
		v.Apps = append(v.Apps, mapApp{Name: a.Name, Port: a.Port, Status: a.Status.String(),
			Listen: appListen(a.Name, a.Args), Whitelist: appWhitelist(a.Name, a.Args)})
	}
	if snap.Roles != nil {
		v.DmsgServer = snap.Roles.DmsgServer.Running
		if v.DmsgServer {
			v.DmsgServerPK, v.DmsgServerAddr = snap.Roles.DmsgServer.PK, snap.Roles.DmsgServer.LocalAddress
		}
	}
	if v.Forwards, err = c.ListForwardedPorts(); err != nil {
		v.Error = err.Error()
	}
}

// dmsgURLKey is the key of a dmsg://<pk>:<port> address.
func dmsgURLKey(u string) (cipher.PubKey, bool) {
	var pk cipher.PubKey
	rest, ok := strings.CutPrefix(u, "dmsg://")
	if !ok {
		return pk, false
	}
	rest, _, _ = strings.Cut(rest, ":")
	return pk, pk.Set(rest) == nil
}

// deploymentRoles names every key a deployment publishes by its role.
func deploymentRoles(s deployment.Services) map[cipher.PubKey]string {
	roles := map[cipher.PubKey]string{}
	for role, u := range map[string]string{
		"dmsg-discovery": s.DmsgDiscoveryDmsg, "service-discovery": s.ServiceDiscoveryDmsg,
		"transport-discovery": s.TransportDiscoveryDmsg, "route-finder": s.RouteFinderDmsg,
		"address-resolver": s.AddressResolverDmsg, "conf-service": s.ConfDmsg, "reward-system": s.RewardSystemDmsg,
	} {
		if pk, ok := dmsgURLKey(u); ok {
			roles[pk] = role
		}
	}
	for _, pk := range s.RouteSetupNodes {
		roles[pk] = "setup-node"
	}
	for _, pk := range s.TransportSetupPKs {
		roles[pk] = "transport-setup"
	}
	for _, d := range s.DmsgServers {
		var pk cipher.PubKey
		if pk.Set(d.Static) == nil {
			roles[pk] = "dmsg-server"
		}
	}
	return roles
}

func (m *hvMap) applyRoles(roles map[cipher.PubKey]string) {
	placed := map[cipher.PubKey]bool{}
	for i := range m.Visors {
		v := &m.Visors[i]
		if r, ok := roles[v.PK]; ok {
			v.Role, placed[v.PK] = r, true
		}
		for j := range v.Services {
			var pk cipher.PubKey
			if pk.Set(v.Services[j].PK) == nil {
				if r, ok := roles[pk]; ok {
					v.Services[j].Role, placed[pk] = r, true
				}
			}
		}
	}
	if len(placed) == 0 {
		return
	}
	for pk, r := range roles {
		if !placed[pk] {
			m.Unplaced = append(m.Unplaced, mapKey{PK: pk, Role: r})
		}
	}
	sort.Slice(m.Unplaced, func(i, j int) bool {
		a, b := m.Unplaced[i], m.Unplaced[j]
		return a.Role < b.Role || a.Role == b.Role && a.PK.Hex() < b.PK.Hex()
	})
}

func (m *hvMap) applyLinodes() error {
	out, err := exec.Command("linode-cli", "linodes", "list", "--json").Output()
	if err != nil {
		return err
	}
	var ls []struct {
		Label  string   `json:"label"`
		Region string   `json:"region"`
		IPv4   []string `json:"ipv4"`
	}
	if err := json.Unmarshal(out, &ls); err != nil {
		return err
	}
	for i := range m.Visors {
		for _, l := range ls {
			for _, ip := range l.IPv4 {
				if ip != "" && ip == m.Visors[i].PublicIP {
					m.Visors[i].Linode = &mapLinode{Label: l.Label, Region: l.Region}
				}
			}
		}
	}
	return nil
}

// renderHVMap draws the map. Each visor's address and version sit on a line
// above its key, and each group under a visor starts with its column names.
func renderHVMap(m hvMap) string {
	// The root's address and version follow its key as text, not columns, so a
	// long local version does not widen every column below.
	root := &bitree.Node{Label: joinNonEmpty("  ", m.Hypervisor.Hex(), m.HypervisorIP, shortVersion(m.HypervisorVersion)),
		Left: []*bitree.Node{{Label: "hypervisor"}}}
	nodes := map[cipher.PubKey]*bitree.Node{m.Hypervisor: root}
	nameCol := "hostname"
	for _, v := range m.Visors {
		if v.Linode != nil {
			nameCol = "linode"
		}
		if v.PK == m.Hypervisor {
			root.Right = append(root.Right, visorBranches(v)...)
			continue
		}
		nodes[v.PK] = visorNode(v, v.Hypervisor != m.Hypervisor)
	}
	for _, v := range m.Visors {
		if v.PK == m.Hypervisor {
			continue
		}
		parent := nodes[v.Hypervisor]
		if parent == nil {
			parent = root
		}
		parent.Right = append(parent.Right, nodes[v.PK])
	}
	if len(m.Unplaced) > 0 {
		g := &bitree.Node{Label: "in services-config.json, run by no visor here", Cols: []string{"role"}}
		for _, k := range m.Unplaced {
			g.Right = append(g.Right, &bitree.Node{Label: k.PK.Hex(), Cols: []string{k.Role}})
		}
		root.Right = append(root.Right, g)
	}
	return bitree.Render(root, bitree.Options{SpineLeft: nameCol, SpineLabel: "visor key", SpineCols: []string{"roles"}})
}

// visorNode puts the name left of the key, or after it when nested, as the
// renderer draws a left side only for the root's children.
func visorNode(v mapVisor, nested bool) *bitree.Node {
	name := v.Hostname
	if v.Linode != nil {
		name = v.Linode.Label
	}
	version := shortVersion(v.Version)
	if !v.Online {
		version = "offline"
	}
	where := v.CountryCode
	if v.Linode != nil {
		where = strings.TrimSpace(where + " " + v.Linode.Region)
	}
	var roles []string
	if v.DmsgServer {
		roles = append(roles, "dmsg server")
	}
	if v.Role != "" && v.Role != "dmsg-server" {
		roles = append(roles, v.Role)
	}
	n := &bitree.Node{Label: v.PK.Hex(), Above: joinNonEmpty("  ", v.PublicIP, version, where), Cols: []string{strings.Join(roles, ", ")}}
	switch {
	case name != "" && nested:
		n.Label += "  " + name
	case name != "":
		n.Left = []*bitree.Node{{Label: name}}
	}
	n.Right = visorBranches(v)
	return n
}

func visorBranches(v mapVisor) []*bitree.Node {
	var out []*bitree.Node
	if v.Error != "" {
		out = append(out, &bitree.Node{Label: "error: " + v.Error})
	}
	if len(v.Services) > 0 || v.DmsgServer {
		g := &bitree.Node{Label: "services", Cols: []string{"name", "listens", "services-config.json"}}
		if v.DmsgServer {
			listed := ""
			if v.Role == "dmsg-server" {
				listed = "listed"
			}
			label := v.DmsgServerPK.Hex()
			if v.DmsgServerPK == v.PK {
				label += " (visor key)"
			}
			g.Right = append(g.Right, &bitree.Node{Label: label, Cols: []string{"dmsg-server", shortAddr(v.DmsgServerAddr), listed}})
		}
		for _, s := range v.Services {
			label := s.PK
			switch {
			case label == "":
				label = "(no key)"
			case !s.OwnKey:
				label += " (visor key)"
			}
			listed := ""
			switch {
			case s.Role == "":
			case strings.HasPrefix(s.Name, s.Role) || s.Role == s.Type:
				listed = "listed"
			default:
				listed = "listed as " + s.Role
			}
			name := s.Name
			if s.State != "running" {
				name += " (" + s.State + ")"
			}
			g.Right = append(g.Right, &bitree.Node{Label: label, Cols: []string{name, shortAddr(s.Listen), listed}})
		}
		out = append(out, g)
	}
	if len(v.Apps) > 0 {
		g := &bitree.Node{Label: "apps", Cols: []string{"skynet port", "listens", "whitelist"}}
		for _, a := range v.Apps {
			name := a.Name
			if a.Status != "running" {
				name += " (" + a.Status + ")"
			}
			g.Right = append(g.Right, &bitree.Node{Label: name, Cols: []string{fmt.Sprintf("%d", a.Port), shortAddr(a.Listen), a.Whitelist}})
		}
		out = append(out, g)
	}
	if len(v.Forwards) > 0 {
		g := &bitree.Node{Label: "forwarded ports", Cols: []string{"to", "over", "label"}}
		for _, f := range v.Forwards {
			to := f.ProxyAddr
			if to == "" && f.LocalPort > 0 {
				to = fmt.Sprintf("localhost:%d", f.LocalPort)
			}
			if to == "" && f.Port == 80 {
				to = "landing page"
			}
			if to == "" {
				to = fmt.Sprintf("localhost:%d", f.Port)
			}
			var nets []string
			if f.Skynet {
				nets = append(nets, "skynet")
			}
			if f.DMSG {
				nets = append(nets, "dmsg")
			}
			fn := &bitree.Node{Label: fmt.Sprintf(":%d", f.Port), Cols: []string{to, strings.Join(nets, " "), f.Label}}
			for _, vh := range f.Vhosts {
				fn.Right = append(fn.Right, &bitree.Node{Label: "vhost " + strings.Join(vh.Names(), " "), Cols: []string{vh.ProxyAddr}})
			}
			g.Right = append(g.Right, fn)
		}
		out = append(out, g)
	}
	return out
}

// appListen is where an app serves directly, read from its arguments, or the
// app's default address when it takes one and none is given.
func appListen(name string, args []string) string {
	addr := argValue(args, "addr")
	if p := argValue(args, "port"); addr == "" && p != "" {
		addr = argValue(args, "host") + ":" + p
	}
	if addr == "" {
		switch name {
		case "skychat":
			if !hasArg(args, "portless") {
				addr = skyenv.SkychatAddr
			}
		case "skysocks-client":
			addr = skyenv.SkysocksClientAddr
		case "skydex-client":
			addr = skyenv.SkydexClientAddr
		case "skydex-market":
			addr = skyenv.SkydexMarketAddr
		}
	}
	if h := argValue(args, "http"); h != "" {
		addr = joinNonEmpty(" ", addr, h)
	}
	return addr
}

// appWhitelist counts the keys in --whitelist for the apps that take one.
// An empty whitelist lets anyone connect.
func appWhitelist(name string, args []string) string {
	switch name {
	case "skysocks", "vpn-server":
	default:
		return "N/A"
	}
	n := 0
	for _, k := range strings.Split(argValue(args, "whitelist"), ",") {
		if strings.TrimSpace(k) != "" {
			n++
		}
	}
	if n == 0 {
		return "open"
	}
	return fmt.Sprintf("%d", n)
}

// argValue is the value of --name or -name in args, given as "--name v" or "--name=v".
func argValue(args []string, name string) string {
	for i, a := range args {
		f := strings.TrimLeft(a, "-")
		if f == a {
			continue
		}
		if k, v, ok := strings.Cut(f, "="); ok && k == name {
			return v
		}
		if f == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func hasArg(args []string, name string) bool {
	for _, a := range args {
		if f := strings.TrimLeft(a, "-"); f != a && (f == name || strings.HasPrefix(f, name+"=")) {
			return true
		}
	}
	return false
}

// shortAddr drops the any-address host, so "[::]:30086" reads ":30086".
func shortAddr(a string) string {
	for _, p := range []string{"[::]", "0.0.0.0"} {
		if strings.HasPrefix(a, p+":") {
			return strings.TrimPrefix(a, p)
		}
	}
	return a
}

func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

// shortVersion drops the full commit a version repeats after its short one.
func shortVersion(v string) string {
	if i := strings.LastIndex(v, "-"); i > 0 && strings.Contains(v[:i], "-g") {
		return v[:i]
	}
	return v
}
