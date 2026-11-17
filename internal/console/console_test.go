package console

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testSecret = "unit-test-secret"

func newTestConfig() *Config {
	return &Config{
		Secret:   testSecret,
		Accounts: map[string]string{"poop656": "poop656", "lain": "lain"},
		Store:    &memoryStore{},
	}
}

func sign(name string) string {
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte(strings.ToLower(name)))
	return hex.EncodeToString(mac.Sum(nil))
}

func request(method, path, name, token string) *http.Request {
	target := path

	if name != "" {
		target += "?user=" + name
	}

	return httptest.NewRequest(method, target, nil)
}

func authed(method, path, name string) *http.Request {
	recorder := request(method, path, name, "")
	recorder.Header.Set("X-Console-User", name)
	recorder.Header.Set("X-Console-Token", sign(name))
	return recorder
}

func resetBuckets() {
	bucketsMu.Lock()
	buckets = map[string]*bucket{}
	bucketsMu.Unlock()
}

func TestWhoami(t *testing.T) {
	config := newTestConfig()

	cases := []struct {
		name   string
		req    *http.Request
		status int
	}{
		{"no token", request(http.MethodGet, "/v1/whoami", "poop656", ""), http.StatusUnauthorized},
		{"wrong token", func() *http.Request {
			r := request(http.MethodGet, "/v1/whoami", "poop656", "")
			r.Header.Set("X-Console-User", "poop656")
			r.Header.Set("X-Console-Token", strings.Repeat("0", 64))
			return r
		}(), http.StatusUnauthorized},
		{"not whitelisted", authed(http.MethodGet, "/v1/whoami", "randomguy"), http.StatusUnauthorized},
		{"case insensitive", authed(http.MethodGet, "/v1/whoami", "Poop656"), http.StatusOK},
	}

	for _, testCase := range cases {
		recorder := httptest.NewRecorder()
		config.Whoami(recorder, testCase.req)

		if recorder.Code != testCase.status {
			t.Fatalf("%s: got %d, want %d", testCase.name, recorder.Code, testCase.status)
		}
	}
}

func TestHealthNeedsNoAuth(t *testing.T) {
	recorder := httptest.NewRecorder()
	newTestConfig().Health(recorder, request(http.MethodGet, "/v1/health", "", ""))

	if recorder.Code != http.StatusOK {
		t.Fatalf("health: got %d", recorder.Code)
	}
}

func TestNoSecretFailsClosed(t *testing.T) {
	config := &Config{Accounts: map[string]string{"poop656": "poop656"}, Store: &memoryStore{}}
	recorder := httptest.NewRecorder()
	config.Whoami(recorder, authed(http.MethodGet, "/v1/whoami", "poop656"))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("empty secret must refuse: got %d", recorder.Code)
	}
}

func TestCommandValidation(t *testing.T) {
	config := newTestConfig()

	bodies := []struct {
		name string
		body string
		code int
	}{
		{"bad action", `{"user":"poop656","action":"nuke","target":"lain","payload":"x"}`, http.StatusBadRequest},
		{"bad target", `{"user":"poop656","action":"off","target":"not a name!","payload":"Flight"}`, http.StatusBadRequest},
		{"empty payload", `{"user":"poop656","action":"off","target":"lain","payload":""}`, http.StatusBadRequest},
		{"too long", `{"user":"poop656","action":"off","target":"lain","payload":"` + strings.Repeat("a", MaxPayload+1) + `"}`, http.StatusBadRequest},
		{"unknown field", `{"user":"poop656","action":"off","target":"lain","payload":"x","admin":true}`, http.StatusBadRequest},
		{"valid", `{"user":"poop656","action":"off","target":"lain","payload":"Flight"}`, http.StatusOK},
		{"broadcast", `{"user":"poop656","action":"broadcast","target":"ignored","payload":"hello all"}`, http.StatusOK},
	}

	for _, testCase := range bodies {
		resetBuckets()
		request := authed(http.MethodPost, "/v1/command", "poop656")
		request.Body = io.NopCloser(strings.NewReader(testCase.body))

		recorder := httptest.NewRecorder()
		config.Command(recorder, request)

		if recorder.Code != testCase.code {
			t.Fatalf("%s: got %d, want %d (%s)", testCase.name, recorder.Code, testCase.code, recorder.Body.String())
		}
	}
}

func TestEventsAreIsolated(t *testing.T) {
	config := newTestConfig()

	post := func(body string) {
		request := authed(http.MethodPost, "/v1/command", "poop656")
		request.Body = io.NopCloser(strings.NewReader(body))
		recorder := httptest.NewRecorder()
		config.Command(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("command failed: %d %s", recorder.Code, recorder.Body.String())
		}
	}

	post(`{"user":"poop656","action":"off","target":"lain","payload":"Flight"}`)
	post(`{"user":"poop656","action":"broadcast","target":"-","payload":"everyone"}`)

	collect := func(name string) []Event {
		resetBuckets()
		recorder := httptest.NewRecorder()
		config.Events(recorder, authed(http.MethodGet, "/v1/events?since=0", name))

		var body struct {
			Events []Event `json:"events"`
			Last   int64   `json:"last"`
		}

		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("bad events json: %v", err)
		}

		return body.Events
	}

	lainEvents := collect("lain")
	otherEvents := collect("poop656")

	if len(lainEvents) != 2 {
		t.Fatalf("lain should see the targeted command and the broadcast, saw %d", len(lainEvents))
	}

	if len(otherEvents) != 1 || otherEvents[0].Action != "broadcast" {
		t.Fatalf("poop656 should only see the broadcast, saw %+v", otherEvents)
	}
}

func TestCommandRateLimited(t *testing.T) {
	config := newTestConfig()
	resetBuckets()

	last := http.StatusOK

	for attempt := 0; attempt < commandPerMinute+5; attempt++ {
		request := authed(http.MethodPost, "/v1/command", "poop656")
		request.Body = io.NopCloser(strings.NewReader(`{"user":"poop656","action":"off","target":"lain","payload":"Flight"}`))
		recorder := httptest.NewRecorder()
		config.Command(recorder, request)
		last = recorder.Code
	}

	if last != http.StatusTooManyRequests {
		t.Fatalf("expected rate limiting, last status %d", last)
	}
}
