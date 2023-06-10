// Package appcommon pkg/app/appcommon/log_store.go
package appcommon

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"go.etcd.io/bbolt"

	"github.com/skycoin/skywire-utilities/pkg/logging"
)

const (
	timeLayout = time.RFC3339Nano
)

var re = regexp.MustCompile("[\u001B\u009B][[\\]()#;?]*(?:(?:(?:[a-zA-Z\\d]*(?:;[a-zA-Z\\d]*)*)?\u0007)|(?:(?:\\d{1,4}(?:;\\d{0,4})*)?[\\dA-PRZcf-ntqry=><~]))")

// NewProcLogger returns a new proc logger.
func NewProcLogger(conf ProcConfig, mLog *logging.MasterLogger) (log *logging.MasterLogger, db LogStore) {
	db, err := NewBBoltLogStore(conf.LogDBLoc, conf.AppName)
	if err != nil {
		panic(err)
	}
	log = mLog

	log.Logger.AddHook(db)

	return log, db
}

// TimestampFromLog is an utility function for retrieving the timestamp from a log. This function should be modified
// if the time layout is changed
func TimestampFromLog(log string) string {
	return strings.Split(log[1:1+len(timeLayout)], "]")[0]
}

// LogStore stores logs from apps, for later consumption from the hypervisor
type LogStore interface {
	// Write implements io.Writer
	Write(p []byte) (n int, err error)

	// Store saves given log in db
	Store(t time.Time, s string) error

	// LogSince returns the logs since given timestamp. For optimal performance,
	// the timestamp should exist in the store (you can get it from previous logs),
	// otherwise the DB will be sequentially iterated until finding entries older than given timestamp
	LogsSince(t time.Time) ([]string, error)

	Fire(entry *log.Entry) error
	Levels() []log.Level
	Flush() error
}

type bBoltLogStore struct {
	dbpath string
	bucket []byte
	mx     sync.RWMutex
}

// NewBBoltLogStore returns a bbolt implementation of an app log store.
func NewBBoltLogStore(path, appName string) (_ LogStore, err error) {
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

	if err != nil && !strings.Contains(err.Error(), bbolt.ErrBucketExists.Error()) {
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
