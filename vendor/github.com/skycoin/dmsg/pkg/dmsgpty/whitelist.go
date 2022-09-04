// Package dmsgpty pkg/dmsgpty/whitelist.go
package dmsgpty

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	jsoniter "github.com/json-iterator/go"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

var (
	json = jsoniter.ConfigFastest
	wl   cipher.PubKeys
)

// Whitelist represents a whitelist of public keys.
type Whitelist interface {
	Get(pk cipher.PubKey) (bool, error)
	All() (map[cipher.PubKey]bool, error)
	Add(pks ...cipher.PubKey) error
	Remove(pks ...cipher.PubKey) error
}

// conf to update whitelists
var conf = Config{}

// NewConfigWhitelist creates a config file implementation of a whitelist.
func NewConfigWhitelist(confPath string) (Whitelist, error) {
	confPath, err := filepath.Abs(confPath)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(confPath), 0750); err != nil {
		return nil, err
	}

	return &configWhitelist{confPath: confPath}, nil
}

type configWhitelist struct {
	confPath string
}

func (w *configWhitelist) Get(pk cipher.PubKey) (bool, error) {
	var ok bool
	err := w.open()
	if err != nil {
		return ok, err
	}
	for _, k := range wl {
		if k == pk {
			ok = true
		}
	}
	return ok, nil
}

func (w *configWhitelist) All() (map[cipher.PubKey]bool, error) {
	err := w.open()
	if err != nil {
		return nil, err
	}
	out := make(map[cipher.PubKey]bool)
	for _, k := range wl {
		out[k] = true
	}
	return out, nil
}

func (w *configWhitelist) Add(pks ...cipher.PubKey) error {
	err := w.open()
	if err != nil {
		return err
	}
	// duplicate flag
	var dFlag bool

	// append new pks to the whitelist slice within the config file
	// for each pk to be added
	var pke []string
	for _, k := range pks {

		dFlag = false
		// check if the pk already exists
		for _, p := range wl {

			// if it does
			if p == k {
				// flag it
				dFlag = true
				pke = append(pke, p.String())
				fmt.Printf("skipping append for %v. Already exists", k)
				break
			}
		}

		// if pk does already not exist
		if !dFlag {
			// append it
			wl = append(wl, k)
			conf.WL = append(conf.WL, k.Hex())
		}

	}

	// write the changes back to the config file
	err = updateFile(w.confPath)
	if err != nil {
		log.Println("unable to update config file")
		return err
	}
	if len(pke) != 0 {
		return errors.New("skipping append for " + strings.Join(pke, ",") + ". Already exists")
	}
	return nil
}

func (w *configWhitelist) Remove(pks ...cipher.PubKey) error {
	err := w.open()
	if err != nil {
		return err
	}

	// for each pubkey to be removed
	for _, k := range pks {

		// find occurrence of pubkey in config whitelist
		for i := 0; i < len(wl); i++ {

			// if an occurrence is found
			if k == wl[i] {
				// remove element
				wl = append(wl[:i], wl[i+1:]...)
				conf.WL = append(conf.WL[:i], conf.WL[i+1:]...)
				break
			}
		}
	}
	// write changes back to the config file
	err = updateFile(w.confPath)
	if err != nil {
		log.Println("unable to update config file")
		return err
	}
	return nil
}

func (w *configWhitelist) open() error {
	info, err := os.Stat(w.confPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			_, err = os.Create(w.confPath)
			if err != nil {
				return err
			}
		}
		return err
	}

	if info.Size() == 0 {
		if err = updateFile(w.confPath); err != nil {
			return err
		}
	}

	// read file
	file, err := os.ReadFile(w.confPath)
	if err != nil {
		return err
	}
	// store config.json into conf to manipulate whitelists
	err = json.Unmarshal(file, &conf)
	if err != nil {
		return err
	}
	// convert []string to cipher.PubKeys
	if len(conf.WL) > 0 {
		ustString := strings.Join(conf.WL, ",")
		if err := wl.Set(ustString); err != nil {
			return err
		}
	}
	return nil
}

// updateFile writes changes to config file
func updateFile(confPath string) error {

	// marshal content
	b, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return err
	}
	// write to config file
	err = os.WriteFile(confPath, b, 0600)
	if err != nil {
		return err
	}

	return nil
}

// NewMemoryWhitelist creates a memory implementation of a whitelist.
func NewMemoryWhitelist() Whitelist {
	return &memoryWhitelist{
		m: make(map[cipher.PubKey]struct{}),
	}
}

type memoryWhitelist struct {
	m   map[cipher.PubKey]struct{}
	mux sync.RWMutex
}

func (w *memoryWhitelist) Get(pk cipher.PubKey) (bool, error) {
	w.mux.RLock()
	_, ok := w.m[pk]
	w.mux.RUnlock()
	return ok, nil
}

func (w *memoryWhitelist) All() (map[cipher.PubKey]bool, error) {
	out := make(map[cipher.PubKey]bool)
	w.mux.RLock()
	for k := range w.m {
		out[k] = true
	}
	w.mux.RUnlock()
	return out, nil
}

func (w *memoryWhitelist) Add(pks ...cipher.PubKey) error {
	w.mux.Lock()
	for _, pk := range pks {
		w.m[pk] = struct{}{}
	}
	w.mux.Unlock()
	return nil
}

func (w *memoryWhitelist) Remove(pks ...cipher.PubKey) error {
	w.mux.Lock()
	for _, pk := range pks {
		delete(w.m, pk)
	}
	w.mux.Unlock()
	return nil
}
