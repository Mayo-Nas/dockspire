package app

import (
	"bytes"
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const passwordIterations = 600000
const sessionLifetime = 8 * time.Hour
const authRequestLimit = 1 << 20 // Generic JSON/stdin transport ceiling, not a password policy.

// A single local administrator; no account database or persisted bearer tokens.
type adminCredential struct {
	Version int    `json:"version"`
	Mode    string `json:"mode,omitempty"`
	Salt    []byte `json:"salt,omitempty"`
	Hash    []byte `json:"hash,omitempty"`
}

func encodedLoginFits(password string) bool {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(map[string]string{"username": "admin", "password": password})
	return err == nil && utf8.ValidString(password) && encoded.Len()-1 <= authRequestLimit
}

// stdin avoids putting the password in process arguments, environment, or logs.
// The caller supplies a pipe (or a file redirected to stdin), not an echoing terminal.
func setAdminPassword(root string, input io.Reader, reset bool) error {
	data, err := io.ReadAll(io.LimitReader(input, authRequestLimit+1))
	if err != nil {
		return errors.New("Could not read password from stdin")
	}
	if len(data) == 0 {
		return errors.New("Empty stdin does not change access mode; choose explicit blank browser setup for passwordless access")
	}
	if len(data) > authRequestLimit {
		return errors.New("Administrator input exceeds the 1 MiB request-size limit")
	}
	if !encodedLoginFits(string(data)) {
		return errors.New("Administrator input must fit a 1 MiB UTF-8 JSON sign-in request")
	}
	return storeAdminCredential(root, string(data), reset)
}

func storeAdminCredential(root, password string, reset bool) error {
	if err := ensurePrivateDirectory(root); err != nil {
		return errors.New("Cannot create the private data directory")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Choose a real data directory")
	}
	filename := filepath.Join(root, "admin.json")
	_, err = os.Lstat(filename)
	if reset {
		if _, err := loadAdminCredential(root); err != nil {
			return errors.New("Reset requires an existing valid admin file; stop the server first")
		}
	} else if !os.IsNotExist(err) {
		return errors.New("Administrator already exists or cannot be checked; stop the server and use --reset-admin")
	}
	credential := adminCredential{Version: 2, Mode: "passwordless"}
	if password != "" {
		credential = adminCredential{Version: 1, Salt: make([]byte, 16)}
		if _, err := rand.Read(credential.Salt); err != nil {
			return err
		}
		var err error
		credential.Hash, err = pbkdf2.Key(sha256.New, password, credential.Salt, passwordIterations, 32)
		if err != nil {
			return err
		}
	}
	if reset {
		return atomicJSON(filename, credential)
	}
	encoded, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	if err := writePrivateFile(filename, encoded); err != nil {
		return errors.New("Cannot create administrator file; existing files are never overwritten by initialization")
	}
	return syncDirectory(root)
}

func loadAdminCredential(root string) (adminCredential, error) {
	var credential adminCredential
	rootInfo, rootErr := os.Lstat(root)
	if rootErr != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return credential, errors.New("Choose a real private data directory and initialize with --init-admin")
	}
	filename := filepath.Join(root, "admin.json")
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return credential, errors.New("Administrator file missing or unsafe; initialize with --init-admin using stdin (file must be private)")
	}
	file, err := os.Open(filename)
	if err != nil {
		return credential, errors.New("Cannot open administrator file")
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 4097))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&credential) != nil || !((credential.Version == 1 && credential.Mode == "" && len(credential.Salt) == 16 && len(credential.Hash) == 32) || (credential.Version == 2 && credential.Mode == "passwordless" && credential.Salt == nil && credential.Hash == nil)) {
		return credential, errors.New("Invalid administrator file; refusing to start")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return credential, errors.New("Invalid administrator file; refusing to start")
	}
	return credential, nil
}

type adminSession struct {
	csrf    string
	expires time.Time
}

type adminSessionKey struct{}

type adminAccess struct {
	credential    adminCredential
	setupRoot     string
	setupDeadline time.Time
	directTLS     bool
	allowHTTP     bool
	origin        string // Empty permits loopback, or IP addresses with explicit LAN HTTP.
	secure        bool
	mu            sync.Mutex
	sessions      map[[32]byte]adminSession
	loginMu       sync.Mutex // Bound expensive password verification to one at a time.
	attempts      map[string]authAttempts
	now           func() time.Time
}

