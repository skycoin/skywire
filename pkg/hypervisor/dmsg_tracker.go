package hypervisor

import (
	"context"
	"sync"
	"time"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/dmsgctrl"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/sirupsen/logrus"

	"github.com/SkycoinProject/skywire-mainnet/pkg/skyenv"
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

// NewDmsgTracker creates a new DmsgTracker.
func NewDmsgTracker(ctx context.Context, dmsgC *dmsg.Client, pk cipher.PubKey) (dt *DmsgTracker, err error) {
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

// DmsgTrackerManager tracks round trip durations for dmsg client connections.
type DmsgTrackerManager struct {
	updateInterval time.Duration
	updateTimeout  time.Duration

	log logrus.FieldLogger
	dc  *dmsg.Client
	dm  map[cipher.PubKey]*DmsgTracker
	mx  sync.Mutex
}

// NewDmsgTrackerManager creates a new dmsg tracker manager.
func NewDmsgTrackerManager(log logrus.FieldLogger, dc *dmsg.Client, updateInterval, updateTimeout time.Duration) *DmsgTrackerManager {
	if log == nil {
		log = logging.MustGetLogger("dmsg_tracker_manager")
	}
	if updateInterval == 0 {
		updateInterval = time.Second * 30
	}
	if updateTimeout == 0 {
		updateTimeout = time.Second * 10
	}
	return &DmsgTrackerManager{
		updateInterval: updateInterval,
		updateTimeout:  updateTimeout,
		log:            log,
		dc:             dc,
		dm:             make(map[cipher.PubKey]*DmsgTracker),
	}
}

// Serve serves the dmsg tracker manager.
func (dtm *DmsgTrackerManager) Serve(ctx context.Context) {
	t := time.NewTicker(dtm.updateInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-t.C:
			ctx, cancel := context.WithDeadline(ctx, time.Now().Add(dtm.updateTimeout))
			dtm.mx.Lock()
			updateAllTrackers(ctx, dtm.dm)
			dtm.mx.Unlock()
			cancel()
		}
	}
}

func updateAllTrackers(ctx context.Context, dts map[cipher.PubKey]*DmsgTracker) {
	log := log.WithField("func", "DmsgTrackerManager.updateAllTrackers")

	type errReport struct {
		pk  cipher.PubKey
		err error
	}

	dtsLen := len(dts)
	errCh := make(chan errReport, dtsLen)
	defer close(errCh)

	for _, te := range dts {
		te := te

		go func() {
			err := te.Update(ctx)
			errCh <- errReport{pk: te.sum.PK, err: err}
		}()
	}

	for i := 0; i < dtsLen; i++ {
		if r := <-errCh; r.err != nil {
			log.WithError(r.err).
				WithField("client_pk", r.pk).
				Warn("Removing dmsg client tracker.")
			delete(dts, r.pk)
		}
	}
}

// Add adds a dmsg tracker to track client at pk.
func (dtm *DmsgTrackerManager) Add(ctx context.Context, pk cipher.PubKey) (err error) {
	log := dtm.log.
		WithField("func", "DmsgTrackerManager.Add").
		WithField("pk", pk)

	dtm.mx.Lock()
	defer dtm.mx.Unlock()

	if e, ok := dtm.dm[pk]; ok && !isDone(e.ctrl.Done()) {
		err := e.ctrl.Close()
		log.WithError(err).Warn("Closed old control stream.")
	}

	dt, err := NewDmsgTracker(ctx, dtm.dc, pk)
	if err != nil {
		return err
	}

	dtm.dm[pk] = dt
	return nil
}

// Get obtains a DmsgClientSummary of the client with given public key.
func (dtm *DmsgTrackerManager) Get(pk cipher.PubKey) (DmsgClientSummary, bool) {
	dtm.mx.Lock()
	defer dtm.mx.Unlock()

	dt, ok := dtm.dm[pk]
	if !ok {
		return DmsgClientSummary{}, false
	}

	return dt.sum, true
}

func isDone(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}
