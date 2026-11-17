package console

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultProfileURL answers with the account a Minecraft session token belongs to. It is what turns
// "the username is in ServerData.go" into "the real owner of that account is asking": a request can
// claim any name it likes, but only the account holding that login gets the profile back.
const DefaultProfileURL = "https://api.minecraftservices.com/minecraft/profile"

// sessionTTL is how long a verified login is trusted before asking again. Clients poll every two
// seconds, so without this cache the API would hammer Minecraft's session service.
const sessionTTL = 10 * time.Minute

const maxCachedSessions = 4096

var (
	profileClient = &http.Client{Timeout: 6 * time.Second}

	sessionMu    sync.Mutex
	sessionCache = map[string]time.Time{}
)

// VerifySession reports whether the given Minecraft session token belongs to username.
//
// The token itself never reaches ServerData.go, the store or a log: only a fingerprint of it is
// kept, as the cache key of a successful verification.
func (config *Config) VerifySession(ctx context.Context, username, session string) bool {
	if session == "" {
		return false
	}

	key := fingerprint(username, session)

	if cachedSession(key) {
		return true
	}

	url := config.ProfileURL

	if url == "" {
		url = DefaultProfileURL
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)

	if err != nil {
		return false
	}

	request.Header.Set("Authorization", "Bearer "+session)
	request.Header.Set("Accept", "application/json")

	client := config.Client

	if client == nil {
		client = profileClient
	}

	response, err := client.Do(request)

	if err != nil {
		return false
	}

	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return false
	}

	var profile struct {
		Name string `json:"name"`
	}

	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&profile); err != nil {
		return false
	}

	if !strings.EqualFold(strings.TrimSpace(profile.Name), username) {
		return false
	}

	rememberSession(key)
	return true
}

func fingerprint(username, session string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(username)) + "|" + session))
	return hex.EncodeToString(digest[:])
}

func cachedSession(key string) bool {
	sessionMu.Lock()
	defer sessionMu.Unlock()

	until, found := sessionCache[key]

	if !found {
		return false
	}

	if time.Now().After(until) {
		delete(sessionCache, key)
		return false
	}

	return true
}

func rememberSession(key string) {
	sessionMu.Lock()
	defer sessionMu.Unlock()

	if len(sessionCache) >= maxCachedSessions {
		now := time.Now()

		for cached, until := range sessionCache {
			if now.After(until) {
				delete(sessionCache, cached)
			}
		}

		if len(sessionCache) >= maxCachedSessions {
			sessionCache = map[string]time.Time{}
		}
	}

	sessionCache[key] = time.Now().Add(sessionTTL)
}
