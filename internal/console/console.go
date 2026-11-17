package console

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	Retention = 15 * time.Minute

	MaxEvents = 500

	MaxBody = 8 << 10

	MaxPayload = 512

	commandPerMinute = 60
	eventsPerMinute  = 240
)

var (
	namePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,16}$`)
	actions     = map[string]bool{"on": true, "off": true, "notify": true, "broadcast": true}
)

type Event struct {
	ID      int64  `json:"id"`
	From    string `json:"from"`
	Action  string `json:"action"`
	Target  string `json:"target"`
	Payload string `json:"payload"`
	Time    int64  `json:"time"`
}

type Config struct {
	Secret string
	Accounts map[string]string
	Store    Store
}

var (
	defaultOnce   sync.Once
	defaultConfig *Config
)

func Default() *Config {
	defaultOnce.Do(func() {
		defaultConfig = FromEnv()
	})
	return defaultConfig
}

func FromEnv() *Config {
	config := &Config{
		Secret:   strings.TrimSpace(os.Getenv("CONSOLE_SECRET")),
		Accounts: map[string]string{},
	}

	for _, entry := range strings.Split(os.Getenv("CONSOLE_ACCOUNTS"), ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		name, display := entry, entry

		if index := strings.Index(entry, ":"); index > 0 {
			name, display = entry[:index], entry[index+1:]
		}

		if namePattern.MatchString(name) {
			config.Accounts[strings.ToLower(name)] = display
		}
	}

	config.Store = storeFromEnv()
	return config
}

func (config *Config) Authorize(request *http.Request) (string, bool) {
	if config == nil || config.Secret == "" {
		return "", false
	}

	name := strings.TrimSpace(request.Header.Get("X-Console-User"))

	if name == "" {
		name = strings.TrimSpace(request.URL.Query().Get("user"))
	}

	if !namePattern.MatchString(name) {
		return "", false
	}

	display, allowed := config.Accounts[strings.ToLower(name)]

	if !allowed {
		return "", false
	}

	given := strings.ToLower(strings.TrimSpace(request.Header.Get("X-Console-Token")))
	expected := config.token(name)

	if subtle.ConstantTimeCompare([]byte(given), []byte(expected)) != 1 {
		return "", false
	}

	return display, true
}

func (config *Config) token(name string) string {
	mac := hmac.New(sha256.New, []byte(config.Secret))
	mac.Write([]byte(strings.ToLower(strings.TrimSpace(name))))
	return hex.EncodeToString(mac.Sum(nil))
}

func (config *Config) Health(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

// Whoami tells an authenticated account whether it may use the console.
func (config *Config) Whoami(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	display, ok := config.Authorize(request)

	if !ok {
		writeError(writer, http.StatusUnauthorized, "unauthorized")
		return
	}

	writeJSON(writer, http.StatusOK, map[string]any{"allowed": true, "name": display})
}

func (config *Config) Command(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	display, ok := config.Authorize(request)

	if !ok {
		writeError(writer, http.StatusUnauthorized, "unauthorized")
		return
	}

	if !allow("command:"+strings.ToLower(display), commandPerMinute) {
		writeError(writer, http.StatusTooManyRequests, "rate limited")
		return
	}

	request.Body = http.MaxBytesReader(writer, request.Body, MaxBody)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()

	var body struct {
		User    string `json:"user"`
		Action  string `json:"action"`
		Target  string `json:"target"`
		Payload string `json:"payload"`
	}

	if err := decoder.Decode(&body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid json")
		return
	}

	action := strings.ToLower(strings.TrimSpace(body.Action))
	target := strings.TrimSpace(body.Target)
	payload := strings.TrimSpace(body.Payload)

	if !actions[action] {
		writeError(writer, http.StatusBadRequest, "unknown action")
		return
	}

	if action == "broadcast" {
		target = "-"
	} else if !namePattern.MatchString(target) {
		writeError(writer, http.StatusBadRequest, "invalid target")
		return
	}

	if !validPayload(payload) {
		writeError(writer, http.StatusBadRequest, "invalid payload")
		return
	}

	id, err := config.Store.Append(display, action, target, payload)

	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store unavailable")
		return
	}

	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (config *Config) Events(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	display, ok := config.Authorize(request)

	if !ok {
		writeError(writer, http.StatusUnauthorized, "unauthorized")
		return
	}

	if !allow("events:"+strings.ToLower(display), eventsPerMinute) {
		writeError(writer, http.StatusTooManyRequests, "rate limited")
		return
	}

	since := parseInt64(request.URL.Query().Get("since"))
	events, last, err := config.Store.Since(strings.ToLower(display), since, time.Now().Add(-Retention))

	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store unavailable")
		return
	}

	if events == nil {
		events = []Event{}
	}

	writeJSON(writer, http.StatusOK, map[string]any{"events": events, "last": last})
}

func validPayload(payload string) bool {
	if payload == "" || utf8.RuneCountInString(payload) > MaxPayload {
		return false
	}

	for _, character := range payload {
		if character < 0x20 || character == 0x7F {
			return false
		}
	}

	return true
}

func parseInt64(raw string) int64 {
	var value int64

	for _, character := range raw {
		if character < '0' || character > '9' {
			return 0
		}

		value = value*10 + int64(character-'0')

		if value > 1<<62 {
			return 0
		}
	}

	return value
}

/* best effort rate limiting: per process, which is enough for this API's traffic */

var (
	bucketsMu sync.Mutex
	buckets   = map[string]*bucket{}
)

type bucket struct {
	window int64
	count  int
}

func allow(key string, limit int) bool {
	now := time.Now().Unix() / 60

	bucketsMu.Lock()
	defer bucketsMu.Unlock()

	current, ok := buckets[key]

	if !ok || current.window != now {
		buckets[key] = &bucket{window: now, count: 1}
		return true
	}

	if current.count >= limit {
		return false
	}

	current.count++
	return true
}

func writeJSON(writer http.ResponseWriter, status int, body any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(body)
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, map[string]any{"error": message})
}
