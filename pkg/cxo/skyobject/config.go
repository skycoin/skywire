package skyobject

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/skycoin/skywire/pkg/cxo/data"
	"github.com/skycoin/skywire/pkg/cxo/node/log"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// config related constants and defaults
const (
	Prefix         string = "[skyobject] " // default log prefix
	RollAvgSamples int    = 5              // rolling average samples

	Degree registry.Degree = 16 // default dgree of registry.Refs

	CacheMaxAmount  int     = 4096        // 4K
	CacheMaxVolume  int     = 8192 * 1024 // 8M
	CacheRegistries int     = 5           // 5
	CacheCleaning   float64 = 0.8         // down to 80%

	// CacheMaxItemSize is 1M (CacheMaxVolume*(1.0-CacheCleaning) / 2)
	CacheMaxItemSize int = 1024 * 1024

	// default CachePolicy is LRU

	MaxObjectSize int = 16 * 1024 * 1024 // default is 16M

	// filling

	MaxFillingParallel int = 10 // ten parallel subtrees

	// DB related constants
	CXDS  string = "cxds.db" // default CXDS file name
	IdxDB string = "idx.db"  // default IdxDB file name

	PackSavePin       log.Pin = 1 << iota // show time of (*Pack).Save in logs
	CleanUpVerbosePin                     // show collecting and removing times
	FillVerbosePin                        // show filling debug logs
	FillPin                               // show filling time

	VerbosePin // too many logs to show
)

// internal constants
const (
	// default tree is
	//   server: ~/.skycoin/cxo/{cxds.db, idx.db}
	skycoinDataDir = ".skycoin"
	cxoSubDir      = "cxo"
)

// UserHome returns the current user home path
func UserHome() string {
	// os/user relies on cgo which is disabled when cross compiling
	// use fallbacks for various OSes instead
	// usr, err := user.Current()
	// if err == nil {
	//     return usr.HomeDir
	// }
	if runtime.GOOS == "windows" {
		home := os.Getenv("HOMEDRIVE") + os.Getenv("HOMEPATH")
		if home == "" {
			home = os.Getenv("USERPROFILE")
		}
		return home
	}

	return os.Getenv("HOME")
}

// DataDir returns path to default data directory
func DataDir() string {
	homeDir := UserHome()

	if homeDir == "" {
		panic("empty home dir")
	}

	return filepath.Join(homeDir, skycoinDataDir, cxoSubDir)
}

// mkdir -p dir
func mkdirp(dir string) error {
	return os.MkdirAll(dir, 0700)
}

// A Config represents configurations
// and options of Container
type Config struct {
	// Degree of registry.Refs' Merkle-trees.
	// The option affects new trees only and not
	// changes existsing registry.Refs
	Degree registry.Degree

	// RollAvgSamples is number of samples used
	// for rolling (moving) average values for
	// statistic
	RollAvgSamples int

	// Cache configs. Set the CacheMaxAmount or the
	// CacheMaxVolume to zero to switch the cache off

	// CacheMaxAmount is maximum number of items the cache can
	// fit. See also, CacheCleaning field
	CacheMaxAmount int
	// CacheMaxVolume is maximum total length of all elements of
	// the caceh. See also CacheCleaning field
	CacheMaxVolume int
	// CachePolicy is policy of the Cache. By default it's LRU,
	// but it's possible to choose LFU if you want
	CachePolicy CachePolicy
	// CacheRegistries is number of Registries the Cache
	// will keep unpacked. A Registry is fast for access
	// but decoding is slow. And to increase performance
	// the Cache keeps some Registries unpacked. Set this
	// field to zero to turn off caching of Registries.
	// This number should not be too big. Becuse the
	// CacheCleaning field and strategy doesn't affect
	// the Registries. If the caceh of the Registries is
	// full then one removed and one appended
	CacheRegistries int
	// CacheCleaning is a flaot point number from 0.5 to 0.9.
	// The number is percent. If the Cache is full, then the
	// will be cleaned down to this percent of fullness. E.g.
	// the cache never exceeds the CacheMaxAmount and the
	// CacheMaxVolume boundaries. Reaching them, the Cache
	// cleans itself down to the percent using given
	// CachePolicy
	CacheCleaning float64
	// CacheMaxItemSize is max size of an item the cache will
	// keep inside. All items bigger will be put to DB directly.
	// This required to don't keep very big items in the cache.
	// This items can will break caching algorithms. The
	// CacheMaxItemSize can't be bigger then
	// CacheMaxVolume*(1.0 - CacheCleaning)
	CacheMaxItemSize int

	// limits

	// MaxObjectSize is max size of object the CXO can
	// manipulate. The size is size of encoded vlaue. The
	// limit is required to protect against intentional
	// DoS attacks. Because, CXO manipulate with objects
	// in RAM a very big object can break a node.
	//
	// So, reducing this size be sure that database doesn't
	// contain object large then this limit. Otherwise CXO
	// falls. To check DB after reducing set the CheckSizes
	// config to true. If DB contains objects large then
	// this limit the NewContiner returns error. And this
	// error can't be solved by CXO, e.g. you should
	// resolve it yourself. But DB doesn't have any limits
	// and it will wroks (separetly) fine.
	//
	// The MaxObjectSize can't be less then 1024
	MaxObjectSize int

	// MaxFillingParallel is limit of subtrees that used
	// by Filler at the same time. The Filler can walk
	// all possible subtress sumultaneously, creating
	// goroutines. The MaxFillingParallel should be closer
	// to number of connections that used to fill a Root.
	MaxFillingParallel int

	// DB configs

	// CheckSizes force Container to check sizes of objects
	// in database. The option can be useful if MaxObjectSize
	// was reduced after last start and you not sure that
	// DB contains valid objects. If this option is true then
	// Container check DB and returns an error if there are
	// objects alrge then MaxObjectSize limit. But, the
	// checking stops on first error.
	CheckSizes bool
	// InMemoryDB uses database in memory. The option is
	// usability trick for test. If DB field (see blow) is
	// nil and this field is treu, then default database in
	// memory will be created and used
	InMemoryDB bool
	// DBPath is path to database file. Because DB consist of
	// two files, the DBPath will be concated with extensions
	// ".cxds" and ".idx". See also DB field. E.g. for path
	// "~/.skycoin/cxo/db" Container creates or opens files
	// "~/.skycoin/cxo/db.cxds" and "~/.skycoin/cxo/db.idxdb".
	// The DBpath doesn't create directories. Use DataDir to
	// be sure that path created. The DBPath used for tests
	// and examples. But it can be used for other
	DBPath string
	// DataDir will be created if it's not empty. If DB field
	// of the config is nil, InMemoryDB is false and DBPath
	// is empty, then database will be created under the
	// DataDir (even if it's empty). In this case, names of
	// the files will be "db.cxds" and "db.idxdb"
	DataDir string

	// DB is *data.DB you can provide. If the field is not nil
	// nil, then DPPath and InMemoryDB fields ignored.
	DB *data.DB
}

