package events

import "testing"

func TestBrokerKeepsBoundedHistory(t *testing.T) {
	t.Parallel()

	broker := NewBroker(2, 1)
	for value := 1; value <= 3; value++ {
		if _, err := broker.Publish("test", map[string]int{"value": value}); err != nil {
			t.Fatalf("Publish() error = %v", err)
		}
	}

	history := broker.History()
	if len(history) != 2 {
		t.Fatalf("history length = %d, want 2", len(history))
	}
	if history[0].ID != 2 || history[1].ID != 3 {
		t.Fatalf("history IDs = [%d, %d], want [2, 3]", history[0].ID, history[1].ID)
	}
}

func TestBrokerDropsOldestPendingSubscriberEvent(t *testing.T) {
	t.Parallel()

	broker := NewBroker(2, 1)
	stream, unsubscribe := broker.Subscribe()
	defer unsubscribe()

	if _, err := broker.Publish("test", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Publish("test", 2); err != nil {
		t.Fatal(err)
	}

	event := <-stream
	if event.ID != 2 {
		t.Fatalf("subscriber received event %d, want newest event 2", event.ID)
	}
}
