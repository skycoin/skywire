package skyobject

import (
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cxo/skyobject/statutil"
)

type cxdsStat struct {
	drps  *statutil.Float // reads per second (DB)
	reads float64         // reads for current second

	dwps   *statutil.Float // writes per second (DB)
	writes float64         // writes for current second

	crps   *statutil.Float // reads per second (cache)
	creads float64         // cache reads for current second

	cwps    *statutil.Float // writes per second (cache)
	cwrites float64         // cache writes for current second

	// cache cleaning pause
	ccp *statutil.Duration

	mx     sync.Mutex
	quit   chan struct{}
	closeo sync.Once
}

func newCxdsStat(rollAvgSamples int) (c *cxdsStat) {

	c = new(cxdsStat)

	c.drps = statutil.NewFloat(rollAvgSamples)
	c.dwps = statutil.NewFloat(rollAvgSamples)
	c.crps = statutil.NewFloat(rollAvgSamples)
	c.cwps = statutil.NewFloat(rollAvgSamples)

	c.ccp = statutil.NewDuration(rollAvgSamples)

	c.quit = make(chan struct{})

	go c.secondLoop()

	return
}

func (c *cxdsStat) addDBGet(inc int) {
	if inc == 0 {
		c.addReadingDBRequest() // reading request
		return
	}

	c.addWritingDBRequest()
}

func (c *cxdsStat) addCacheGet(inc int) {
	if inc == 0 {
		c.addReadingCacheRequest()
		return
	}
	c.addWritingCacheRequest()
}

func (c *cxdsStat) addWritingDBRequest() {
	c.mx.Lock()
	defer c.mx.Unlock()

	c.writes++
}

func (c *cxdsStat) addReadingDBRequest() {
	c.mx.Lock()
	defer c.mx.Unlock()

	c.reads++
}

func (c *cxdsStat) addReadingCacheRequest() {
	c.mx.Lock()
	defer c.mx.Unlock()

	c.creads++
}

func (c *cxdsStat) addWritingCacheRequest() {
	c.mx.Lock()
	defer c.mx.Unlock()

	c.cwrites++
}

func (c *cxdsStat) dbRPS() float64 {
	return c.drps.Value()
}

func (c *cxdsStat) dbWPS() float64 {
	return c.dwps.Value()
}

func (c *cxdsStat) cRPS() float64 {
	return c.crps.Value()
}

func (c *cxdsStat) cWPS() float64 {
	return c.cwps.Value()
}

func (c *cxdsStat) addCacheCleaning(pause time.Duration) {
	c.ccp.Add(pause)
}

// avg of the pause
func (c *cxdsStat) cacheCleaning() time.Duration {
	return c.ccp.Value()
}

func (c *cxdsStat) secondLoop() {

	var (
		tk = time.NewTicker(time.Second)
		tc = tk.C
	)

	defer tk.Stop()

	for {
		select {
		case <-tc:
			c.second()
		case <-c.quit:
			return
		}
	}

}

func (c *cxdsStat) second() {
	c.mx.Lock()
	defer c.mx.Unlock()

	// read DB
	c.drps.Add(c.reads)
	c.reads = 0

	// write DB
	c.dwps.Add(c.writes)
	c.writes = 0

	// read cache
	c.crps.Add(c.creads)
	c.creads = 0

	// write cache
	c.cwps.Add(c.cwrites)
	c.cwrites = 0

}

func (c *cxdsStat) Close() {
	c.closeo.Do(func() {
		close(c.quit)
	})
}