func newAdminAccess(credential adminCredential, externalURL string) (*adminAccess, error) {
	a := &adminAccess{credential: credential, sessions: make(map[[32]byte]adminSession), now: time.Now}
	if externalURL != "" {
		u, err := url.Parse(externalURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !validHostAuthority(u.Host) || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
			return nil, errors.New("external-url must be an HTTP(S) origin, for example http://stacks.example.test:8787, without credentials, a path, query, or fragment")
		}
		a.origin = u.Scheme + "://" + canonicalAuthority(u.Host, u.Scheme)
		a.secure = u.Scheme == "https"
	}
	return a, nil
}

func loopbackHost(authority string) bool {
	host := authority
	if h, _, err := net.SplitHostPort(authority); err == nil {
		host = h
	}
	return host == "localhost" || host == "127.0.0.1" || host == "[::1]" || host == "::1"
}

func validHostAuthority(authority string) bool {
	u, err := url.Parse("http://" + authority)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.HasSuffix(authority, ":") {
		return false
	}
	if strings.Contains(u.Hostname(), ":") && !strings.HasPrefix(authority, "[") {
		return false
	}
	if strings.HasPrefix(authority, "[") && net.ParseIP(u.Hostname()) == nil {
		return false
	}
	if port := u.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return false
		}
	}
	return true
}

func canonicalAuthority(authority, scheme string) string {
	u, _ := url.Parse(scheme + "://" + authority)
	if (scheme == "http" && u.Port() == "80") || (scheme == "https" && u.Port() == "443") {
		host := strings.ToLower(u.Hostname())
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		return host
	}
	return strings.ToLower(authority)
}

func (a *adminAccess) allowedHost(r *http.Request) bool {
	if !validHostAuthority(r.Host) {
		return false
	}
	if a.directTLS && r.TLS == nil {
		return false
	}
	if a.origin != "" {
		parts := strings.SplitN(a.origin, "://", 2)
		return canonicalAuthority(r.Host, parts[0]) == parts[1] || (r.URL.Path == "/api/health" && loopbackHost(r.Host))
	}
	if a.directTLS || a.allowHTTP {
		u, _ := url.Parse("http://" + r.Host)
		return a.directTLS || loopbackHost(r.Host) || net.ParseIP(u.Hostname()) != nil
	}
	return loopbackHost(r.Host)
}

func (a *adminAccess) requestOrigin(r *http.Request) string {
	if a.origin != "" {
		return a.origin
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + canonicalAuthority(r.Host, scheme)
}

func (a *adminAccess) cookieName() string {
	if a.secure {
		return "__Host-stacklane_session"
	}
	return "stacklane_session"
}

func (a *adminAccess) cookie(w http.ResponseWriter, token string, expires time.Time) {
	maxAge := int(sessionLifetime.Seconds())
	if token == "" {
		maxAge = -1
	}
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(), Value: token, Path: "/", HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: maxAge})
}

func (a *adminAccess) session(r *http.Request) (adminSession, [32]byte, bool) {
	cookie, err := r.Cookie(a.cookieName())
	if err != nil || len(cookie.Value) != 43 {
		return adminSession{}, [32]byte{}, false
	}
	key := sha256.Sum256([]byte(cookie.Value))
	a.mu.Lock()
	defer a.mu.Unlock()
	session, ok := a.sessions[key]
	if ok && !a.now().Before(session.expires) {
		delete(a.sessions, key)
		ok = false
	}
	return session, key, ok
}

func (a *adminAccess) passwordless() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.credential.Version == 2 && a.credential.Mode == "passwordless"
}

func (a *adminAccess) login(w http.ResponseWriter, r *http.Request) {
	if !a.allowAuthAttempt(w, r) {
		return
	}
	a.mu.Lock()
	credential := a.credential
	a.mu.Unlock()
	if credential.Version == 0 {
		writeJSON(w, 409, map[string]string{"error": "Complete administrator setup first"})
		return
	}
	if credential.Mode == "passwordless" {
		writeJSON(w, 409, map[string]string{"error": "Passwordless access is enabled; open the dashboard"})
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, authRequestLimit))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&input) != nil || decoder.Decode(&extra) != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "Invalid sign-in request"})
		return
	}
	// Read bounded input before taking the expensive-work slot. A stalled body
	// must not prevent another source from signing in.
	if !a.loginMu.TryLock() {
		w.Header().Set("Retry-After", "2")
		writeJSON(w, 429, map[string]string{"error": "A sign-in is in progress. Try again shortly."})
		return
	}
	defer a.loginMu.Unlock()
	hash, err := pbkdf2.Key(sha256.New, input.Password, credential.Salt, passwordIterations, 32)
	if err != nil || subtle.ConstantTimeCompare(hash, credential.Hash) != 1 || input.Username != "admin" {
		writeJSON(w, 401, map[string]string{"error": "Incorrect username or password"})
		return
	}
	a.startSession(w, r)
	writeJSON(w, 200, map[string]bool{"authenticated": true})
}

