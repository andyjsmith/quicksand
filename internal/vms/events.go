package vms

import "sync"

// Hub fans out VM change notifications to subscribers (the per-user event
// streams). A notification only carries the VM ID; subscribers re-read state.
type Hub struct {
	mu   sync.Mutex
	subs map[chan Event]int64
}

// Event says a VM owned by OwnerID changed or, if Deleted, is gone.
type Event struct {
	VMID    string
	OwnerID int64
	Deleted bool
}

func NewHub() *Hub {
	return &Hub{subs: map[chan Event]int64{}}
}

// Subscribe returns a channel of events for VMs owned by ownerID (0 for
// all VMs) and a function to unsubscribe.
func (h *Hub) Subscribe(ownerID int64) (<-chan Event, func()) {
	ch := make(chan Event, 64)
	h.mu.Lock()
	h.subs[ch] = ownerID
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

func (h *Hub) Publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch, owner := range h.subs {
		if owner != 0 && owner != e.OwnerID {
			continue
		}
		// Slow subscribers miss intermediate updates rather than blocking
		// deployments; the next event or a refresh catches them up.
		select {
		case ch <- e:
		default:
		}
	}
}
