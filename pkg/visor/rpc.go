package visor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/rpc"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/buildinfo"
	"github.com/skycoin/dmsg/cipher"

	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
	"github.com/skycoin/skywire/pkg/util/updater"
)

const (
	// RPCPrefix is the prefix used with all RPC calls.
	RPCPrefix = "app-visor"
	// HealthTimeout defines timeout for /health endpoint calls.
	HealthTimeout = 5 * time.Second
)

var (
	// ErrNotImplemented occurs when a method is not implemented.
	ErrNotImplemented = errors.New("not implemented")

	// ErrNotFound is returned when a requested resource is not found.
	ErrNotFound = errors.New("not found")

	// ErrMalformedRestartContext is returned when restart context is malformed.
	ErrMalformedRestartContext = errors.New("restart context is malformed")
)

// RPC defines RPC methods for Visor.
type RPC struct {
	visor *Visor
	log   logrus.FieldLogger
}

func newRPCServer(v *Visor, remoteName string) (*rpc.Server, error) {
	rpcS := rpc.NewServer()
	rpcG := &RPC{
		visor: v,
		log:   v.MasterLogger().PackageLogger("visor_rpc:" + remoteName),
	}

	if err := rpcS.RegisterName(RPCPrefix, rpcG); err != nil {
		return nil, fmt.Errorf("failed to create visor RPC server: %w", err)
	}

	return rpcS, nil
}

/*
	<<< NODE HEALTH >>>
*/

// HealthInfo carries information about visor's external services health represented as http status codes
type HealthInfo struct {
	TransportDiscovery int `json:"transport_discovery"`
	RouteFinder        int `json:"route_finder"`
	SetupNode          int `json:"setup_node"`
	UptimeTracker      int `json:"uptime_tracker"`
	AddressResolver    int `json:"address_resolver"`
}

// Health returns health information about the visor
func (r *RPC) Health(_ *struct{}, out *HealthInfo) (err error) {
	defer rpcutil.LogCall(r.log, "Health", nil)(out, &err)

	ctx, cancel := context.WithTimeout(context.Background(), HealthTimeout/2)
	defer cancel()

	out.TransportDiscovery = http.StatusNotFound
	out.RouteFinder = http.StatusNotFound
	out.SetupNode = http.StatusNotFound
	out.UptimeTracker = http.StatusNotFound
	out.AddressResolver = http.StatusNotFound

	var wg sync.WaitGroup

	if tdClient := r.visor.TpDiscClient(); tdClient != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tdStatus, err := tdClient.Health(ctx)
			if err != nil {
				r.log.WithError(err).Warnf("Failed to check transport discovery health")

				out.TransportDiscovery = http.StatusInternalServerError
			}

			out.TransportDiscovery = tdStatus
		}()
	}

	if rfClient := r.visor.RouteFinderClient(); rfClient != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rfStatus, err := rfClient.Health(ctx)
			if err != nil {
				r.log.WithError(err).Warnf("Failed to check route finder health")

				out.RouteFinder = http.StatusInternalServerError
			}

			out.RouteFinder = rfStatus
		}()
	}

	// TODO(evanlinjin): This should actually poll the setup nodes services.
	if len(r.visor.conf.Routing.SetupNodes) == 0 {
		out.SetupNode = http.StatusNotFound
	} else {
		out.SetupNode = http.StatusOK
	}

	if utClient := r.visor.UptimeTrackerClient(); utClient != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			utStatus, err := utClient.Health(ctx)
			if err != nil {
				r.log.WithError(err).Warnf("Failed to check uptime tracker health")

				out.UptimeTracker = http.StatusInternalServerError
			}

			out.UptimeTracker = utStatus
		}()
	}

	if arClient := r.visor.AddressResolverClient(); arClient != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			arStatus, err := arClient.Health(ctx)
			if err != nil {
				r.log.WithError(err).Warnf("Failed to check address resolver health")

				out.AddressResolver = http.StatusInternalServerError
			}

			out.AddressResolver = arStatus
		}()
	}

	wg.Wait()

	return nil
}

/*
	<<< NODE UPTIME >>>
*/

// Uptime returns for how long the visor has been running in seconds
func (r *RPC) Uptime(_ *struct{}, out *float64) (err error) {
	defer rpcutil.LogCall(r.log, "Uptime", nil)(out, &err)

	*out = time.Since(r.visor.startedAt).Seconds()
	return nil
}

/*
	<<< APP LOGS >>>
*/

