package dmsgtracker

import (
	"context"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsgctrl"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// Default values for DmsgTrackerManager
const (
	DefaultDTMUpdateInterval = time.Second * 30
	DefaultDTMUpdateTimeout  = time.Second * 10
)

// DmsgClientSummary summaries a dmsg client.
type DmsgClientSummary struct {
	PK        cipher.PubKey `json:"public_key"`
	ServerPK  cipher.PubKey `json:"server_public_key"`
	RoundTrip time.Duration `json:"round_trip"`
}

// DmsgTracker tracks a dmsg client.
type DmsgTracker struct {
	sum  DmsgClientSummary // dmsg summary
	ctrl *dmsgctrl.Control // dmsg ctrl
}

// newDmsgTracker creates a new DmsgTracker.
func newDmsgTracker(ctx context.Context, dmsgC *dmsg.Client, pk cipher.PubKey) (dt *DmsgTracker, err error) {
	conn, err := dmsgC.DialStream(ctx, dmsg.Addr{PK: pk, Port: skyenv.DmsgCtrlPort})
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = conn.Close() //nolint:errcheck
		}
	}()

	ctrl := dmsgctrl.ControlStream(conn)

	dur, err := ctrl.Ping(ctx)
	if err != nil {
		return nil, err
	}

	dt = &DmsgTracker{
		sum: DmsgClientSummary{
			PK:        conn.RawRemoteAddr().PK,
			ServerPK:  conn.ServerPK(),
			RoundTrip: dur,
		},
		ctrl: ctrl,
	}

	return dt, nil
}

// Update updates the dmsg client summary.
func (dt *DmsgTracker) Update(ctx context.Context) error {
	dur, err := dt.ctrl.Ping(ctx)
	if err != nil {
		return err
	}

	dt.sum.RoundTrip = dur
	return nil
}

// Manager tracks round trip durations for dmsg client connections.
type Manager struct {
	updateInterval time.Duration
	updateTimeout  time.Duration

	log *logging.Logger
	dc  *dmsg.Client
	dts map[cipher.PubKey]*DmsgTracker
	mx  sync.Mutex

	done     chan struct{}
	doneOnce sync.Once
}

// NewDmsgTrackerManager creates a new dmsg tracker manager.
func NewDmsgTrackerManager(mLog *logging.MasterLogger, dc *dmsg.Client, updateInterval, updateTimeout time.Duration) *Manager {
	log := mLog.PackageLogger("dmsg_tracker_manager")
	if updateInterval == 0 {
		updateInterval = DefaultDTMUpdateInterval
	}
	if updateTimeout == 0 {
		updateTimeout = DefaultDTMUpdateTimeout
	}

	dtm := &Manager{
		updateInterval: updateInterval,
		updateTimeout:  updateTimeout,
		log:            log,
		dc:             dc,
		dts:            make(map[cipher.PubKey]*DmsgTracker),
		done:           make(chan struct{}),
	}

	if dc != nil {
		go dtm.serve()
	}

	return dtm
}

// Serve serves the dmsg tracker manager.
func (dtm *Manager) serve() {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-dtm.done
		cancel()
	}()

	t := time.NewTicker(dtm.updateInterval)
	defer t.Stop()

	for {
		select {
		case <-dtm.done:
			return
		case <-t.C:
			dtm.updateAllTrackers(ctx)
		}
	}
}

func (dtm *Manager) updateAllTrackers(ctx context.Context) {
	dtm.mx.Lock()
	defer dtm.mx.Unlock()

	cancelCtx, cancel := context.WithDeadline(ctx, time.Now().Add(dtm.updateTimeout))
	defer cancel()

	log := dtm.log.WithField("func", "dtm.updateAllTrackers")

	type errReport struct {
		pk  cipher.PubKey
		err error
	}

	dtsLen := len(dtm.dts)
	errCh := make(chan errReport, dtsLen)
	defer close(errCh)

	for _, dt := range dtm.dts {
		dt := dt
		go func() {
			err := dt.Update(cancelCtx)
			errCh <- errReport{pk: dt.sum.PK, err: err}
		}()
	}

	for i := 0; i < dtsLen; i++ {
		if r := <-errCh; r.err != nil {
			log.WithError(r.err).
				WithField("client_pk", r.pk).
				Warn("Removing dmsg client tracker.")
			delete(dtm.dts, r.pk)
		}
	}
}

