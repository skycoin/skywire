package skyssh

import "sync"

type chanList struct {
	sync.Mutex

	chans []*skysshChannel
}

func newChanList() *chanList {
	return &chanList{chans: []*skysshChannel{}}
}

func (c *chanList) add(sshCh *skysshChannel) uint32 {
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

func (c *chanList) getChannel(id uint32) *skysshChannel {
	c.Lock()
	defer c.Unlock()

	if id < uint32(len(c.chans)) {
		return c.chans[id]
	}

	return nil
}

func (c *chanList) dropAll() []*skysshChannel {
	c.Lock()
	defer c.Unlock()
	var r []*skysshChannel

	for _, ch := range c.chans {
		if ch == nil {
			continue
		}
		r = append(r, ch)
	}
	c.chans = nil
	return r
}
