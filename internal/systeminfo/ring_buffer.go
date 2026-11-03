package systeminfo

import (
	"sync"
)

// RingBuffer stores a fixed-capacity circular buffer of hardware history points.
type RingBuffer struct {
	mu       sync.RWMutex
	capacity int
	data     []HardwareHistoryPoint
	start    int
	count    int
}

// NewRingBuffer creates a new RingBuffer with a fixed positive capacity.
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = 60
	}
	return &RingBuffer{
		capacity: capacity,
		data:     make([]HardwareHistoryPoint, capacity),
	}
}

// Push appends a new point to the buffer, overwriting the oldest point if full.
func (r *RingBuffer) Push(point HardwareHistoryPoint) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.count < r.capacity {
		idx := (r.start + r.count) % r.capacity
		r.data[idx] = point
		r.count++
		return
	}

	// Overwrite the oldest element at r.start
	r.data[r.start] = point
	r.start = (r.start + 1) % r.capacity
}

// Snapshot returns a copy of points in chronological order (oldest to newest).
// It always returns an initialized slice, never nil.
func (r *RingBuffer) Snapshot() []HardwareHistoryPoint {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.count == 0 {
		return []HardwareHistoryPoint{}
	}

	result := make([]HardwareHistoryPoint, r.count)
	for i := 0; i < r.count; i++ {
		idx := (r.start + i) % r.capacity
		result[i] = r.data[idx]
	}
	return result
}

// Len returns the current number of items stored in the buffer.
func (r *RingBuffer) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.count
}