// AppLogsRequest represents a LogSince method request
type AppLogsRequest struct {
	// TimeStamp should be time.RFC3339Nano formated
	TimeStamp time.Time `json:"time_stamp"`
	// AppName should match the app name in visor config
	AppName string `json:"app_name"`
}

// LogsSince returns all logs from an specific app since the timestamp
func (r *RPC) LogsSince(in *AppLogsRequest, out *[]string) (err error) {
	defer rpcutil.LogCall(r.log, "LogsSince", in)(out, &err)

	proc, ok := r.visor.procM.ProcByName(in.AppName)
	if !ok {
		return fmt.Errorf("proc of app name '%s' is not found", in.AppName)
	}

	res, err := proc.Logs().LogsSince(in.TimeStamp)
	if err != nil {
		return err
	}

	*out = res
	return nil
}

/*
	<<< NODE SUMMARY >>>
*/

// TransportSummary summarizes a Transport.
type TransportSummary struct {
	ID      uuid.UUID           `json:"id"`
	Local   cipher.PubKey       `json:"local_pk"`
	Remote  cipher.PubKey       `json:"remote_pk"`
	Type    string              `json:"type"`
	Log     *transport.LogEntry `json:"log,omitempty"`
	IsSetup bool                `json:"is_setup"`
	IsUp    bool                `json:"is_up"`
}

func newTransportSummary(tm *transport.Manager, tp *transport.ManagedTransport, includeLogs, isSetup bool) *TransportSummary {
	summary := &TransportSummary{
		ID:      tp.Entry.ID,
		Local:   tm.Local(),
		Remote:  tp.Remote(),
		Type:    tp.Type(),
		IsSetup: isSetup,
		IsUp:    tp.IsUp(),
	}
	if includeLogs {
		summary.Log = tp.LogEntry
	}
	return summary
}

// Summary provides a summary of a Skywire Visor.
type Summary struct {
	PubKey          cipher.PubKey        `json:"local_pk"`
	BuildInfo       *buildinfo.Info      `json:"build_info"`
	AppProtoVersion string               `json:"app_protocol_version"`
	Apps            []*launcher.AppState `json:"apps"`
	Transports      []*TransportSummary  `json:"transports"`
	RoutesCount     int                  `json:"routes_count"`
}

// Summary provides a summary of the AppNode.
func (r *RPC) Summary(_ *struct{}, out *Summary) (err error) {
	defer rpcutil.LogCall(r.log, "Summary", nil)(out, &err)

	var summaries []*TransportSummary
	r.visor.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
		summaries = append(summaries,
			newTransportSummary(r.visor.tpM, tp, false, r.visor.router.SetupIsTrusted(tp.Remote())))
		return true
	})
	*out = Summary{
		PubKey:          r.visor.conf.PK,
		BuildInfo:       buildinfo.Get(),
		AppProtoVersion: supportedProtocolVersion,
		Apps:            r.visor.appL.AppStates(),
		Transports:      summaries,
		RoutesCount:     r.visor.router.RoutesCount(),
	}
	return nil
}

/*
	<<< APP MANAGEMENT >>>
*/

// Apps returns list of Apps registered on the Visor.
func (r *RPC) Apps(_ *struct{}, reply *[]*launcher.AppState) (err error) {
	defer rpcutil.LogCall(r.log, "Apps", nil)(reply, &err)

	*reply = r.visor.appL.AppStates()
	return nil
}

// StartApp start App with provided name.
func (r *RPC) StartApp(name *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "StartApp", name)(nil, &err)

	var envs []string
	if *name == skyenv.VPNClientName {
		envs, err = makeVPNEnvs(r.visor.conf, r.visor.net, r.visor.tpM.STCPRRemoteAddrs())
		if err != nil {
			return err
		}
	}

	return r.visor.appL.StartApp(*name, nil, envs)
}

// StopApp stops App with provided name.
func (r *RPC) StopApp(name *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "StopApp", name)(nil, &err)

	_, err = r.visor.appL.StopApp(*name)
	return err
}

// SetAutoStartIn is input for SetAutoStart.
type SetAutoStartIn struct {
	AppName   string
	AutoStart bool
}

// SetAutoStart sets auto-start settings for an app.
func (r *RPC) SetAutoStart(in *SetAutoStartIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAutoStart", in)(nil, &err)

	return r.visor.setAutoStart(in.AppName, in.AutoStart)
}

// SetAppPasswordIn is input for SetAppPassword.
type SetAppPasswordIn struct {
	AppName  string
	Password string
}

