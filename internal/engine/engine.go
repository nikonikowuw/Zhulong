package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../native/include
#cgo darwin LDFLAGS: -L${SRCDIR}/../../build/native -lZhulongEngine -lc++
#cgo linux LDFLAGS: -L${SRCDIR}/../../build/native -lZhulongEngine -lstdc++ -pthread
#include <Zhulong/engine.h>
*/
import "C"

import (
	"fmt"
	"sync"
)

// Engine owns an opaque C++ handle. Calls are serialized so Close cannot race
// with a lifecycle operation.
type Engine struct {
	mu      sync.Mutex
	handle  C.Zhulong_engine_h
	running bool
}

// New creates an uninitialized native engine wrapper.
func New() *Engine {
	return &Engine{}
}

// Start creates and starts the native engine. Repeated calls are safe.
func (e *Engine) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == nil {
		var handle C.Zhulong_engine_h
		if status := C.Zhulong_engine_create(&handle); status != C.Zhulong_OK {
			return nativeError("create", status)
		}
		e.handle = handle
	}

	if status := C.Zhulong_engine_start(e.handle); status != C.Zhulong_OK {
		C.Zhulong_engine_destroy(e.handle)
		e.handle = nil
		e.running = false
		return nativeError("start", status)
	}
	e.running = true
	return nil
}

// Stop stops the native engine if it has been started.
func (e *Engine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == nil {
		return nil
	}
	status := C.Zhulong_engine_stop(e.handle)
	e.running = false
	if status != C.Zhulong_OK {
		return nativeError("stop", status)
	}
	return nil
}

// Ready reports whether the native engine is running.
func (e *Engine) Ready() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

// Close stops and destroys the C-owned handle. Calling Close more than once is safe.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == nil {
		return nil
	}

	status := C.Zhulong_engine_stop(e.handle)
	C.Zhulong_engine_destroy(e.handle)
	e.handle = nil
	e.running = false
	if status != C.Zhulong_OK {
		return nativeError("stop", status)
	}
	return nil
}

func nativeError(operation string, status C.Zhulong_status_t) error {
	return fmt.Errorf("native engine %s failed with status %d", operation, int(status))
}
