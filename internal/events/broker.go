package events

import (
	"encoding/json"
	"sync"
	"time"
)

type Event struct {
	ID        uint64          `json:"id"`
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

type Broker struct {
	mu               sync.Mutex
	nextID           uint64
	history          []Event
	historyCapacity  int
	subscriberBuffer int
	subscribers      map[uint64]chan Event
	nextSubscriberID uint64
}

func NewBroker(historyCapacity, subscriberBuffer int) *Broker {
	if historyCapacity < 1 {
		historyCapacity = 1
	}
	if subscriberBuffer < 1 {
		subscriberBuffer = 1
	}

	return &Broker{
		historyCapacity:  historyCapacity,
		subscriberBuffer: subscriberBuffer,
		subscribers:      make(map[uint64]chan Event),
	}
}

func (b *Broker) Publish(eventType string, payload any) (Event, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextID++
	event := Event{
		ID:        b.nextID,
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Payload:   encoded,
	}

	if len(b.history) == b.historyCapacity {
		copy(b.history, b.history[1:])
		b.history[len(b.history)-1] = event
	} else {
		b.history = append(b.history, event)
	}

	for _, subscriber := range b.subscribers {
		select {
		case subscriber <- event:
		default:
			select {
			case <-subscriber:
			default:
			}
			select {
			case subscriber <- event:
			default:
			}
		}
	}

	return event, nil
}

func (b *Broker) Subscribe() (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextSubscriberID++
	id := b.nextSubscriberID
	stream := make(chan Event, b.subscriberBuffer)
	b.subscribers[id] = stream

	var once sync.Once
	return stream, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subscribers, id)
			close(stream)
			b.mu.Unlock()
		})
	}
}

func (b *Broker) History() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	history := make([]Event, len(b.history))
	copy(history, b.history)
	return history
}
