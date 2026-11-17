package console

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	poop656Token = "token-for-poop656"
	lainToken    = "token-for-lain"
)

func newTestConfig() *Config {
	return &Config{
		accounts: map[string]Account{
			"poop656": {Display: "TLine", Username: "poop656", token: poop656Token},
			"lain":    {Display: "Lain", Username: "lain", token: lainToken, commands: map[string]bool{"on": true, "notify": true}},
		},
		enabled: map[string]bool{"on": true, "off": true, "notify": true, "broadcast": true},
		Store:   &memoryStore{},
	}
}

func authed(method, path, name, token string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("X-Console-User", name)
	request.Header.Set("X-Console-Token", token)
	return request
}

func postCommand(config *Config, name, token, body string) *httptest.ResponseRecorder {
	request := authed(http.MethodPost, "/v1/command", name, token)
	request.Body = io.NopCloser(strings.NewReader(body))
	recorder := httptest.NewRecorder()
	config.Command(recorder, request)
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
		{"no token", authed(http.MethodGet, "/v1/whoami", "poop656", ""), http.StatusUnauthorized},
		{"wrong token", authed(http.MethodGet, "/v1/whoami", "poop656", "nope"), http.StatusUnauthorized},
		{"unknown name", authed(http.MethodGet, "/v1/whoami", "randomguy", poop656Token), http.StatusUnauthorized},
		{"case insensitive name", authed(http.MethodGet, "/v1/whoami", "Poop656", poop656Token), http.StatusOK},
	}

	for _, testCase := range cases {
		recorder := httptest.NewRecorder()
		config.Whoami(recorder, testCase.req)

		if recorder.Code != testCase.status {
			t.Fatalf("%s: got %d, want %d", testCase.name, recorder.Code, testCase.status)
		}
	}
}

func TestWhoamiReturnsDisplayName(t *testing.T) {
	recorder := httptest.NewRecorder()
	newTestConfig().Whoami(recorder, authed(http.MethodGet, "/v1/whoami", "poop656", poop656Token))

	if !strings.Contains(recorder.Body.String(), `"name":"TLine"`) {
		t.Fatalf("display name missing: %s", recorder.Body.String())
	}
}

func TestImpersonationIsRefused(t *testing.T) {
	config := newTestConfig()

	// Claiming another whitelisted account with your own token must fail.
	if recorder := postCommand(config, "lain", poop656Token, `{"user":"lain","action":"on","target":"lain","payload":"Flight"}`); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("lain with poop656's token: got %d", recorder.Code)
	}

	// A token that belongs to nobody must fail just the same.
	if recorder := postCommand(config, "poop656", "guess", `{"user":"poop656","action":"off","target":"lain","payload":"Flight"}`); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: got %d", recorder.Code)
	}
}

func TestBodyUserIsIgnored(t *testing.T) {
	config := newTestConfig()

	// poop656 signs the request but the body claims to be lain: the event must be signed as TLine.
	if recorder := postCommand(config, "poop656", poop656Token, `{"user":"lain","action":"notify","target":"lain","payload":"hi"}`); recorder.Code != http.StatusOK {
		t.Fatalf("command failed: %d %s", recorder.Code, recorder.Body.String())
	}

	recorder := httptest.NewRecorder()
	config.Events(recorder, authed(http.MethodGet, "/v1/events?since=0", "lain", lainToken))

	if !strings.Contains(recorder.Body.String(), `"from":"TLine"`) {
		t.Fatalf("event should be signed by the authenticated account: %s", recorder.Body.String())
	}
}

func TestPerAccountCommands(t *testing.T) {
	config := newTestConfig()
	resetBuckets()

	// lain may not broadcast, poop656 may.
	if recorder := postCommand(config, "lain", lainToken, `{"user":"lain","action":"broadcast","target":"-","payload":"hello"}`); recorder.Code != http.StatusForbidden {
		t.Fatalf("lain broadcast: got %d", recorder.Code)
	}

	if recorder := postCommand(config, "poop656", poop656Token, `{"user":"poop656","action":"broadcast","target":"-","payload":"hello"}`); recorder.Code != http.StatusOK {
		t.Fatalf("poop656 broadcast: got %d %s", recorder.Code, recorder.Body.String())
	}

	// lain may notify.
	if recorder := postCommand(config, "lain", lainToken, `{"user":"lain","action":"notify","target":"poop656","payload":"hi"}`); recorder.Code != http.StatusOK {
		t.Fatalf("lain notify: got %d", recorder.Code)
	}
}

func TestGloballyDisabledCommand(t *testing.T) {
	config := newTestConfig()
	delete(config.enabled, "notify")
	resetBuckets()

	if recorder := postCommand(config, "poop656", poop656Token, `{"user":"poop656","action":"notify","target":"lain","payload":"hi"}`); recorder.Code != http.StatusForbidden {
		t.Fatalf("disabled command accepted: got %d", recorder.Code)
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
	}

	for _, testCase := range bodies {
		resetBuckets()

		if recorder := postCommand(config, "poop656", poop656Token, testCase.body); recorder.Code != testCase.code {
			t.Fatalf("%s: got %d, want %d (%s)", testCase.name, recorder.Code, testCase.code, recorder.Body.String())
		}
	}
}

func TestEventsAreIsolated(t *testing.T) {
	config := newTestConfig()

	if recorder := postCommand(config, "poop656", poop656Token, `{"user":"poop656","action":"off","target":"lain","payload":"Flight"}`); recorder.Code != http.StatusOK {
		t.Fatalf("command failed: %d %s", recorder.Code, recorder.Body.String())
	}

	if recorder := postCommand(config, "poop656", poop656Token, `{"user":"poop656","action":"broadcast","target":"-","payload":"everyone"}`); recorder.Code != http.StatusOK {
		t.Fatalf("broadcast failed: %d %s", recorder.Code, recorder.Body.String())
	}

	collect := func(name, token string) []Event {
		resetBuckets()
		recorder := httptest.NewRecorder()
		config.Events(recorder, authed(http.MethodGet, "/v1/events?since=0", name, token))

		var body struct {
			Events []Event `json:"events"`
			Last   int64   `json:"last"`
		}

		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("bad events json: %v", err)
		}

		return body.Events
	}

	lainEvents := collect("lain", lainToken)
	otherEvents := collect("poop656", poop656Token)

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
		last = postCommand(config, "poop656", poop656Token, `{"user":"poop656","action":"off","target":"lain","payload":"Flight"}`).Code
	}

	if last != http.StatusTooManyRequests {
		t.Fatalf("expected rate limiting, last status %d", last)
	}
}
