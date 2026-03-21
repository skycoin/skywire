package skyobject

import (
	"time"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/statutil"
)

// A Stat represents statistic of the Container
type Stat struct {

	// CXDS is statistic of CXDS key-value store.
	CXDS ReadWriteStat
	// Cache contains read-write statistic of the
	// Cache. The Cache statistic collect only
	// effective reads and writes (where data
	// requested of changed in the Cache without
	// DB access).
	Cache ReadWriteStat
	// CacheCleaning is average pause of Cache
	// for cleaning
	CacheCleaning time.Duration

	CacheObjects ObjectsStat // cached objects
	AllObjects   ObjectsStat // all objects
	UsedObjects  ObjectsStat // used objects

	// RootsPerSecond is average vlaue of new
	// Root objects per second.
	RootsPerSecond float64

	// Feeds contains statistic of feeds
	Feeds map[cipher.PubKey]FeedStat
}

// An ObjectsStat represents
// statistics of objects
type ObjectsStat struct {
	Amount statutil.Amount
	Volume statutil.Volume
}

// A ReadWriteStat represents read-write statistic
type ReadWriteStat struct {
	RPS float64 // reads per second
	WPS float64 // writes per second
}

// A FeedStat represents statistic
// of a feed
type FeedStat struct {
	// Hads contains statistic of heads
	Heads map[uint64]HeadStat
}

// A HeadStat represents statistic of
// a head
type HeadStat struct {
	// Len is total amount of Root
	// objects of this head
	Len int

	// First Root of the head
	First RootStat
	// Last is Root of the head
	Last RootStat
}

// A RootStat represetns brief informaion about a Root
type RootStat struct {
	Time time.Time     // timestamp
	Seq  uint64        // seq number
	Hash cipher.SHA256 // hash of the Root
}

// Stat retusn statistic of the Container
func (c *Container) Stat() (s *Stat) {

	s = new(Stat)

	s.CXDS.RPS = c.Cache.stat.dbRPS()
	s.CXDS.WPS = c.Cache.stat.dbWPS()

	s.Cache.RPS = c.Cache.stat.cRPS()
	s.Cache.WPS = c.Cache.stat.cWPS()

	s.CacheCleaning = c.Cache.stat.cacheCleaning()

	var amount, volume = c.amountVolume() // of cache

	s.CacheObjects.Amount = statutil.Amount(amount) //nolint:gosec
	s.CacheObjects.Volume = statutil.Volume(volume) //nolint:gosec

	var all, used = c.db.CXDS().Amount()

	s.AllObjects.Amount = statutil.Amount(all)   //nolint:gosec
	s.UsedObjects.Amount = statutil.Amount(used) //nolint:gosec

	all, used = c.db.CXDS().Volume()

	s.AllObjects.Volume = statutil.Volume(all)   //nolint:gosec
	s.UsedObjects.Volume = statutil.Volume(used) //nolint:gosec

	s.RootsPerSecond = c.Index.stat.rootsPerSecond()

	s.Feeds = c.Index.feedsStat()

	return s
}

func (i *Index) feedsStat() (s map[cipher.PubKey]FeedStat) {

	i.mx.Lock()
	defer i.mx.Unlock()

	s = make(map[cipher.PubKey]FeedStat)

	// ignore error
	i.c.db.IdxDB().Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec

		//
		// range feeds
		//

		for pk, hs := range i.feeds {

			var (
				sf    FeedStat
				heads data.Heads
			)

			if heads, err = feeds.Heads(pk); err != nil {
				continue // ignore error
			}

			sf.Heads = make(map[uint64]HeadStat)

			//
			// range heads
			//

			for nonce, last := range hs.h {

				var (
					sh    HeadStat
					roots data.Roots
				)

				if last == nil {
					sf.Heads[nonce] = sh // blank head
				}

				if roots, err = heads.Roots(nonce); err != nil {
					continue // ignore error
				}

				sh.Len = roots.Len()

				// first

				if sh.Len > 1 {

					roots.Ascend(func(dr *data.Root) (err error) { //nolint:errcheck,gosec

						sh.First.Seq = dr.Seq
						sh.First.Time = time.Unix(0, dr.Time)
						sh.First.Hash = dr.Hash

						return data.ErrStopIteration
					})

				}

				// last

				sh.Last.Seq = last.Seq
				sh.Last.Time = time.Unix(0, last.Time)
				sh.Last.Hash = last.Hash

				sf.Heads[nonce] = sh

			}

			s[pk] = sf

			//
			// end
			//

		}

		//
		// end
		//

		return err
	})

	return s

}
