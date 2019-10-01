package skyssh

import "sync"

type chanList struct {
	sync.Mutex

	chans []*Channel
}

func newChanList() *chanList {
	return &chanList{chans: []*Channel{}}
}

func (c *chanList) add(sshCh *Channel) uint32 {
	c.Lock()
	defer c.Unlock()

	for i := range c.chans {
		if c.chans[i] == nil {
			c.chans[i] = sshCh
			return uint32(i)
		}
	}

	c.chans = append(c.chans, sshCh)
	return uint32(len(c.chans) - 1)
}

func (c *chanList) getChannel(id uint32) *Channel {
	c.Lock()
	defer c.Unlock()

	if id < uint32(len(c.chans)) {
		return c.chans[id]
	}

	return nil
}

func (c *chanList) dropAll() []*Channel {
	c.Lock()
	defer c.Unlock()
	var r []*Channel

	for _, ch := range c.chans {
		if ch == nil {
			continue
		}
		r = append(r, ch)
	}
	c.chans = nil
	return r
}
