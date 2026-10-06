package camera

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

const (
	SSEMaxClients        = 16
	SSEClientQueueSize   = 32
	SSEHeartbeatInterval = 15 * time.Second
)

var (
	ErrSSEMaxClientsReached = errors.New("maximum concurrent SSE clients reached")
)

type EventType string

const (
	EventTypeSnapshot  EventType = "snapshot"
	EventTypeChange    EventType = "change"
	EventTypeHeartbeat EventType = "heartbeat"
)

// EventMessage represents a message pushed over SSE.
type EventMessage struct {
	Event    EventType `json:"event"`
	Sequence uint64    `json:"sequence"`
	Data     any       `json:"data"`
}

// ClientSubscription represents a connected SSE client session.
type ClientSubscription struct {
	id     uint64
	ch     chan *EventMessage
	closed atomic.Bool
}

// EventHub manages SSE client subscribers, sequence generation, and thread-safe event broadcasting.
type EventHub struct {
	mu           sync.RWMutex
	clients      map[uint64]*ClientSubscription
	nextClientID uint64
	sequence     atomic.Uint64
	registry     *StateRegistry
	stopCh       chan struct{}
	stopped      atomic.Bool
}

// NewEventHub creates a new EventHub and starts the background heartbeat loop.
func NewEventHub(registry *StateRegistry) *EventHub {
	hub := &EventHub{
		clients:  make(map[uint64]*ClientSubscription),
		registry: registry,
		stopCh:   make(chan struct{}),
	}
	go hub.heartbeatLoop()
	return hub
}

func (h *EventHub) heartbeatLoop() {
	ticker := time.NewTicker(SSEHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-h.stopCh:
			return
		case <-ticker.C:
			h.BroadcastHeartbeat()
		}
	}
}

// Subscribe registers a new SSE subscriber. Under the registry/hub synchronization boundary,
// it atomically creates the subscription and prepares the initial snapshot event so that
// clients do not suffer from snapshot/increment gaps.
func (h *EventHub) Subscribe() (*ClientSubscription, *EventMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.stopped.Load() {
		return nil, nil, errors.New("event hub is stopped")
	}

	if len(h.clients) >= SSEMaxClients {
		return nil, nil, ErrSSEMaxClientsReached
	}

	h.nextClientID++
	clientID := h.nextClientID

	sub := &ClientSubscription{
		id: clientID,
		ch: make(chan *EventMessage, SSEClientQueueSize),
	}
	h.clients[clientID] = sub

	// Snapshot generation
	seq := h.sequence.Add(1)
	allStates := h.registry.GetAllStates()
	snapshotMsg := &EventMessage{
		Event:    EventTypeSnapshot,
		Sequence: seq,
		Data:     allStates,
	}

	return sub, snapshotMsg, nil
}

// Unsubscribe safely unregisters and closes a client subscription.
func (h *EventHub) Unsubscribe(sub *ClientSubscription) {
	if sub == nil {
		return
	}

	h.mu.Lock()
	delete(h.clients, sub.id)
	h.mu.Unlock()

	if sub.closed.CompareAndSwap(false, true) {
		close(sub.ch)
	}
}

// BroadcastChange emits a state change event for a camera to all connected clients.
// If a client's bounded channel is full (slow consumer), it is disconnected to prevent server-side leak.
func (h *EventHub) BroadcastChange(state *CameraStateInfo) {
	if state == nil || h.stopped.Load() {
		return
	}

	seq := h.sequence.Add(1)
	msg := &EventMessage{
		Event:    EventTypeChange,
		Sequence: seq,
		Data:     state,
	}

	h.mu.RLock()
	var slowClients []*ClientSubscription
	for _, sub := range h.clients {
		select {
		case sub.ch <- msg:
		default:
			slowClients = append(slowClients, sub)
		}
	}
	h.mu.RUnlock()

	// Evict slow clients outside read lock
	for _, slow := range slowClients {
		h.Unsubscribe(slow)
	}
}

// BroadcastHeartbeat emits periodic keep-alive event.
func (h *EventHub) BroadcastHeartbeat() {
	if h.stopped.Load() {
		return
	}

	msg := &EventMessage{
		Event: EventTypeHeartbeat,
		Data: map[string]string{
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		},
	}

	h.mu.RLock()
	for _, sub := range h.clients {
		select {
		case sub.ch <- msg:
		default:
		}
	}
	h.mu.RUnlock()
}

// Close shuts down the hub and unblocks all connected subscribers.
func (h *EventHub) Close() error {
	if !h.stopped.CompareAndSwap(false, true) {
		return nil
	}
	close(h.stopCh)

	h.mu.Lock()
	for _, sub := range h.clients {
		if sub.closed.CompareAndSwap(false, true) {
			close(sub.ch)
		}
	}
	h.clients = make(map[uint64]*ClientSubscription)
	h.mu.Unlock()
	return nil
}

// Ensure interface compliance
var _ io.Closer = (*EventHub)(nil)

// Channel returns the receive-only channel for event messages.
func (s *ClientSubscription) Channel() <-chan *EventMessage {
	return s.ch
}

// FormatSSE formats an EventMessage as valid SSE lines.
func (msg *EventMessage) FormatSSE() ([]byte, error) {
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal SSE data: %w", err)
	}

	if msg.Sequence > 0 {
		return []byte(fmt.Sprintf("event: %s\nid: %d\ndata: %s\n\n", msg.Event, msg.Sequence, dataBytes)), nil
	}
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", msg.Event, dataBytes)), nil
}
