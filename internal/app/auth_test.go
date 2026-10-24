package app

import (
	"archive/zip"
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const fixtureAdminPassword = "fictional test password only"

var fixtureCredential = sync.OnceValue(func() adminCredential {
	salt := []byte("fixture-salt-016")
	hash, err := pbkdf2.Key(sha256.New, fixtureAdminPassword, salt, passwordIterations, 32)
	if err != nil {
		panic(err)
	}
	return adminCredential{Version: 1, Salt: salt, Hash: hash}
})

func authFixture(t *testing.T, origin string) (*adminAccess, http.Handler) {
	t.Helper()
	a, err := newAdminAccess(fixtureCredential(), origin)
	if err != nil {
		t.Fatal(err)
	}
	library := newLibrary(t.TempDir(), "", false, demoSource{}, demoCompose{})
	return a, newApplicationHandler(demoSource{}, library, a)
}

func authRequest(handler http.Handler, method, path, origin, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if method == "POST" {
		r.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	r.Header.Set("X-Dockspire-CSRF", csrf)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func fixtureLogin(t *testing.T, handler http.Handler, origin string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": fixtureAdminPassword})
	w := authRequest(handler, "POST", origin+"/api/auth/login", origin, string(body), nil, "")
	if w.Code != 200 || len(w.Result().Cookies()) != 1 {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	return w.Result().Cookies()[0]
}

func fixtureCSRF(t *testing.T, handler http.Handler, origin string, cookie *http.Cookie) string {
	t.Helper()
	w := authRequest(handler, "GET", origin+"/api/session", "", "", cookie, "")
	var session struct{ CSRFToken string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &session) != nil || len(session.CSRFToken) != 43 {
		t.Fatalf("session failed: %d %s", w.Code, w.Body.String())
	}
	return session.CSRFToken
}

func TestSessionReportsBuildVersion(t *testing.T) {
	a, handler := authFixture(t, "")
	a.credential = adminCredential{Version: 2, Mode: "passwordless"}
	w := authRequest(handler, "GET", "http://localhost/api/session", "", "", nil, "")
	var response struct{ Version string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Version != Version || response.Version == "" {
		t.Fatal("session did not report embedded build version")
	}
}

func TestAuthenticationProtectsApplication(t *testing.T) {
	_, handler := authFixture(t, "")
	for _, path := range []string{"/api/session", "/api/inventory", "/api/stacks", "/api/stacks/example", "/api/containers/fixture/logs", "/api/system/installation", "/api/unknown"} {
		w := authRequest(handler, "GET", "http://localhost:8788"+path, "", "", nil, "")
		if w.Code != 401 || strings.Contains(w.Body.String(), "csrfToken") {
			t.Fatalf("unauthenticated API accessible: %s %d", path, w.Code)
		}
	}
	for _, path := range []string{"/", "/index.html", "/app.js", "/admin.json", "/anything", "/brand/", "/brand/unknown.png", "/brand/../app.js"} {
		w := authRequest(handler, "GET", "http://localhost"+path, "", "", nil, "")
		if w.Code != 303 || w.Header().Get("Location") != "/login" {
			t.Fatalf("unauthenticated asset accessible: %s %d", path, w.Code)
		}
	}
	for _, path := range []string{"/login", "/login.js", "/style.css", "/api/health", "/brand/dockspire-icon-256.png", "/brand/dockspire-horizontal-dark.png"} {
		w := authRequest(handler, "GET", "http://127.0.0.1:8788"+path, "", "", nil, "")
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Security-Policy") == "" {
			t.Fatalf("public login/health unavailable or unprotected: %s %d", path, w.Code)
		}
		if path == "/api/health" && strings.Contains(w.Body.String(), "capability") {
			t.Fatal("public health exposed capabilities")
		}
		if strings.HasSuffix(path, ".png") && (!strings.HasPrefix(w.Header().Get("Content-Type"), "image/png") || !strings.HasPrefix(w.Body.String(), "\x89PNG\r\n\x1a\n")) {
			t.Fatalf("brand image not served as PNG: %s", path)
		}
	}
}

func TestAdminLoginLogoutExpiryAndRestart(t *testing.T) {
	a, handler := authFixture(t, "")
	origin := "http://127.0.0.1:8788"
	now := time.Now()
	a.now = func() time.Time { return now }
	cookie := fixtureLogin(t, handler, origin)
	if !cookie.HttpOnly || cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.MaxAge != int(sessionLifetime.Seconds()) {
		t.Fatal("incorrect localhost cookie policy")
	}
	csrf := fixtureCSRF(t, handler, origin, cookie)
	if w := authRequest(handler, "GET", origin+"/api/inventory", "", "", cookie, ""); w.Code != 200 {
		t.Fatal("signed-in inventory unavailable", w.Code)
	}
	for _, token := range []string{"", "incorrect-token"} {
		if w := authRequest(handler, "POST", origin+"/api/auth/logout", origin, "{}", cookie, token); w.Code != 403 {
			t.Fatal("logout bypassed CSRF", w.Code)
		}
	}
	w := authRequest(handler, "POST", origin+"/api/auth/logout", origin, "{}", cookie, csrf)
	if w.Code != 200 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout did not clear cookie")
	}
	if w := authRequest(handler, "GET", origin+"/api/session", "", "", cookie, ""); w.Code != 401 {
		t.Fatal("logged-out cookie still works")
	}
	cookie = fixtureLogin(t, handler, origin)
	_, restarted := authFixture(t, "")
	if w := authRequest(restarted, "GET", origin+"/api/session", "", "", cookie, ""); w.Code != 401 {
		t.Fatal("session survived a server restart")
	}
	now = now.Add(sessionLifetime)
	if w := authRequest(handler, "GET", origin+"/api/session", "", "", cookie, ""); w.Code != 401 {
		t.Fatal("expired session accepted")
	}
}

func TestSessionCSRFIsolationAndRotation(t *testing.T) {
	a, handler := authFixture(t, "")
	origin := "http://localhost"
	first := fixtureLogin(t, handler, origin)
	second := fixtureLogin(t, handler, origin)
	firstCSRF := fixtureCSRF(t, handler, origin, first)
	secondCSRF := fixtureCSRF(t, handler, origin, second)
	if first.Value == second.Value || firstCSRF == secondCSRF {
		t.Fatal("sessions reused tokens")
	}
	for _, path := range []string{"/api/auth/logout", "/api/stacks", "/api/library/backup"} {
		if w := authRequest(handler, "POST", origin+path, origin, "{}", second, firstCSRF); w.Code != 403 {
			t.Fatal("cross-session CSRF accepted", path)
		}
	}
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": fixtureAdminPassword})
	w := authRequest(handler, "POST", origin+"/api/auth/login", origin, string(body), first, "")
	if w.Code != 200 || len(a.sessions) != 2 {
		t.Fatal("re-login did not replace the old session")
	}
	if w := authRequest(handler, "GET", origin+"/api/session", "", "", first, ""); w.Code != 401 {
		t.Fatal("old session accepted after rotation")
	}
}

func TestLoginOriginHostProxyAndMediaChecks(t *testing.T) {
	a, handler := authFixture(t, "https://stacks.example.test")
	origin := "https://stacks.example.test"
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": fixtureAdminPassword})
	for _, requestOrigin := range []string{"", "http://stacks.example.test", "https://other.example.test", "null"} {
		if w := authRequest(handler, "POST", origin+"/api/auth/login", requestOrigin, string(body), nil, ""); w.Code != 403 {
			t.Fatal("bad login origin accepted", requestOrigin)
		}
	}
	r := httptest.NewRequest("POST", "http://stacks.example.test/api/auth/login", strings.NewReader(string(body)))
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("non-JSON login accepted")
	}
	r.Header.Set("Content-Type", "application/json")
	r.Host = "evil.example.test"
	r.Header.Set("X-Forwarded-Host", "stacks.example.test")
	r.Header.Set("X-Forwarded-Proto", "https")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("forwarded headers bypassed Host restriction")
	}
	// TLS terminates at the proxy; backend HTTP still uses the configured HTTPS
	// origin and secure cookie, without trusting forwarded headers.
	w = authRequest(handler, "POST", "http://stacks.example.test/api/auth/login", origin, string(body), nil, "")
	if w.Code != 200 || !w.Result().Cookies()[0].Secure || a.cookieName() != "__Host-dockspire_session" {
		t.Fatal("proxy origin/cookie policy failed", w.Code)
	}
	if w := authRequest(handler, "GET", "http://127.0.0.1:8787/api/health", "", "", nil, ""); w.Code != 200 {
		t.Fatal("local proxy health check blocked")
	}
	if w := authRequest(handler, "GET", "http://127.0.0.1:8787/api/session", "", "", nil, ""); w.Code != 403 {
		t.Fatal("alternate origin exposed the application")
	}
	for _, value := range []string{"ftp://192.168.1.10", "https://user:password@example.test", "https://example.test/path", "https://example.test?query", "https://example.test#fragment", "//example.test", "https://", "http://::1", "http://[example.test]", "http://example.test:", "http://example.test:65536"} {
		if _, err := newAdminAccess(fixtureCredential(), value); err == nil {
			t.Fatal("unsafe external origin accepted", value)
		}
	}
	_, local := authFixture(t, "")
	if w := authRequest(local, "GET", "http://192.168.1.10/api/health", "", "", nil, ""); w.Code != 403 {
		t.Fatal("default accepts LAN Host")
	}
}

func TestExplicitHTTPLANHostsAndOrigins(t *testing.T) {
	a, handler := authFixture(t, "")
	a.allowHTTP = true
	for _, host := range []string{"localhost:8787", "127.0.0.1", "192.0.2.10:8787", "[2001:db8::1]:8787"} {
		r := httptest.NewRequest("GET", "http://localhost/api/health", nil)
		r.Host = host
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("HTTP IP/loopback Host rejected: %s (%d)", host, w.Code)
		}
	}
	for _, host := range []string{"attacker.example.test:8787", "localhost.attacker.test", "user@192.0.2.10", "192.0.2.10/path", "192.0.2.10?x", "192.0.2.10#x", "192.0.2.10:", "192.0.2.10:0", "192.0.2.10:65536", "::1", "[example.test]", ""} {
		r := httptest.NewRequest("GET", "http://localhost/api/health", nil)
		r.Host = host
		r.Header.Set("X-Forwarded-Host", "192.0.2.10:8787")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("untrusted HTTP Host accepted: %s (%d)", host, w.Code)
		}
	}
	origin := "http://192.0.2.10:8787"
	body := `{"username":"admin","password":"` + fixtureAdminPassword + `"}`
	for _, other := range []string{"", "https://192.0.2.10:8787", "http://192.0.2.10:8788", "http://attacker.example.test:8787"} {
		if w := authRequest(handler, "POST", origin+"/api/auth/login", other, body, nil, ""); w.Code != 403 {
			t.Fatal("HTTP login accepted wrong/missing origin", other, w.Code)
		}
	}
	cookie := fixtureLogin(t, handler, origin)
	if cookie.Secure || cookie.Name != "dockspire_session" {
		t.Fatal("HTTP cookie unusable")
	}
	_, pinned := authFixture(t, "http://stacks.example.test:8787")
	cookie = fixtureLogin(t, pinned, "http://stacks.example.test:8787")
	if cookie.Secure {
		t.Fatal("pinned HTTP origin received Secure cookie")
	}
	if w := authRequest(pinned, "GET", "http://stacks.example.test/api/health", "", "", nil, ""); w.Code != 403 {
		t.Fatal("pinned HTTP origin accepted another port", w.Code)
	}
}

