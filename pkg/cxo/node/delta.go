// Package node pkg/cxo/node/delta.go c2-net-cxo
package node

import (
	"time"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
	"github.com/skycoin/skywire/pkg/cxo/node/msg"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// A fill asks the peer that sent the Root for the objects new in it, so it
// does not pay one round trip per tree level. Whatever the reply lacks is
// fetched one object at a time as before.
const (
	deltaMaxObjects = 256       // objects in one Objects reply
	deltaMaxBytes   = 512 << 10 // bytes of values in one Objects reply
	deltaWait       = 5 * time.Second
	deltaFillWait   = 4 * time.Second // a peer still filling the Root waits this long for it
)

const (
	deltaUnknown int32 = iota
	deltaYes
	deltaNo
)

// probeDelta asks the peer whether it serves RqDelta. An older peer answers
// the probe as an unknown object and is never sent an RqDelta.
func (c *Conn) probeDelta() {
	if c.n.noDelta {
		return
	}
	reply, err := c.sendRequest(&msg.RqObject{Key: msg.DeltaProbe})
	if err != nil {
		return
	}
	if e, ok := reply.(*msg.Err); ok && e.Err == msg.DeltaProbeReply {
		c.deltaCap.Store(deltaYes)
		return
	}
	c.deltaCap.Store(deltaNo)
}

// deltaSupported reports whether the peer answered the probe as a peer that
// serves RqDelta.
func (c *Conn) deltaSupported() bool {
	return c.deltaCap.Load() == deltaYes
}

func (c *Conn) handleRqDelta(seq uint32, rq *msg.RqDelta) {
	defer c.await.Done()

	var wait time.Duration
	switch last, err := c.n.c.LastRootSeq(rq.Feed, rq.Nonce); {
	case err == data.ErrNoSuchFeed:
	case err != nil || last < rq.Seq:
		wait = deltaFillWait // we announced the Root and are filling it
	}

	keys, _ := c.n.c.WaitDeltaKeys(rq.Feed, rq.Nonce, rq.Since, rq.Seq,
		deltaMaxObjects, wait)

	var (
		vals [][]byte
		size int
	)
	for _, key := range keys {
		val, _, err := c.n.c.Get(key, 0)
		if err != nil {
			continue
		}
		if size+len(val) > deltaMaxBytes {
			break
		}
		size += len(val)
		vals = append(vals, val)
	}

	c.sendMsg(c.nextSeq(), seq, &msg.Objects{Values: vals}) //nolint:errcheck,gosec
}

// requestDelta fetches the objects new in r from c and hands them to the
// head loop, which serves the fill's requests from them.
func (f *fillHead) requestDelta(c *Conn, fill uint64, r *registry.Root, since uint64) {
	defer f.await.Done()

	objs := make(map[cipher.SHA256][]byte)
	reply, err := c.sendRequestTimeout(&msg.RqDelta{
		Feed:  r.Pub,
		Nonce: r.Nonce,
		Since: since,
		Seq:   r.Seq,
	}, deltaWait)
	if err == nil {
		if x, ok := reply.(*msg.Objects); ok {
			for _, v := range x.Values {
				objs[cipher.SumSHA256(v)] = v
			}
		}
	}
	f.node().Debugf(FillPin, "[fill] delta from [%s] %s: %d objects, %v",
		c.String(), r.Short(), len(objs), err)

	select {
	case f.deltaq <- deltaReply{fill, objs}:
	case <-f.closeq:
	}
}

type deltaReply struct {
	fill uint64
	objs map[cipher.SHA256][]byte
}

// startDelta asks the source of cr for its delta when it serves one. Requests
// wait in rqo until the reply arrives.
func (f *fillHead) startDelta(cr connRoot) {
	if cr.c == nil || !cr.c.deltaSupported() {
		return
	}
	var since uint64
	if last, err := f.node().c.LastRootSeq(cr.r.Pub, cr.r.Nonce); err == nil {
		since = last + 1
	}
	f.delta = true
	f.inflight++
	f.await.Add(1)
	go f.requestDelta(cr.c, f.fill, cr.r, since)
}

func (f *fillHead) handleDelta(dr deltaReply) {
	f.inflight--
	if dr.fill != f.fill || f.rqo == nil {
		return // closeFiller race; see handleRequest
	}
	f.delta = false
	f.pre = dr.objs

	for e := f.rqo.Front(); e != nil; {
		next := e.Next()
		if f.servePre(e.Value.(cipher.SHA256)) {
			f.rqo.Remove(e)
		}
		e = next
	}
	f.triggerRequest()
}

// servePre stores a wanted object from the delta, reporting false when the
// delta does not hold it and it must be requested.
func (f *fillHead) servePre(key cipher.SHA256) bool {
	val, ok := f.pre[key]
	if !ok {
		return false
	}
	delete(f.pre, key)
	if _, err := f.node().c.SetWanted(key, val); err != nil {
		f.node().Errorf(err, "SetWanted for %s failed", key.Hex()[:7])
		return false
	}
	f.rearmStall()
	return true
}
