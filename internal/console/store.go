package console

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Store keeps console events between requests.
//
// Serverless deployments must use a shared store: every function instance has its own memory, so
// a command posted to one instance would never be seen by a poll hitting another.
type Store interface {
	Append(from, action, target, payload string) (int64, error)
	Since(target string, since int64, cutoff time.Time) ([]Event, int64, error)
}

func storeFromEnv() Store {
	url := firstEnv("KV_REST_API_URL", "UPSTASH_REDIS_REST_URL")
	token := firstEnv("KV_REST_API_TOKEN", "UPSTASH_REDIS_REST_TOKEN")

	if url != "" && token != "" {
		return &redisStore{base: strings.TrimRight(url, "/"), token: token, client: &http.Client{Timeout: 8 * time.Second}}
	}

	return &memoryStore{}
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}

	return ""
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

/* redis store: Vercel KV / Upstash over their REST API, no client library needed */

type redisStore struct {
	base   string
	token  string
	client *http.Client
}

const (
	seqKey    = "console:seq"
	eventsKey = "console:events"
)

func (store *redisStore) Append(from, action, target, payload string) (int64, error) {
	event := Event{ID: 0, From: from, Action: action, Target: target, Payload: payload, Time: time.Now().UnixMilli()}
	encoded, err := json.Marshal(event)

	if err != nil {
		return 0, err
	}

	// One pipeline keeps the id and the event in the same round trip.
	commands := [][]any{
		{"INCR", seqKey},
		{"RPUSH", eventsKey, string(encoded)},
		{"LTRIM", eventsKey, "-" + itoa(MaxEvents), "-1"},
	}

	var results []struct {
		Result int64 `json:"result"`
	}

	if err := store.do(http.MethodPost, "/pipeline", commands, &results); err != nil {
		return 0, err
	}

	if len(results) == 0 {
		return 0, errEmpty
	}

	return results[0].Result, nil
}

func (store *redisStore) Since(target string, since int64, cutoff time.Time) ([]Event, int64, error) {
	var response struct {
		Result []string `json:"result"`
	}

	if err := store.do(http.MethodGet, "/lrange/"+eventsKey+"/0/-1", nil, &response); err != nil {
		return nil, 0, err
	}

	events := make([]Event, 0, len(response.Result))
	last := since

	for _, raw := range response.Result {
		var event Event

		if json.Unmarshal([]byte(raw), &event) != nil {
			continue
		}

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

func (store *redisStore) do(method, path string, payload any, out any) error {
	var body *bytes.Reader

	if payload != nil {
		encoded, err := json.Marshal(payload)

		if err != nil {
			return err
		}

		body = bytes.NewReader(encoded)
	} else {
		body = bytes.NewReader(nil)
	}

	request, err := http.NewRequest(method, store.base+path, body)

	if err != nil {
		return err
	}

	request.Header.Set("Authorization", "Bearer "+store.token)

	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := store.client.Do(request)

	if err != nil {
		return err
	}

	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errStatus
	}

	return json.NewDecoder(response.Body).Decode(out)
}

type storeError string

func (err storeError) Error() string { return string(err) }

const (
	errEmpty  = storeError("empty store response")
	errStatus = storeError("store rejected the request")
)

func itoa(value int) string {
	if value == 0 {
		return "0"
	}

	var digits [20]byte
	index := len(digits)

	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}

	return string(digits[index:])
}