func TestExternalOriginDefaultPorts(t *testing.T) {
	for _, test := range []struct {
		configured, expected string
		secure               bool
	}{
		{"http://stacks.example.test:80", "http://stacks.example.test", false},
		{"https://stacks.example.test:443", "https://stacks.example.test", true},
		{"http://[2001:db8::1]:80", "http://[2001:db8::1]", false},
		{"https://[2001:db8::1]:443", "https://[2001:db8::1]", true},
	} {
		a, handler := authFixture(t, test.configured)
		if a.origin != test.expected {
			t.Fatal("origin not canonical", a.origin)
		}
		body := `{"username":"admin","password":"` + fixtureAdminPassword + `"}`
		w := authRequest(handler, "POST", test.configured+"/api/auth/login", test.expected, body, nil, "")
		if w.Code != 200 || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].Secure != test.secure {
			t.Fatal("browser default-port origin/cookie failed", test.configured, w.Code)
		}
	}
}

func TestLoginThrottlingAndBoundedWork(t *testing.T) {
	a, handler := authFixture(t, "")
	origin := "http://localhost"
	now := time.Now()
	a.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		w := authRequest(handler, "POST", origin+"/api/auth/login", origin, `{"username":"admin","password":"wrong"}`, nil, "")
		if w.Code != 401 {
			t.Fatal("unexpected failed login response", w.Code)
		}
	}
	w := authRequest(handler, "POST", origin+"/api/auth/login", origin, `{}`, nil, "")
	if w.Code != 429 || w.Header().Get("Retry-After") == "" || len(a.sessions) != 0 {
		t.Fatal("login attempts not throttled")
	}
	now = now.Add(time.Minute)
	fixtureLogin(t, handler, origin)
	a.loginMu.Lock()
	w = authRequest(handler, "POST", origin+"/api/auth/login", origin, `{}`, nil, "")
	a.loginMu.Unlock()
	if w.Code != 429 {
		t.Fatal("concurrent password checks are not bounded")
	}
}