// MustGet obtains a DmsgClientSummary of the client of given pk.
// If one is not found internally, a new tracker stream is to be established, returning error on failure.
func (dtm *Manager) MustGet(ctx context.Context, pk cipher.PubKey) (DmsgClientSummary, error) {
	dtm.mx.Lock()
	defer dtm.mx.Unlock()

	if isDone(dtm.done) {
		return DmsgClientSummary{}, io.ErrClosedPipe
	}

	if e, ok := dtm.dts[pk]; ok && !isDone(e.ctrl.Done()) {
		return e.sum, nil
	}

	dtm.mustEstablishTracker(pk)
	return DmsgClientSummary{}, nil
}

// Get obtains a DmsgClientSummary of the client with given public key.
func (dtm *Manager) Get(pk cipher.PubKey) (DmsgClientSummary, bool) {
	dtm.mx.Lock()
	defer dtm.mx.Unlock()

	if isDone(dtm.done) {
		return DmsgClientSummary{}, false
	}

	return dtm.get(pk)
}

// mustEstablishTracker creates / re-creates tracker when dmsgTrackerMap entry got deleted, and reconnected.
func (dtm *Manager) mustEstablishTracker(pk cipher.PubKey) {

	log := dtm.log.WithField("func", "dtm.mustEstablishTracker")

	type errReport struct {
		pk  cipher.PubKey
		err error
	}

	errCh := make(chan errReport)
	defer close(errCh)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(dtm.updateTimeout))
	defer cancel()
	go func() {
		dt, err := newDmsgTracker(ctx, dtm.dc, pk)
		if err != nil {
			errCh <- errReport{pk: pk, err: err}
			return
		}
		dtm.mx.Lock()
		dtm.dts[pk] = dt
		dtm.mx.Unlock()
	}()

	t := time.NewTicker(dtm.updateTimeout)
	defer t.Stop()

	select {
	case <-t.C:
		log.WithError(network.ErrTimeout).WithField("client_pk", pk).Warn("Failed to re-create dmsgtracker client.")
	case r := <-errCh:
		if r.err != nil {
			log.WithError(r.err).WithField("client_pk", r.pk).Warn("Failed to re-create dmsgtracker client.")
		}
	}
}

// GetBulk obtains bulk dmsg client summaries.
func (dtm *Manager) GetBulk(pks []cipher.PubKey) []DmsgClientSummary {
	out := make([]DmsgClientSummary, 0, len(pks))

	for _, pk := range pks {
		dtm.mx.Lock()
		dt, ok := dtm.dts[pk]
		dtm.mx.Unlock()
		if !ok {
			dtm.mustEstablishTracker(pk)
		}
		out = append(out, dt.sum)
	}

	sort.Slice(out, func(i, j int) bool {
		outI := out[i].PK.Big()
		outJ := out[j].PK.Big()
		return outI.Cmp(outJ) < 0
	})

	return out
}

func (dtm *Manager) get(pk cipher.PubKey) (DmsgClientSummary, bool) {
	dt, ok := dtm.dts[pk]
	if !ok {
		return DmsgClientSummary{}, false
	}

	return dt.sum, true
}

// Close implements io.Closer
func (dtm *Manager) Close() error {
	log := dtm.log.WithField("func", "dtm.Close")

	dtm.mx.Lock()
	defer dtm.mx.Unlock()

	closed := false

	dtm.doneOnce.Do(func() {
		closed = true
		close(dtm.done)

		for pk, dt := range dtm.dts {
			if err := dt.ctrl.Close(); err != nil {
				log.WithError(err).
					WithField("client_pk", pk).
					Warn("Dmsg client closed with error.")
			}
		}
	})

	if !closed {
		return io.ErrClosedPipe
	}

	return nil
}

func isDone(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}
