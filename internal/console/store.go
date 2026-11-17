package console

import (
	"strings"
	"sync"
	"time"
)

// Store keeps console events between requests.
//
// Serverless deployments must use a shared store: every function instance has its own memory, so a
// command posted to one instance would never be seen by a poll hitting another. MongoDB is that
// shared store; the in-memory one exists for local runs.
type Store interface {
	Append(from, action, target, payload string) (int64, error)
	Since(target string, since int64, cutoff time.Time) ([]Event, int64, error)
}

func storeFromEnv(uri string) (Store, error) {
	if strings.TrimSpace(uri) != "" {
		return newMongoStore(uri)
	}

	return &memoryStore{}, nil
}

/* memory store: local development and single instance runs */

type memoryStore struct {
	mu     sync.Mutex
	events []Event
	seq    int64
}

func (store *memoryStore) Append(from, action, target, payload string) (int64, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	store.seq++
	event := Event{ID: store.seq, From: from, Action: action, Target: target, Payload: payload, Time: time.Now().UnixMilli()}
	store.events = append(store.events, event)

	cutoff := time.Now().Add(-Retention).UnixMilli()
	kept := store.events[:0]

	for _, existing := range store.events {
		if existing.Time >= cutoff {
			kept = append(kept, existing)
		}
	}

	store.events = kept

	if len(store.events) > MaxEvents {
		store.events = store.events[len(store.events)-MaxEvents:]
	}

	return event.ID, nil
}

func (store *memoryStore) Since(target string, since int64, cutoff time.Time) ([]Event, int64, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	events := make([]Event, 0, len(store.events))
	last := since

	for _, event := range store.events {
		if event.ID <= since || event.Time < cutoff.UnixMilli() {
			continue
		}

		if event.Action != "broadcast" && !strings.EqualFold(event.Target, target) {
			continue
		}

		events = append(events, event)

		if event.ID > last {
			last = event.ID
		}
	}

	return events, last, nil
}