// SetAppPassword sets password for the app.
func (r *RPC) SetAppPassword(in *SetAppPasswordIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAppPassword", in)(nil, &err)

	return r.visor.setAppPassword(in.AppName, in.Password)
}

// SetAppPKIn is input for SetAppPK.
type SetAppPKIn struct {
	AppName string
	PK      cipher.PubKey
}

// SetAppPK sets PK for the app.
func (r *RPC) SetAppPK(in *SetAppPKIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAppPK", in)(nil, &err)

	return r.visor.setAppPK(in.AppName, in.PK)
}

/*
	<<< TRANSPORT MANAGEMENT >>>
*/

// TransportTypes lists all transport types supported by the Visor.
func (r *RPC) TransportTypes(_ *struct{}, out *[]string) (err error) {
	defer rpcutil.LogCall(r.log, "TransportTypes", nil)(out, &err)

	*out = r.visor.tpM.Networks()
	return nil
}

// TransportsIn is input for Transports.
type TransportsIn struct {
	FilterTypes   []string
	FilterPubKeys []cipher.PubKey
	ShowLogs      bool
}

// Transports lists Transports of the Visor and provides a summary of each.
func (r *RPC) Transports(in *TransportsIn, out *[]*TransportSummary) (err error) {
	defer rpcutil.LogCall(r.log, "Transports", in)(out, &err)

	typeIncluded := func(tType string) bool {
		if in.FilterTypes != nil {
			for _, ft := range in.FilterTypes {
				if tType == ft {
					return true
				}
			}
			return false
		}
		return true
	}
	pkIncluded := func(localPK, remotePK cipher.PubKey) bool {
		if in.FilterPubKeys != nil {
			for _, fpk := range in.FilterPubKeys {
				if localPK == fpk || remotePK == fpk {
					return true
				}
			}
			return false
		}
		return true
	}
	r.visor.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
		if typeIncluded(tp.Type()) && pkIncluded(r.visor.tpM.Local(), tp.Remote()) {
			*out = append(*out, newTransportSummary(r.visor.tpM, tp, in.ShowLogs, r.visor.router.SetupIsTrusted(tp.Remote())))
		}
		return true
	})
	return nil
}

// Transport obtains a Transport Summary of Transport of given Transport ID.
func (r *RPC) Transport(in *uuid.UUID, out *TransportSummary) (err error) {
	defer rpcutil.LogCall(r.log, "Transport", in)(out, &err)

	tp := r.visor.tpM.Transport(*in)
	if tp == nil {
		return ErrNotFound
	}
	*out = *newTransportSummary(r.visor.tpM, tp, true, r.visor.router.SetupIsTrusted(tp.Remote()))
	return nil
}

// AddTransportIn is input for AddTransport.
type AddTransportIn struct {
	RemotePK cipher.PubKey
	TpType   string
	Public   bool
	Timeout  time.Duration
}

// AddTransport creates a transport for the visor.
func (r *RPC) AddTransport(in *AddTransportIn, out *TransportSummary) (err error) {
	defer rpcutil.LogCall(r.log, "AddTransport", in)(out, &err)

	ctx := context.Background()

	if in.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second*20)
		defer cancel()
	}

	r.log.Debugf("Saving transport to %v via %v", in.RemotePK, in.TpType)

	tp, err := r.visor.tpM.SaveTransport(ctx, in.RemotePK, in.TpType)
	if err != nil {
		return err
	}

	r.log.Debugf("Saved transport to %v via %v", in.RemotePK, in.TpType)

	*out = *newTransportSummary(r.visor.tpM, tp, false, r.visor.router.SetupIsTrusted(tp.Remote()))
	return nil
}

// RemoveTransport removes a Transport from the visor.
func (r *RPC) RemoveTransport(tid *uuid.UUID, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "RemoveTransport", tid)(nil, &err)

	r.visor.tpM.DeleteTransport(*tid)
	return nil
}

/*
	<<< AVAILABLE TRANSPORTS >>>
*/

// DiscoverTransportsByPK obtains available transports via the transport discovery via given public key.
func (r *RPC) DiscoverTransportsByPK(pk *cipher.PubKey, out *[]*transport.EntryWithStatus) (err error) {
	defer rpcutil.LogCall(r.log, "DiscoverTransportsByPK", pk)(out, &err)

	tpD := r.visor.TpDiscClient()

	entries, err := tpD.GetTransportsByEdge(context.Background(), *pk)
	if err != nil {
		return err
	}

	*out = entries
	return nil
}

