//go:build !tinygo && !js

// Package appcommon pkg/app/appcommon/log_store_native.go c2-vis-appsvc
//
// The bbolt-backed LogStore. bbolt does not compile on the TinyGo js/wasm
// target, so this file is native-only; the in-memory TinyGo store lives in
// log_store_tinygo.go. The shared interface + NewProcLogger are in log_store.go.
package appcommon

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"go.etcd.io/bbolt"
	bboltErrors "go.etcd.io/bbolt/errors"

	"github.com/skycoin/skywire/pkg/util/bbolthealth"
)

var re = regexp.MustCompile(`[\x{1b}\x{9b}][[\]()#;?]*(?:(?:(?:[a-zA-Z\d]*(?:;[a-zA-Z\d]*)*)?\x{7})|(?:(?:\d{1,4}(?:;\d{0,4})*)?[\dA-PRZcf-ntqry=><~]))`)

// newProcLogStore is the native (bbolt) backing store for NewProcLogger.
func newProcLogStore(conf ProcConfig) (LogStore, error) {
	return NewBBoltLogStore(conf.LogDBLoc, conf.AppName)
}

type bBoltLogStore struct {
	dbpath string
	bucket []byte
	mx     sync.RWMutex
}

// NewBBoltLogStore returns a bbolt implementation of an app log store.
func NewBBoltLogStore(path, appName string) (_ LogStore, err error) {
	// Repair-on-corrupt: bbolt panics from inside its batch goroutine
	// on a corrupt freelist page, which propagates up and crash-loops
	// the visor during InitConcurrent. Per-app log history is local
	// telemetry — recreate fresh on corruption instead of crashing.
	if err := bbolthealth.RepairIfCorrupt(path); err != nil {
		return nil, fmt.Errorf("appcommon: integrity-check %s: %w", path, err)
	}
	db, err := bbolt.Open(path, 0606, nil)
	if err != nil {
		return nil, err
	}

	defer func() {
		cErr := db.Close()
		err = cErr
	}()

	b := []byte(appName)
	err = db.Update(func(tx *bbolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(b); err != nil {
			return fmt.Errorf("failed to create bucket: %w", err)
		}

		return nil
	})

	if err != nil && !errors.Is(err, bboltErrors.ErrBucketExists) {
		return nil, err
	}

	return &bBoltLogStore{
		dbpath: path,
		bucket: b,
	}, nil
}

// Write implements io.Writer
func (l *bBoltLogStore) Write(p []byte) (n int, err error) {
	l.mx.Lock()
	defer l.mx.Unlock()

	// ensure there is at least timestamp long bytes
	if len(p) < len(timeLayout)+2 {
		return 0, io.ErrShortBuffer
	}

	db, err := bbolt.Open(l.dbpath, 0600, nil)
	if err != nil {
		return 0, err
	}

	defer func() {
		if closeErr := db.Close(); err == nil {
			err = closeErr
		}
	}()

	// time in RFC3339Nano is between the bytes 1 and 36. This will change if other time layout is in use
	t := p[1 : 1+len(timeLayout)]

	err = db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(l.bucket)
		return b.Put(t, p)
	})

	if err != nil {
		return 0, err
	}

	return len(p), nil
}

// Store implements LogStore
func (l *bBoltLogStore) Store(t time.Time, s string) (err error) {
	l.mx.Lock()
	defer l.mx.Unlock()

	db, err := bbolt.Open(l.dbpath, 0600, nil)
	if err != nil {
		return err
	}

	defer func() {
		cErr := db.Close()
		err = cErr
	}()

	parsedTime := []byte(t.Format(timeLayout))

	return db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(l.bucket)
		return b.Put(parsedTime, []byte(s))
	})
}

// LogSince implements LogStore
func (l *bBoltLogStore) LogsSince(t time.Time) (logs []string, err error) {
	l.mx.RLock()
	defer l.mx.RUnlock()

	db, err := bbolt.Open(l.dbpath, 0600, nil)
	if err != nil {
		return nil, err
	}

	defer func() {
		cErr := db.Close()
		err = cErr
	}()

	logs = make([]string, 0)

	err = db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(l.bucket)
		parsedTime := []byte(t.Format(timeLayout))
		c := b.Cursor()

		v := b.Get(parsedTime)
		if v == nil {
			logs = iterateFromBeginning(c, parsedTime)
			return nil
		}
		c.Seek(parsedTime)
		logs = iterateFromKey(c)
		return nil
	})

	return logs, err
}

func (l *bBoltLogStore) Fire(entry *log.Entry) error {
	l.mx.Lock()
	defer l.mx.Unlock()

	p, err := entry.String()
	if err != nil {
		return err
	}
	var substitution = ""
	str := re.ReplaceAllString(p, substitution)

	// ensure there is at least timestamp long bytes
	if len(p) < len(timeLayout)+2 {
		return io.ErrShortBuffer
	}

	db, err := bbolt.Open(l.dbpath, 0600, nil)
	if err != nil {
		return err
	}

	defer func() {
		if closeErr := db.Close(); err == nil {
			err = closeErr
		}
	}()

	// time in RFC3339Nano is between the bytes 1 and 36. This will change if other time layout is in use
	t := strings.Split(str[1:1+len(timeLayout)], "]")[0]
	err = db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(l.bucket)
		if b == nil {
			// Bucket doesn't exist, create it
			var createErr error
			b, createErr = tx.CreateBucketIfNotExists(l.bucket)
			if createErr != nil {
				return createErr
			}
		}
		return b.Put([]byte(t), []byte(str))
	})

	if err != nil {
		return err
	}
	return nil
}

func (l *bBoltLogStore) Levels() []log.Level {
	return log.AllLevels
}

func (l *bBoltLogStore) Flush() error {
	return nil
}

func iterateFromKey(c *bbolt.Cursor) []string {
	logs := make([]string, 0)

	for k, v := c.Next(); k != nil; k, v = c.Next() {
		logs = append(logs, string(v))
	}

	return logs
}

func iterateFromBeginning(c *bbolt.Cursor, parsedTime []byte) []string {
	logs := make([]string, 0)

	for k, v := c.First(); k != nil; k, v = c.Next() {
		if bytes.Compare(k, parsedTime) < 0 {
			continue
		}

		logs = append(logs, string(v))
	}

	return logs
}
