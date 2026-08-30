package realtime

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

const CommunicationChanged = "communication.changed"

const (
	ChangeCreated  = "created"
	ChangeMessage  = "message"
	ChangeRead     = "read"
	ChangeClosed   = "closed"
	ChangeReopened = "reopened"
)

type Event struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	ResourceID string    `json:"resource_id"`
	Change     string    `json:"change"`
	OccurredAt time.Time `json:"occurred_at"`
}

func NewEvent(eventType, resourceID, change string) Event {
	return Event{ID: newID(), Type: eventType, ResourceID: resourceID, Change: change, OccurredAt: time.Now().UTC()}
}

type Hub struct {
	mu          sync.Mutex
	bufferSize  int
	nextID      uint64
	subscribers map[string]map[uint64]*subscriber
}

type subscriber struct {
	id     uint64
	userID string
	events chan Event
	done   chan struct{}
}

type Subscription struct {
	Events <-chan Event
	Done   <-chan struct{}
	close  func()
	once   sync.Once
}

func NewHub(bufferSize int) *Hub {
	if bufferSize < 1 {
		bufferSize = 64
	}
	return &Hub{bufferSize: bufferSize, subscribers: make(map[string]map[uint64]*subscriber)}
}

func (h *Hub) Subscribe(userID string) *Subscription {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	item := &subscriber{id: h.nextID, userID: userID, events: make(chan Event, h.bufferSize), done: make(chan struct{})}
	if h.subscribers[userID] == nil {
		h.subscribers[userID] = make(map[uint64]*subscriber)
	}
	h.subscribers[userID][item.id] = item
	result := &Subscription{Events: item.events, Done: item.done}
	result.close = func() { h.unsubscribe(item) }
	return result
}

func (s *Subscription) Close() {
	if s == nil || s.close == nil {
		return
	}
	s.once.Do(s.close)
}

func (h *Hub) Publish(userIDs []string, event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := make(map[string]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID == "" {
			continue
		}
		if _, exists := seen[userID]; exists {
			continue
		}
		seen[userID] = struct{}{}
		for id, item := range h.subscribers[userID] {
			select {
			case item.events <- event:
			default:
				close(item.done)
				delete(h.subscribers[userID], id)
			}
		}
		if len(h.subscribers[userID]) == 0 {
			delete(h.subscribers, userID)
		}
	}
}

func (h *Hub) unsubscribe(item *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	items := h.subscribers[item.userID]
	current, exists := items[item.id]
	if !exists || current != item {
		return
	}
	delete(items, item.id)
	close(item.done)
	if len(items) == 0 {
		delete(h.subscribers, item.userID)
	}
}

func newID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		stamp := time.Now().UTC().Format("20060102150405.000000000")
		return stamp
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}
