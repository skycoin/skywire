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
	Use:   "map",
	Short: "Show the visors of this hypervisor with their services, apps and forwarded ports",
	Long: `Show every visor attached to this hypervisor as a tree, asking each one
live for the deployment services it embeds, its apps and its forwarded ports.

The root is this hypervisor. A visor attached through a sub-hypervisor hangs
under that hypervisor. Keys that the chosen deployment publishes are named by
their role, and a final branch lists the published keys no visor here runs.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		tree, err := rpcClient.HVListVisorsTree()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to list visors: %w", err))
		}
		m := collectHVMap(rpcClient, *tree)
		roles := map[cipher.PubKey]string{}
		switch hvMapEnv {
		case "prod":
			roles = deploymentRoles(deployment.Prod)
		case "test":
			roles = deploymentRoles(deployment.Test)
		case "none", "":
		default:
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--deployment must be prod, test or none"))
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
	Visors     []mapVisor    `json:"visors"`
	// Unplaced are keys the deployment publishes that no visor here runs.
	Unplaced []mapKey `json:"unplaced,omitempty"`
}

type mapVisor struct {
	PK          cipher.PubKey            `json:"pk"`
	Hypervisor  cipher.PubKey            `json:"hypervisor"`
	Local       bool                     `json:"local,omitempty"`
	Online      bool                     `json:"online"`
	Hostname    string                   `json:"hostname,omitempty"`
	Version     string                   `json:"version,omitempty"`
	PublicIP    string                   `json:"public_ip,omitempty"`
	CountryCode string                   `json:"country_code,omitempty"`
	Role        string                   `json:"role,omitempty"`
	DmsgServer  bool                     `json:"dmsg_server,omitempty"`
	Linode      *mapLinode               `json:"linode,omitempty"`
	Services    []mapService             `json:"services,omitempty"`
	Apps        []mapApp                 `json:"apps,omitempty"`
	Forwards    []visorapi.ForwardedPort `json:"forwards,omitempty"`
	Error       string                   `json:"error,omitempty"`
}

type mapService struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	PK     string `json:"pk,omitempty"`
	OwnKey bool   `json:"own_key,omitempty"`
	State  string `json:"state"`
	Role   string `json:"role,omitempty"`
}

type mapApp struct {
	Name   string       `json:"name"`
	Port   routing.Port `json:"port"`
	Status string       `json:"status"`
}

type mapLinode struct {
	Label  string `json:"label"`
	Region string `json:"region"`
}

type mapKey struct {
	PK   cipher.PubKey `json:"pk"`
	Role string        `json:"role"`
}

// collectHVMap asks every visor in tree, at once, what it runs. A visor
// listed under several hypervisors is shown under the first.
func collectHVMap(local visorapi.API, tree visorapi.HVVisorTree) hvMap {
	var m hvMap
	if len(tree.Sections) == 0 {
		return m
	}
	m.Hypervisor = tree.Sections[0].HypervisorPK
	seen := map[cipher.PubKey]bool{}
	for _, s := range tree.Sections {
		for _, e := range s.Visors {
			if seen[e.PK] {
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
		ms := mapService{Name: s.Name, Type: s.Type, OwnKey: s.OwnKey, State: st}
		if pk, ok := dmsgURLKey(s.URL); ok {
			ms.PK = pk.Hex()
		}
		v.Services = append(v.Services, ms)
	}
	for _, a := range snap.Apps {
		if !hvMapAllApps && a.Status == appserver.AppStatusStopped {
			continue
		}
		v.Apps = append(v.Apps, mapApp{Name: a.Name, Port: a.Port, Status: a.Status.String()})
	}
	if snap.Roles != nil {
		v.DmsgServer = snap.Roles.DmsgServer.Running
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

// renderHVMap draws the map with the visor's name left of its key.
func renderHVMap(m hvMap) string {
	root := &bitree.Node{Label: m.Hypervisor.Hex()}
	nodes := map[cipher.PubKey]*bitree.Node{m.Hypervisor: root}
	for _, v := range m.Visors {
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
		g := &bitree.Node{Label: "published by the deployment, not embedded in any visor here"}
		for _, k := range m.Unplaced {
			g.Right = append(g.Right, &bitree.Node{Label: k.PK.Hex(), Cols: []string{k.Role}})
		}
		root.Right = append(root.Right, g)
	}
	return bitree.Render(root, bitree.Options{})
}

// visorNode puts the name left of the key, or after it when nested, as the
// renderer draws a left side only for the root's children.
func visorNode(v mapVisor, nested bool) *bitree.Node {
	name := v.Hostname
	if v.Linode != nil {
		name = v.Linode.Label
	}
	state := shortVersion(v.Version)
	if !v.Online {
		state = "offline"
	}
	where := strings.TrimSpace(v.PublicIP + " " + v.CountryCode)
	if v.Linode != nil {
		where += " " + v.Linode.Region
	}
	var roles []string
	if v.DmsgServer {
		roles = append(roles, "dmsg server")
	}
	if v.Role != "" && v.Role != "dmsg-server" {
		roles = append(roles, v.Role)
	}
	n := &bitree.Node{Label: v.PK.Hex(), Cols: []string{state, where, strings.Join(roles, ", ")}}
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
	if len(v.Services) > 0 {
		g := &bitree.Node{Label: "services"}
		for _, s := range v.Services {
			label := s.PK
			switch {
			case label == "":
				label = "(no key)"
			case !s.OwnKey:
				label += " (visor key)"
			}
			pub := ""
			switch {
			case s.Role == "":
			case strings.HasPrefix(s.Name, s.Role) || s.Role == s.Type:
				pub = "published"
			default:
				pub = "published as " + s.Role
			}
			g.Right = append(g.Right, &bitree.Node{Label: label, Cols: []string{s.Name, s.State, pub}})
		}
		out = append(out, g)
	}
	if len(v.Apps) > 0 {
		g := &bitree.Node{Label: "apps"}
		for _, a := range v.Apps {
			g.Right = append(g.Right, &bitree.Node{Label: a.Name, Cols: []string{a.Status, fmt.Sprintf("port %d", a.Port)}})
		}
		out = append(out, g)
	}
	if len(v.Forwards) > 0 {
		g := &bitree.Node{Label: "forwarded ports"}
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

// shortVersion drops the full commit a version repeats after its short one.
func shortVersion(v string) string {
	if i := strings.LastIndex(v, "-"); i > 0 && strings.Contains(v[:i], "-g") {
		return v[:i]
	}
	return v
}
