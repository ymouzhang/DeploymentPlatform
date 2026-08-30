package realtime

import (
	"testing"
	"time"
)

func TestHubScopesEventsByAccountAndDeduplicatesAudience(t *testing.T) {
	hub := NewHub(4)
	first := hub.Subscribe("first")
	defer first.Close()
	firstOtherTab := hub.Subscribe("first")
	defer firstOtherTab.Close()
	second := hub.Subscribe("second")
	defer second.Close()
	event := NewEvent(CommunicationChanged, "thread-1", "message")
	hub.Publish([]string{"first", "first"}, event)
	select {
	case received := <-first.Events:
		if received.ID != event.ID || received.ResourceID != "thread-1" {
			t.Fatalf("event=%+v", received)
		}
	case <-time.After(time.Second):
		t.Fatal("target account did not receive event")
	}
	select {
	case received := <-firstOtherTab.Events:
		if received.ID != event.ID {
			t.Fatalf("other tab event=%+v", received)
		}
	case <-time.After(time.Second):
		t.Fatal("other tab for target account did not receive event")
	}
	select {
	case received := <-first.Events:
		t.Fatalf("duplicate audience produced duplicate event: %+v", received)
	default:
	}
	select {
	case received := <-second.Events:
		t.Fatalf("unrelated account received event: %+v", received)
	default:
	}
}

func TestHubDisconnectsSlowSubscriberAndCleansUp(t *testing.T) {
	hub := NewHub(1)
	subscription := hub.Subscribe("slow")
	hub.Publish([]string{"slow"}, NewEvent(CommunicationChanged, "thread-1", "message"))
	hub.Publish([]string{"slow"}, NewEvent(CommunicationChanged, "thread-1", "read"))
	select {
	case <-subscription.Done:
	case <-time.After(time.Second):
		t.Fatal("slow subscriber was not disconnected")
	}
	subscription.Close()
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if len(hub.subscribers) != 0 {
		t.Fatalf("subscribers were not cleaned up: %+v", hub.subscribers)
	}
}
