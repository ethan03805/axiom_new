package events

import "testing"

func TestPublish_AuthoritativeEventWithoutRunID_FansOutWithoutPersistence(t *testing.T) {
	db := testDB(t)
	bus := New(db, testLogger())

	ch, subID := bus.Subscribe(nil)
	defer bus.Unsubscribe(subID)

	if err := bus.Publish(EngineEvent{Type: RecoveryStarted}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case ev := <-ch:
		if ev.Type != RecoveryStarted {
			t.Fatalf("event type = %q, want %q", ev.Type, RecoveryStarted)
		}
	default:
		t.Fatal("expected recovery event to be fanned out without a run id")
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 0 {
		t.Fatalf("persisted events = %d, want 0", count)
	}
}
