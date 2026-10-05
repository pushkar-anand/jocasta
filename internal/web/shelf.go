package web

import (
	"crypto/rand"
	"maps"
	"sync"
	"time"
)

// shelfTTL is how long a secret waits on the shelf for the GET that shows it.
// The redirect follows at once, so this covers only a slow or interrupted
// browser.
const shelfTTL = 5 * time.Minute

// A shelf holds one-time secrets, such as a new token's plaintext or fresh
// recovery codes, in memory between a POST and the GET it redirects to. The
// session carries only the random id, so a secret never reaches the session
// store in the database. Each secret is gone after its first take, after
// shelfTTL, or on restart; the page then shows without it.
//
// A shelf is safe for concurrent use by multiple goroutines.
type shelf struct {
	now func() time.Time

	mu      sync.Mutex
	entries map[string]shelfEntry
}

type shelfEntry struct {
	value   string
	expires time.Time
}

func newShelf(now func() time.Time) *shelf {
	return &shelf{now: now, entries: make(map[string]shelfEntry)}
}

// put stores value and returns the id that takes it back.
func (s *shelf) put(value string) string {
	id := rand.Text()
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Sweeping here bounds the map by what was put in the last shelfTTL,
	// with no goroutine to stop.
	maps.DeleteFunc(s.entries, func(_ string, e shelfEntry) bool {
		return !now.Before(e.expires)
	})

	s.entries[id] = shelfEntry{value: value, expires: now.Add(shelfTTL)}

	return id
}

// take returns the value stored under id and removes it. It reports false
// when id is unknown, already taken or expired.
func (s *shelf) take(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[id]
	if !ok {
		return "", false
	}

	delete(s.entries, id)

	if !s.now().Before(e.expires) {
		return "", false
	}

	return e.value, true
}
