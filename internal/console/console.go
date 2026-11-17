// Package console implements the Revoke console API.
//
// The API is the whole control plane for the client's console. Who may do what comes from
// ServerData.go at the module root; each account there proves itself with its own token, so a
// request carrying somebody else's name is refused even though the whitelist is public.
package console

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	serverdata "github.com/BenjYahu/Console"
)

const (
	// Retention is how long a command stays deliverable.
	Retention = 15 * time.Minute

	// MaxEvents bounds how many commands are kept.
	MaxEvents = 500

	// MaxBody caps a request body, so an oversized post cannot be used as a memory hammer.
	MaxBody = 8 << 10

	// MaxPayload caps the message/feature carried by a command.
	MaxPayload = 512

	commandPerMinute = 60
	eventsPerMinute  = 240
)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,16}$`)

// Action names the client understands. ServerData.go decides which of them are switched on.
var actions = []string{"on", "off", "notify", "broadcast"}

// Event is one console command, addressed to a target or to everybody (broadcast).
type Event struct {
	ID      int64  `json:"id"`
	From    string `json:"from"`
	Action  string `json:"action"`
	Target  string `json:"target"`
	Payload string `json:"payload"`
	Time    int64  `json:"time"`
}

// Account is one entry of ServerData.go with its token already read from the environment.
type Account struct {
	// Display is the name other players see on broadcasts.
	Display string
	// Username is the Minecraft name this account must send.
	Username string
	token    string
	// commands is nil when the account may use every enabled command.
	commands map[string]bool
}

func (account Account) allows(action string) bool {
	return account.commands == nil || account.commands[action]
}

// Config is everything the API needs to answer a request.
type Config struct {
	accounts map[string]Account // lower-cased username -> account
	enabled  map[string]bool
	// Store keeps events between requests.
	Store Store
	// Shared is true when a shared store (KV/Upstash) is configured.
	Shared bool
	// ProfileURL overrides Minecraft's profile endpoint (used by tests); empty means the real one.
	ProfileURL string
	// Client makes that verification call; nil means a shared client.
	Client *http.Client
}

var (
	defaultOnce   sync.Once
	defaultConfig *Config
)

// Default returns the process-wide configuration, built once per instance.
func Default() *Config {
	defaultOnce.Do(func() {
		defaultConfig = FromEnv()
	})
	return defaultConfig
}

// FromEnv resolves ServerData.go against the environment: each account's token comes from its
// TokenEnv variable (or CONSOLE_TOKEN_<USERNAME>), never from the repository.
func FromEnv() *Config {
	config := &Config{
		accounts: map[string]Account{},
		enabled:  map[string]bool{},
	}

	for _, action := range serverdata.EnabledCommands {
		action = strings.ToLower(strings.TrimSpace(action))
		if isAction(action) {
			config.enabled[action] = true
		}
	}

	for _, entry := range serverdata.Accounts {
		username := strings.TrimSpace(entry.Username)

		if !namePattern.MatchString(username) {
			continue
		}

		display := strings.TrimSpace(entry.DisplayName)

		if display == "" {
			display = username
		}

		account := Account{
			Display:  display,
			Username: username,
			token:    strings.TrimSpace(os.Getenv(tokenVariable(entry))),
		}

		if len(entry.Commands) > 0 {
			account.commands = map[string]bool{}

			for _, action := range entry.Commands {
				account.commands[strings.ToLower(strings.TrimSpace(action))] = true
			}
		}

		config.accounts[strings.ToLower(username)] = account
	}

	config.Store = storeFromEnv()
	_, config.Shared = config.Store.(*redisStore)
	return config
}

// tokenVariable is the environment variable holding an account's token.
func tokenVariable(entry serverdata.Account) string {
	if name := strings.TrimSpace(entry.TokenEnv); name != "" {
		return name
	}

	return "CONSOLE_TOKEN_" + strings.ToUpper(strings.TrimSpace(entry.Username))
}

// Warnings describes configuration that will silently refuse everybody, for the server's log.
func (config *Config) Warnings() []string {
	var warnings []string

	if len(config.accounts) == 0 {
		warnings = append(warnings, "ServerData.go lists no usable account: every console command will be refused")
	}

	if len(config.enabled) == 0 {
		warnings = append(warnings, "ServerData.go enables no command: every console command will be refused")
	}

	for _, entry := range serverdata.Accounts {
		username := strings.TrimSpace(entry.Username)

		if !namePattern.MatchString(username) {
			warnings = append(warnings, "ServerData.go skips an account with an invalid Minecraft name: "+username)
			continue
		}

		if strings.TrimSpace(os.Getenv(tokenVariable(entry))) == "" {
			warnings = append(warnings, "no fallback token set for "+username+" ("+tokenVariable(entry)+"): only a verified Minecraft login can use that account")
		}
	}

	return warnings
}

// Authorize returns the account behind a request.
//
// Being listed in ServerData.go is what grants access; authorizing then comes down to proving the
// request really is that account:
//
//   - the client's Minecraft login is checked against Minecraft's profile service, so naming
//     somebody else fails even though the whitelist is public, and
//   - an optional per-account token from the environment is accepted as a fallback for installs
//     whose launcher has no live login to verify.
//
// The name in the request only selects the entry. Nothing the caller puts in the request body, and
// no name alone, can authorize anything.
func (config *Config) Authorize(request *http.Request) (Account, bool) {
	if config == nil {
		return Account{}, false
	}

	name := strings.TrimSpace(request.Header.Get("X-Console-User"))

	if name == "" {
		name = strings.TrimSpace(request.URL.Query().Get("user"))
	}

	if !namePattern.MatchString(name) {
		return Account{}, false
	}

	account, known := config.accounts[strings.ToLower(name)]

	if !known {
		return Account{}, false
	}

	if tokenMatches(account.token, strings.TrimSpace(request.Header.Get("X-Console-Token"))) {
		return account, true
	}

	if config.VerifySession(request.Context(), account.Username, strings.TrimSpace(request.Header.Get("X-Console-Session"))) {
		return account, true
	}

	return Account{}, false
}

func tokenMatches(expected, given string) bool {
	if expected == "" || given == "" {
		return false
	}

	expectedDigest := sha256.Sum256([]byte(expected))
	givenDigest := sha256.Sum256([]byte(given))
	return subtle.ConstantTimeCompare(expectedDigest[:], givenDigest[:]) == 1
}

/* handlers */

// Health is an unauthenticated liveness probe that reveals nothing.
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

	account, ok := config.Authorize(request)

	if !ok {
		writeError(writer, http.StatusUnauthorized, "unauthorized")
		return
	}

	writeJSON(writer, http.StatusOK, map[string]any{"allowed": true, "name": account.Display})
}

// Command stores a console command for delivery to its target.
func (config *Config) Command(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	account, ok := config.Authorize(request)

	if !ok {
		writeError(writer, http.StatusUnauthorized, "unauthorized")
		return
	}

	if !allow("command:"+strings.ToLower(account.Username), commandPerMinute) {
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

	if !isAction(action) {
		writeError(writer, http.StatusBadRequest, "unknown action")
		return
	}

	// The signed-in account decides what may be sent; the "user" field in the body is ignored.
	if !config.enabled[action] || !account.allows(action) {
		writeError(writer, http.StatusForbidden, "command not allowed")
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

	id, err := config.Store.Append(account.Display, action, target, payload)

	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store unavailable")
		return
	}

	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "id": id})
}

// Events returns the commands addressed to the authenticated player, newest cursor included.
func (config *Config) Events(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	account, ok := config.Authorize(request)

	if !ok {
		writeError(writer, http.StatusUnauthorized, "unauthorized")
		return
	}

	if !allow("events:"+strings.ToLower(account.Username), eventsPerMinute) {
		writeError(writer, http.StatusTooManyRequests, "rate limited")
		return
	}

	since := parseInt64(request.URL.Query().Get("since"))
	events, last, err := config.Store.Since(strings.ToLower(account.Username), since, time.Now().Add(-Retention))

	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "store unavailable")
		return
	}

	if events == nil {
		events = []Event{}
	}

	writeJSON(writer, http.StatusOK, map[string]any{"events": events, "last": last})
}

/* validation and plumbing */

func isAction(action string) bool {
	for _, known := range actions {
		if action == known {
			return true
		}
	}

	return false
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
