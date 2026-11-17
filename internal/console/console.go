
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
	Retention = 15 * time.Minute
	MaxEvents = 500
	MaxBody = 8 << 10
	MaxPayload = 512

	commandPerMinute = 60
	eventsPerMinute  = 240
)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,16}$`)
var actions = []string{"on", "off", "notify", "broadcast"}

type Event struct {
	ID      int64  `json:"id"`
	From    string `json:"from"`
	Action  string `json:"action"`
	Target  string `json:"target"`
	Payload string `json:"payload"`
	Time    int64  `json:"time"`
}

type Account struct {
	Display string
	Username string
	token    string
	commands map[string]bool
}

func (account Account) allows(action string) bool {
	return account.commands == nil || account.commands[action]
}

type Config struct {
	accounts map[string]Account
	enabled  map[string]bool
	Store Store
	Shared bool
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

func tokenVariable(entry serverdata.Account) string {
	if name := strings.TrimSpace(entry.TokenEnv); name != "" {
		return name
	}

	return "CONSOLE_TOKEN_" + strings.ToUpper(strings.TrimSpace(entry.Username))
}

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
			warnings = append(warnings, "no token set for "+username+": export "+tokenVariable(entry))
		}
	}

	return warnings
}

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

	if !known || !tokenMatches(account.token, strings.TrimSpace(request.Header.Get("X-Console-Token"))) {
		return Account{}, false
	}

	return account, true
}

func tokenMatches(expected, given string) bool {
	if expected == "" || given == "" {
		return false
	}

	expectedDigest := sha256.Sum256([]byte(expected))
	givenDigest := sha256.Sum256([]byte(given))
	return subtle.ConstantTimeCompare(expectedDigest[:], givenDigest[:]) == 1
}

func (config *Config) Health(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

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
