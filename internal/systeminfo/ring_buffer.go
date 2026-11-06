package systeminfo

import "sync"

// ringBuffer stores a fixed-capacity circular buffer of hardware history points.
// The zero value is not usable: construct it with newRingBuffer.
type ringBuffer struct {
	mu       sync.RWMutex
	capacity int
	data     []HardwareHistoryPoint
	start    int
	count    int
}

// newRingBuffer creates a ring buffer with a fixed capacity.
//
// A non-positive capacity would make the backing slice panic, so it degrades to a
// single-point buffer: the trend chart then shows the newest sample instead of the
// host refusing to start over a configuration mistake.
func newRingBuffer(capacity int) *ringBuffer {
	if capacity < 1 {
		capacity = 1
	}
	return &ringBuffer{
		capacity: capacity,
		data:     make([]HardwareHistoryPoint, capacity),
	}
}

// push appends a point, overwriting the oldest one once the buffer is full.
func (r *ringBuffer) push(point HardwareHistoryPoint) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.count < r.capacity {
		r.data[(r.start+r.count)%r.capacity] = point
		r.count++
		return
	}

	// Full: overwrite the oldest element in place and advance the window.
	r.data[r.start] = point
	r.start = (r.start + 1) % r.capacity
}

// snapshot returns a copy of the points in chronological order (oldest to newest).
// It always returns an initialized slice, never nil.
func (r *ringBuffer) snapshot() []HardwareHistoryPoint {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.count == 0 {
		return []HardwareHistoryPoint{}
	}

	result := make([]HardwareHistoryPoint, r.count)
	for i := range r.count {
		result[i] = r.data[(r.start+i)%r.capacity]
	}
	return result
}

// length returns the number of stored points.
func (r *ringBuffer) length() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.count
}