func TestAdminCredentialLifecycleAndArchiveExclusion(t *testing.T) {
	root := t.TempDir()
	if err := setAdminPassword(root, strings.NewReader(""), false); err == nil {
		t.Fatal("empty offline input enabled passwordless access")
	}
	if err := setAdminPassword(root, strings.NewReader(fixtureAdminPassword+"\r\n"), false); err != nil {
		t.Fatal(err)
	}
	credential, err := loadAdminCredential(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := setAdminPassword(root, strings.NewReader(fixtureAdminPassword), false); err == nil {
		t.Fatal("initialization overwrote existing administrator")
	}
	raw, _ := os.ReadFile(filepath.Join(root, "admin.json"))
	if strings.Contains(string(raw), fixtureAdminPassword) {
		t.Fatal("password stored in plaintext")
	}
	if err := setAdminPassword(root, strings.NewReader(fixtureAdminPassword+" changed"), true); err != nil {
		t.Fatal(err)
	}
	changed, err := loadAdminCredential(root)
	if err != nil || string(changed.Hash) == string(credential.Hash) || string(changed.Salt) == string(credential.Salt) {
		t.Fatal("reset did not generate a new salt/hash", err)
	}
	library := newLibrary(root, "", false, demoSource{}, demoCompose{})
	if _, err := serverCertificate(root, "", ""); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "library.zip")
	if err := library.writeArchive(archive, ""); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	for _, file := range z.File {
		if strings.Contains(file.Name, "admin") || strings.HasPrefix(file.Name, "tls/") || strings.HasSuffix(file.Name, ".pem") {
			t.Fatal("portable library archive contains credentials")
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Join(root, "admin.json"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadAdminCredential(root); err == nil {
			t.Fatal("publicly readable administrator file accepted")
		}
	}
}

func TestAdminRejectsMalformedStorageAndRequests(t *testing.T) {
	root := t.TempDir()
	for _, data := range []string{`{}`, `{"version":1,"salt":"AA==","hash":"AA=="}`, `{"version":999}`, `{"version":2}`, `{"version":2,"mode":"password"}`, `{"version":2,"mode":"passwordless","salt":"AA=="}`, `{"version":1,"mode":"passwordless"}`, strings.Repeat("x", 4097)} {
		if err := os.WriteFile(filepath.Join(root, "admin.json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadAdminCredential(root); err == nil {
			t.Fatal("invalid admin file accepted")
		}
	}
	for _, body := range []string{`{} {}`, `{"unknown":"field"}`, `{"username":"admin","password":"` + strings.Repeat("x", authRequestLimit) + `"}`, strings.Repeat(" ", authRequestLimit+1)} {
		_, handler := authFixture(t, "")
		if w := authRequest(handler, "POST", "http://localhost/api/auth/login", "http://localhost", body, nil, ""); w.Code != 400 {
			t.Fatal("malformed login accepted", w.Code)
		}
	}
}

func TestAdminCLI(t *testing.T) {
	if os.Getenv("DOCKSPIRE_TEST_ADMIN_CLI") == "1" {
		for i, arg := range os.Args {
			if arg == "--" {
				os.Args = append([]string{os.Args[0]}, os.Args[i+1:]...)
				break
			}
		}
		flag.CommandLine = flag.NewFlagSet("fixture-admin", flag.ExitOnError)
		Run()
		return
	}
	root := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []struct {
		args []string
		ok   bool
	}{
		{[]string{"--demo", "--data-dir", filepath.Join(root, "uninitialized")}, false},
		{[]string{"--check-installation", "--demo", "--data-dir", filepath.Join(root, "uninitialized")}, true},
		{[]string{"--init-admin"}, true},
		{[]string{"--init-admin"}, false},
		{[]string{"--reset-admin"}, true},
		{[]string{"--init-admin", "--reset-admin"}, false},
		{[]string{"--allow-http", "--https"}, false},
		{[]string{"--https", "--external-url", "http://stacks.example.test"}, false},
		{[]string{"--allow-http", "--external-url", "https://stacks.example.test"}, false},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, binary, append([]string{"-test.run=^TestAdminCLI$", "--", "--data-dir", root}, command.args...)...)
		cmd.Env = append(os.Environ(), "DOCKSPIRE_TEST_ADMIN_CLI=1")
		cmd.Stdin = strings.NewReader(fixtureAdminPassword + "\n")
		output, err := cmd.CombinedOutput()
		cancel()
		if (err == nil) != command.ok || strings.Contains(string(output), fixtureAdminPassword) || strings.Contains(string(output), "Installation check") {
			t.Fatalf("admin CLI failed or disclosed credentials: %v", err)
		}
	}
}

func TestAuthenticatedHTTPStackAndArchiveFlow(t *testing.T) {
	_, handler := authFixture(t, "")
	server := httptest.NewServer(handler)
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	request := func(path, body, csrf string) *http.Response {
		t.Helper()
		method := "GET"
		if body != "" {
			method = "POST"
		}
		r, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Origin", server.URL)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Dockspire-CSRF", csrf)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { response.Body.Close() })
		return response
	}
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": fixtureAdminPassword})
	if response := request("/api/auth/login", string(body), ""); response.StatusCode != 200 {
		t.Fatal("real HTTP login failed", response.StatusCode)
	}
	var session struct{ CSRFToken string }
	if response := request("/api/session", "", ""); json.NewDecoder(response.Body).Decode(&session) != nil || session.CSRFToken == "" {
		t.Fatal("session cookie was not retained")
	}
	definition, _ := json.Marshal(stackInput{Name: "auth-fixture", Compose: "services:\n web:\n  image: nginx:alpine\n"})
	if response := request("/api/stacks", string(definition), "incorrect"); response.StatusCode != 403 {
		t.Fatal("unauthorized draft write was accepted")
	}
	if response := request("/api/stacks", string(definition), session.CSRFToken); response.StatusCode != 200 {
		t.Fatal("authenticated draft creation failed", response.StatusCode)
	}
	if response := request("/api/library/backup", `{"includeSecrets":true}`, session.CSRFToken); response.StatusCode != 200 || response.Header.Get("Content-Type") != "application/zip" {
		t.Fatal("authenticated archive failed", response.StatusCode)
	}
	if response := request("/api/auth/logout", "{}", session.CSRFToken); response.StatusCode != 200 {
		t.Fatal("HTTP logout failed")
	}
	if response := request("/api/stacks", "", ""); response.StatusCode != 401 {
		t.Fatal("cookie jar retained access after logout")
	}
}