// NewConfig returns pointer to Config with default values
func NewConfig() (conf *Config) {
	conf = new(Config)

	conf.Degree = Degree
	conf.RollAvgSamples = RollAvgSamples

	// cache configs

	conf.CacheMaxAmount = CacheMaxAmount
	conf.CacheMaxVolume = CacheMaxVolume
	conf.CachePolicy = LRU
	conf.CacheCleaning = CacheCleaning
	conf.CacheMaxItemSize = CacheMaxItemSize

	conf.MaxObjectSize = MaxObjectSize

	// data dir
	conf.DataDir = DataDir()

	return
}

// FromFlags associates config fields
// with command line flags.
func (c *Config) FromFlags() {
	flag.BoolVar(&c.InMemoryDB,
		"mem-db",
		c.InMemoryDB,
		"use in-memory database")
	flag.StringVar(&c.DataDir,
		"data-dir",
		c.DataDir,
		"directory with data")
	flag.StringVar(&c.DBPath,
		"db-path",
		c.DBPath,
		"path to database")
}

// Validate the Config
func (c *Config) Validate() error {

	if err := c.Degree.Validate(); err != nil {
		return err
	}
	if c.RollAvgSamples < 1 {
		return fmt.Errorf("skyobject.Config.RollAvgSampels too small: %d",
			c.RollAvgSamples)
	}

	if c.CacheMaxAmount < 0 {
		return fmt.Errorf("skyobject.Config.CacheMaxAmount is negaive: %d",
			c.CacheMaxAmount)
	}

	if c.CacheMaxVolume < 0 {
		return fmt.Errorf("skyobject.Config.CacheMaxVolume is negaive: %d",
			c.CacheMaxVolume)
	}

	if c.CachePolicy != LRU && c.CachePolicy != LFU {
		return fmt.Errorf(
			"skyobject.Config.CachePolicy is unknown: %d (choose LRU or LFU)",
			c.CachePolicy)
	}

	if c.CacheCleaning < 0.5 {
		return fmt.Errorf(
			"skyobject.Config.CacheCleaning is too small: %f (< 0.5)",
			c.CacheCleaning)
	}

	if c.CacheCleaning > 0.9 {
		return fmt.Errorf(
			"skyobject.Config.CacheCleaning is too big: %f (> 0.9)",
			c.CacheCleaning)
	}

	var cacheMaxItemSize = int(float64(c.CacheMaxVolume) *
		(1.0 - c.CacheCleaning))

	if c.CacheMaxItemSize > cacheMaxItemSize {
		return fmt.Errorf(
			"skyobject.Config.CacheaxItemSize is too big: %d (> %d)",
			c.CacheMaxItemSize, cacheMaxItemSize)
	}

	if c.MaxObjectSize < 1024 {
		return fmt.Errorf("skyobject.Config.MAxObjectSize is too small: %d",
			c.MaxObjectSize)
	}

	return nil
}
