package api

import (
	"errors"
	"strings"

	"github.com/dgraph-io/badger/v3"
	json "github.com/json-iterator/go"

	"github.com/skycoin/skywire/pkg/transport"
)

// AddToCache Adds new result to cache
func (a *API) AddToCache(p *PollResult) error {
	if p, _ := a.GetCache(); p == nil { //nolint:errcheck
		return a.cache.Update(func(txn *badger.Txn) error {
			for k, v := range p.Nodes {
				b, err := json.Marshal(v)
				if err != nil {
					return err
				}
				if err = txn.Set([]byte("nodes/"+k), b); err != nil {
					return err
				}
			}

			for _, v := range p.Edges {
				b, err := json.Marshal(v)
				if err != nil {
					return err
				}
				if err = txn.Set([]byte("edges/"+v.ID.URN()), b); err != nil {
					return err
				}
			}
			return nil
		})
	}
	err := a.cache.View(func(txn *badger.Txn) error {
		for k, v := range p.Nodes {
			_, err := txn.Get([]byte("nodes/" + k))
			if err != nil && errors.Is(badger.ErrKeyNotFound, err) {
				b, err := json.Marshal(v)
				if err != nil {
					return err
				}
				if err = a.cache.Update(func(txn *badger.Txn) error {
					return txn.Set([]byte("nodes/"+k), b)
				}); err != nil {
					return err
				}
			}
		}
		for _, v := range p.Edges {
			_, err := txn.Get([]byte("edges/" + v.ID.URN()))
			if err != nil && errors.Is(badger.ErrKeyNotFound, err) {
				b, err := json.Marshal(v)
				if err != nil {
					return err
				}
				if err = a.cache.Update(func(txn *badger.Txn) error {
					return txn.Set([]byte("edges/"+v.ID.URN()), b)
				}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return err
}

// GetCache gets uptime and transport data from cache
func (a *API) GetCache() (*PollResult, error) {
	nodeMap := map[string]visorIPsResponse{}
	var edgeList []*transport.Entry
	err := a.cache.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		prefix := []byte("nodes/")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			err := item.Value(func(v []byte) error {
				var node visorIPsResponse
				if err := json.Unmarshal(v, &node); err != nil {
					return err
				}
				k := strings.Split(string(item.Key()), "/")[1]
				nodeMap[k] = node
				return nil
			})
			if err != nil {
				return err
			}
		}
		prefix = []byte("edges/")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			if err := item.Value(func(val []byte) error {
				var edge transport.Entry
				if err := json.Unmarshal(val, &edge); err != nil {
					return err
				}
				edgeList = append(edgeList, &edge)
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return &PollResult{
		Nodes: nodeMap,
		Edges: edgeList,
	}, err
}
