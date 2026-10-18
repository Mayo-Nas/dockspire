package app

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const setupLifetime = 5 * time.Minute

// Browser setup is opt-in; the existing offline initialization path stays valid.
// A missing/unsafe existing credential must never silently become an empty account.
func loadServerAccess(root, origin string, browserSetup bool) (*adminAccess, error) {
	_, err := os.Lstat(filepath.Join(root, "admin.json"))
	if !browserSetup || !os.IsNotExist(err) {
		credential, err := loadAdminCredential(root)
		if err != nil {
			return nil, err
		}
		return newAdminAccess(credential, origin)
	}
	if err := ensurePrivateDirectory(root); err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Choose a real private data directory")
	}
	a, err := newAdminAccess(adminCredential{}, origin)
	if err != nil {
		return nil, err
	}
	a.setupRoot, a.setupDeadline = root, a.now().Add(setupLifetime)
	a.setupToken = rand.Text()
	return a, nil
}

func (a *adminAccess) setupState() (required, expired bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	required = a.credential.Version == 0 && a.setupRoot != ""
	return required, required && !a.now().Before(a.setupDeadline)
}

func (a *adminAccess) setup(w http.ResponseWriter, r *http.Request) {
	required, expired := a.setupState()
	if !required {
		writeJSON(w, 409, map[string]string{"error": "Administrator already exists. Sign in instead."})
		return
	}
	if expired {
		writeJSON(w, 403, map[string]string{"error": "Setup timed out. Restart Dockspire to open a new five-minute setup window."})
		return
	}
	var input struct {
		Token        string `json:"token"`
		Password     string `json:"password"`
		Confirmation string `json:"confirmation"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&input) != nil || decoder.Decode(&extra) != io.EOF {
		if !a.allowAuthAttempt(w, r) {
			return
		}
		writeJSON(w, 400, map[string]string{"error": "Use matching passwords of at least 15 characters and at most 1024 UTF-8 bytes."})
		return
	}
	a.mu.Lock()
	token := a.setupToken
	a.mu.Unlock()
	if token == "" {
		writeJSON(w, 409, map[string]string{"error": "Administrator already exists. Sign in instead."})
		return
	}
	if subtle.ConstantTimeCompare([]byte(input.Token), []byte(token)) != 1 {
		if !a.allowAuthAttempt(w, r) {
			return
		}
		writeJSON(w, 400, map[string]string{"error": "Enter the setup token from the Dockspire container logs."})
		return
	}
	// A valid one-time token is the operator's recovery path even when another
	// client behind the same NAT/proxy exhausted the anonymous attempt allowance.
	if !validAdminPassword(input.Password) || input.Password != input.Confirmation {
		writeJSON(w, 400, map[string]string{"error": "Use matching passwords of at least 15 characters and at most 1024 UTF-8 bytes."})
		return
	}
	if !a.loginMu.TryLock() {
		w.Header().Set("Retry-After", "2")
		writeJSON(w, 429, map[string]string{"error": "Administrator setup is busy. Try again shortly."})
		return
	}
	defer a.loginMu.Unlock()
	if required, expired := a.setupState(); !required || expired {
		status, message := 409, "Administrator already exists. Sign in instead."
		if expired {
			status, message = 403, "Setup timed out. Restart Dockspire to open a new five-minute setup window."
		}
		writeJSON(w, status, map[string]string{"error": message})
		return
	}
	// Exclusive creation prevents a second process or request from replacing admin.
	if err := setAdminPassword(a.setupRoot, strings.NewReader(input.Password), false); err != nil {
		writeJSON(w, 503, map[string]string{"error": "Could not initialize administrator. Check private storage and restart Dockspire."})
		return
	}
	credential, err := loadAdminCredential(a.setupRoot)
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "Could not load administrator. Check private storage and restart Dockspire."})
		return
	}
	a.mu.Lock()
	a.credential = credential
	a.setupToken = ""
	a.mu.Unlock()
	a.mu.Lock()
	a.attempts = nil
	a.mu.Unlock()
	writeJSON(w, 201, map[string]bool{"created": true})
}
