package hypervisor

import (
	"sync"
	"sync/atomic"

	"github.com/skycoin/skywire/pkg/visor"
)

type chanMux struct {
	finished  uint32
	mu        sync.Mutex
	ch        <-chan visor.StatusMessage
	consumers []chan<- visor.StatusMessage
}

func newChanMux(ch <-chan visor.StatusMessage, consumers []chan<- visor.StatusMessage) *chanMux {
	m := &chanMux{
		ch:        ch,
		consumers: consumers,
	}

	go m.worker()

	return m
}

func (m *chanMux) worker() {
	for message := range m.ch {
		m.mu.Lock()
		consumers := m.consumers
		m.mu.Unlock()

		for _, consumer := range consumers {
			consumer <- message
		}
	}

	atomic.StoreUint32(&m.finished, 1)

	m.mu.Lock()
	consumers := m.consumers
	for _, consumer := range consumers {
		close(consumer)
	}
}

func (m *chanMux) addConsumer(consumer chan<- visor.StatusMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if atomic.LoadUint32(&m.finished) == 0 {
		m.consumers = append(m.consumers, consumer)
	}
}
