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

func newIndexStat(samples int) (i *indexStat) {
	i = new(indexStat)

	i.rps = statutil.NewFloat(samples)
	i.quit = make(chan struct{})

	go i.secondLoop()

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
