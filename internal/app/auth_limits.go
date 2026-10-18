package app

import (
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"
)

const maxAuthSources = 1024

type authAttempts struct {
	window time.Time
	count  int
}

// Use the actual peer IP, never caller-controlled forwarding headers. Expiring
// entries and oldest-entry eviction bound memory without denying a new source
// merely because other sources filled the table.
func (a *adminAccess) allowAuthAttempt(w http.ResponseWriter, r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	address, err := netip.ParseAddr(host)
	key := "unknown-peer"
	if err == nil {
		key = address.Unmap().String()
	}
	a.mu.Lock()
	now := a.now()
	if a.attempts == nil {
		a.attempts = make(map[string]authAttempts)
	}
	for source, entry := range a.attempts {
		if !now.Before(entry.window.Add(time.Minute)) {
			delete(a.attempts, source)
		}
	}
	entry, exists := a.attempts[key]
	if !exists {
		if len(a.attempts) >= maxAuthSources {
			oldestSource := ""
			var oldest time.Time
			for source, candidate := range a.attempts {
				if oldest.IsZero() || candidate.window.Before(oldest) {
					oldestSource, oldest = source, candidate.window
				}
			}
			delete(a.attempts, oldestSource)
		}
		entry.window = now
	}
	if entry.count >= 5 {
		a.mu.Unlock()
		w.Header().Set("Retry-After", strconv.Itoa(int(entry.window.Add(time.Minute).Sub(now).Seconds())+1))
		writeJSON(w, 429, map[string]string{"error": "Too many attempts from this address. Try again in a minute."})
		return false
	}
	entry.count++
	a.attempts[key] = entry
	a.mu.Unlock()
	return true
}