func (a *adminAccess) startSession(w http.ResponseWriter, r *http.Request) (adminSession, [32]byte) {
	now := a.now()
	// 32 random bytes for both tokens; the map holds only a digest of the cookie.
	tokenBytes := make([]byte, 32)
	_, _ = rand.Read(tokenBytes)
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	_, _ = rand.Read(tokenBytes)
	session := adminSession{csrf: base64.RawURLEncoding.EncodeToString(tokenBytes), expires: now.Add(sessionLifetime)}
	_, oldKey, hasOld := a.session(r)
	a.mu.Lock()
	if hasOld {
		delete(a.sessions, oldKey)
	}
	for key, existing := range a.sessions {
		if !now.Before(existing.expires) {
			delete(a.sessions, key)
		}
	}
	if len(a.sessions) >= 32 {
		var oldest [32]byte
		var expires time.Time
		for key, existing := range a.sessions {
			if expires.IsZero() || existing.expires.Before(expires) {
				oldest, expires = key, existing.expires
			}
		}
		delete(a.sessions, oldest)
	}
	a.sessions[sha256.Sum256([]byte(token))] = session
	a.mu.Unlock()
	a.cookie(w, token, session.expires)
	return session, sha256.Sum256([]byte(token))
}

func (a *adminAccess) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/setup" && r.Method == http.MethodPost {
			a.setup(w, r)
			return
		}
		required, expired := a.setupState()
		if r.URL.Path == "/api/auth/setup" && r.Method == http.MethodGet {
			writeJSON(w, 200, map[string]bool{"required": required, "expired": expired})
			return
		}
		if required && r.URL.Path != "/api/health" {
			switch r.URL.Path {
			case "/setup", "/setup.js", "/style.css", "/favicon.ico", "/brand/dockspire-horizontal-dark.png", "/brand/dockspire-icon-256.png":
				next.ServeHTTP(w, r)
			default:
				if strings.HasPrefix(r.URL.Path, "/api/") {
					writeJSON(w, 401, map[string]string{"error": "Complete administrator setup first"})
				} else {
					http.Redirect(w, r, "/setup", http.StatusSeeOther)
				}
			}
			return
		}
		if r.URL.Path == "/setup" || r.URL.Path == "/setup.js" {
			target := "/login"
			if a.passwordless() {
				target = "/"
			}
			http.Redirect(w, r, target, http.StatusSeeOther)
			return
		}
		if r.URL.Path == "/api/auth/login" && r.Method == http.MethodPost {
			a.login(w, r)
			return
		}
		if r.URL.Path == "/api/health" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			writeJSON(w, 200, map[string]string{"status": "ok"})
			return
		}
		session, key, authenticated := a.session(r)
		if a.passwordless() && !authenticated && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			session, key = a.startSession(w, r)
			authenticated = true
		}
		// Only the exact static assets needed before sign-in are public.
		publicAsset := false
		switch r.URL.Path {
		case "/login", "/login.js", "/style.css", "/favicon.ico",
			"/brand/dockspire-icon-64.png", "/brand/dockspire-icon-128.png",
			"/brand/dockspire-icon-256.png", "/brand/dockspire-horizontal-dark.png":
			publicAsset = true
		}
		if publicAsset {
			if authenticated && r.URL.Path == "/login" {
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if !authenticated {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSON(w, 401, map[string]string{"error": "Sign in to continue"})
			} else {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
			}
			return
		}
		if r.Method == http.MethodPost && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Stacklane-CSRF")), []byte(session.csrf)) != 1 {
			writeJSON(w, 403, map[string]string{"error": "Open Dockspire in this browser and retry the operation"})
			return
		}
		if r.URL.Path == "/api/auth/logout" && r.Method == http.MethodPost {
			a.mu.Lock()
			delete(a.sessions, key)
			a.mu.Unlock()
			a.cookie(w, "", time.Unix(1, 0))
			writeJSON(w, 200, map[string]bool{"authenticated": false})
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminSessionKey{}, session)))
	})
}
