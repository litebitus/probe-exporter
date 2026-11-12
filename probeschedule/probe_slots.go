package probeschedule

import (
	"context"
	"sync"
)

// probeSlots bounds how many probes run at once, a bound a reload may change while probes run.
type probeSlots struct {
	mutex sync.Mutex
	limit int
	taken int
	freed chan struct{}
}

func newProbeSlots() *probeSlots {
	return &probeSlots{freed: make(chan struct{})}
}

func (slots *probeSlots) setLimit(limit int) {
	slots.mutex.Lock()
	defer slots.mutex.Unlock()
	slots.limit = limit
	slots.signalFreed()
}

func (slots *probeSlots) take(ctx context.Context) bool {
	for {
		slots.mutex.Lock()
		if slots.taken < slots.limit {
			slots.taken++
			slots.mutex.Unlock()
			return true
		}
		freed := slots.freed
		slots.mutex.Unlock()
		select {
		case <-ctx.Done():
			return false
		case <-freed:
		}
	}
}

func (slots *probeSlots) free() {
	slots.mutex.Lock()
	defer slots.mutex.Unlock()
	slots.taken--
	slots.signalFreed()
}

func (slots *probeSlots) signalFreed() {
	close(slots.freed)
	slots.freed = make(chan struct{})
}