// DiscoverTransportByID obtains available transports via the transport discovery via a given transport ID.
func (r *RPC) DiscoverTransportByID(id *uuid.UUID, out *transport.EntryWithStatus) (err error) {
	defer rpcutil.LogCall(r.log, "DiscoverTransportByID", id)(out, &err)

	tpD := r.visor.TpDiscClient()

	entry, err := tpD.GetTransportByID(context.Background(), *id)
	if err != nil {
		return err
	}

	*out = *entry
	return nil
}

/*
	<<< ROUTES MANAGEMENT >>>
*/

// RoutingRules obtains all routing rules of the RoutingTable.
func (r *RPC) RoutingRules(_ *struct{}, out *[]routing.Rule) (err error) {
	defer rpcutil.LogCall(r.log, "RoutingRules", nil)(out, &err)

	*out = r.visor.router.Rules()
	return nil
}

// RoutingRule obtains a routing rule of given RouteID.
func (r *RPC) RoutingRule(key *routing.RouteID, rule *routing.Rule) (err error) {
	defer rpcutil.LogCall(r.log, "RoutingRule", key)(rule, &err)

	*rule, err = r.visor.router.Rule(*key)
	return err
}

// SaveRoutingRule saves a routing rule.
func (r *RPC) SaveRoutingRule(in *routing.Rule, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SaveRoutingRule", in)(nil, &err)

	return r.visor.router.SaveRule(*in)
}

// RemoveRoutingRule removes a RoutingRule based on given RouteID key.
func (r *RPC) RemoveRoutingRule(key *routing.RouteID, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "RemoveRoutingRule", key)(nil, &err)

	r.visor.router.DelRules([]routing.RouteID{*key})
	return nil
}

/*
	<<< ROUTEGROUPS MANAGEMENT >>>
	>>> TODO(evanlinjin): Implement.
*/

// RouteGroupInfo is a human-understandable representation of a RouteGroup.
type RouteGroupInfo struct {
	ConsumeRule routing.Rule
	FwdRule     routing.Rule
}

// RouteGroups retrieves routegroups via rules of the routing table.
func (r *RPC) RouteGroups(_ *struct{}, out *[]RouteGroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "RouteGroups", nil)(out, &err)

	var routegroups []RouteGroupInfo

	rules := r.visor.router.Rules()
	for _, rule := range rules {
		if rule.Type() != routing.RuleReverse {
			continue
		}

		fwdRID := rule.NextRouteID()
		rule, err := r.visor.router.Rule(fwdRID)
		if err != nil {
			return err
		}

		routegroups = append(routegroups, RouteGroupInfo{
			ConsumeRule: rule,
			FwdRule:     rule,
		})
	}

	*out = routegroups
	return nil
}

/*
	<<< VISOR MANAGEMENT >>>
*/

// Restart restarts visor.
func (r *RPC) Restart(_ *struct{}, _ *struct{}) (err error) {
	// @evanlinjin: do not defer this log statement, as the underlying visor.Logger will get closed.
	rpcutil.LogCall(r.log, "Restart", nil)(nil, nil)

	if r.visor.restartCtx == nil {
		return ErrMalformedRestartContext
	}

	return r.visor.restartCtx.Restart()
}

// Exec executes a given command in cmd and writes its output to out.
func (r *RPC) Exec(cmd *string, out *[]byte) (err error) {
	defer rpcutil.LogCall(r.log, "Exec", cmd)(out, &err)

	*out, err = r.visor.Exec(*cmd)
	return err
}

// Update updates visor.
func (r *RPC) Update(updateConfig *updater.UpdateConfig, updated *bool) (err error) {
	defer rpcutil.LogCall(r.log, "Update", updateConfig)(updated, &err)

	config := updater.UpdateConfig{}

	if updateConfig != nil {
		config = *updateConfig
	}

	*updated, err = r.visor.Update(config)
	return
}

// UpdateAvailable checks if visor update is available.
func (r *RPC) UpdateAvailable(channel *updater.Channel, version *updater.Version) (err error) {
	defer rpcutil.LogCall(r.log, "UpdateAvailable", channel)(version, &err)

	if channel == nil {
		return updater.ErrUnknownChannel
	}

	v, err := r.visor.UpdateAvailable(*channel)
	if err != nil {
		return err
	}

	if v == nil {
		return nil
	}

	*version = *v
	return nil
}

// UpdateStatus returns visor update status.
func (r *RPC) UpdateStatus(_ *struct{}, status *string) (err error) {
	*status = r.visor.UpdateStatus()
	return nil
}
