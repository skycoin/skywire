// Package skyobject pkg/cxo/skyobject/index_stat.go c2-net-cxo
package skyobject

import (
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cxo/skyobject/statutil"
)

type indexStat struct {
	rps *statutil.Float // new Root objects per second
	sr  int             // roots for current second

	mx     sync.Mutex
	quit   chan struct{}
	closeo sync.Once
}

func newIndexStat(samples int, rollingAverages bool) (i *indexStat) {
	i = new(indexStat)

	i.rps = statutil.NewFloat(samples)
	i.quit = make(chan struct{})

	// The per-second roll only feeds rootsPerSecond, which is reachable solely
	// through Container.Stat() -> Node.Stat() -> the node RPC. A node with no RPC
	// listener has no reader, so the goroutine would tick forever for nobody.
	// See Config.TrackRollingAverages.
	if rollingAverages {
		go i.secondLoop()
	}

	return
}

func (i *indexStat) Close() {
	i.closeo.Do(func() {
		close(i.quit)
	})
}

func (i *indexStat) addRoot() {
	i.mx.Lock()
	defer i.mx.Unlock()

	i.sr++
}

func (i *indexStat) rootsPerSecond() float64 {
	return i.rps.Value()
}

func (i *indexStat) secondLoop() {

	var (
		tk = time.NewTicker(time.Second)
		tc = tk.C
	)

	defer tk.Stop() // was missing; its twin in cxds_stat.go has it

	for {

		select {
		case <-tc:
			i.second()
		case <-i.quit:
			return
		}

	}

}

func (i *indexStat) second() {
	i.mx.Lock()
	defer i.mx.Unlock()

	i.rps.Add(float64(i.sr))
	i.sr = 0
}
