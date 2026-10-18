package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func setupFixture(t *testing.T) (*adminAccess, http.Handler, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "data")
	a, err := loadServerAccess(root, "", true)
	if err != nil {
		t.Fatal(err)
	}
	a.directTLS, a.secure = true, true
	return a, newApplicationHandler(demoSource{}, newLibrary(root, "", false, demoSource{}, demoCompose{}), a), root
}

func setupBody() string {
	data, _ := json.Marshal(map[string]string{"password": fixtureAdminPassword, "confirmation": fixtureAdminPassword})
	return string(data)
}

func TestBrowserSetupLoginAndRestart(t *testing.T) {
	for _, useTLS := range []bool{false, true} {
		name := "http"
		if useTLS {
			name = "https"
		}
		t.Run(name, func(t *testing.T) {
			a, handler, root := setupFixture(t)
			a.directTLS, a.allowHTTP, a.secure = useTLS, !useTLS, useTLS
			server := httptest.NewUnstartedServer(handler)
			if useTLS {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			client := server.Client()
			client.Jar, _ = cookiejar.New(nil)
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			request := func(method, path, body string) *http.Response {
				t.Helper()
				r, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Origin", server.URL)
				response, err := client.Do(r)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { response.Body.Close() })
				return response
			}
			if response := request("GET", "/", ""); response.StatusCode != 303 || response.Header.Get("Location") != "/setup" {
				t.Fatal("fresh startup did not offer setup")
			}
			if response := request("GET", "/setup", ""); response.StatusCode != 200 {
				t.Fatal("setup unavailable")
			}
			if response := request("GET", "/api/inventory", ""); response.StatusCode != 401 {
				t.Fatal("fresh server exposed inventory")
			}
			status := request("GET", "/api/auth/setup", "")
			data, _ := io.ReadAll(status.Body)
			var state map[string]bool
			if json.Unmarshal(data, &state) != nil || len(state) != 2 || !state["required"] || state["expired"] {
				t.Fatal("bad public setup status")
			}
			if response := request("POST", "/api/auth/setup", setupBody()); response.StatusCode != 201 {
				t.Fatal("setup failed", response.StatusCode)
			}
			before, err := os.ReadFile(filepath.Join(root, "admin.json"))
			if err != nil || bytes.Contains(before, []byte(fixtureAdminPassword)) {
				t.Fatal("password not privately hashed")
			}
			if response := request("POST", "/api/auth/setup", setupBody()); response.StatusCode != 409 {
				t.Fatal("administrator replacement allowed")
			}
			login, _ := json.Marshal(map[string]string{"username": "admin", "password": fixtureAdminPassword})
			response := request("POST", "/api/auth/login", string(login))
			cookieName := "stacklane_session"
			if useTLS {
				cookieName = "__Host-stacklane_session"
			}
			if response.StatusCode != 200 || len(response.Cookies()) != 1 || response.Cookies()[0].Secure != useTLS || response.Cookies()[0].Name != cookieName {
				t.Fatal("login/cookie failed")
			}
			if response := request("GET", "/api/session", ""); response.StatusCode != 200 {
				t.Fatal("TLS session not retained")
			}
			restarted, err := loadServerAccess(root, "", true)
			if err != nil {
				t.Fatal(err)
			}
			if required, _ := restarted.setupState(); required {
				t.Fatal("restart reopened setup")
			}
			after, _ := os.ReadFile(filepath.Join(root, "admin.json"))
			if !bytes.Equal(before, after) {
				t.Fatal("existing administrator changed")
			}
		})
	}
}

func TestBrowserSetupGuardsAndTimeout(t *testing.T) {
	a, handler, root := setupFixture(t)
	body := setupBody()
	for _, test := range []struct {
		path, origin, body string
		status             int
	}{
		{"https://vm.test:9443/api/auth/setup", "https://evil.test", body, 403},
		{"https://vm.test:9443/api/auth/setup", "", body, 403},
		{"http://vm.test:9443/api/auth/setup", "http://vm.test:9443", body, 403},
		{"https://vm.test:9443/api/auth/setup", "https://vm.test:9443", strings.TrimSuffix(body, "}") + `,"unexpected":true}`, 400},
		{"https://vm.test:9443/api/auth/setup", "https://vm.test:9443", `{"password":"short","confirmation":"different"}`, 400},
		{"https://vm.test:9443/api/auth/setup", "https://vm.test:9443", body + `{}`, 400},
	} {
		w := authRequest(handler, "POST", test.path, test.origin, test.body, nil, "")
		if w.Code != test.status || strings.Contains(w.Body.String(), fixtureAdminPassword) {
			t.Fatalf("bad setup guard: %d %s", w.Code, w.Body.String())
		}
	}
	now := a.setupDeadline
	a.now = func() time.Time { return now }
	if w := authRequest(handler, "POST", "https://vm.test:9443/api/auth/setup", "https://vm.test:9443", body, nil, ""); w.Code != 403 {
		t.Fatal("expired setup succeeded")
	}
	if w := authRequest(handler, "GET", "https://vm.test:9443/api/auth/setup", "", "", nil, ""); !strings.Contains(w.Body.String(), `"expired":true`) {
		t.Fatal("expiry not visible")
	}
	if _, err := os.Lstat(filepath.Join(root, "admin.json")); !os.IsNotExist(err) {
		t.Fatal("expired setup created administrator")
	}
	restarted, err := loadServerAccess(root, "", true)
	if err != nil {
		t.Fatal("restart failed to reopen setup", err)
	}
	if required, expired := restarted.setupState(); !required || expired {
		t.Fatal("restart did not reopen setup window")
	}
}

func TestBrowserSetupConcurrentRequestsAndThrottle(t *testing.T) {
	_, handler, _ := setupFixture(t)
	body := setupBody()
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- authRequest(handler, "POST", "https://vm.test:9443/api/auth/setup", "https://vm.test:9443", body, nil, "").Code
		}()
	}
	wg.Wait()
	close(codes)
	created := 0
	for code := range codes {
		if code == 201 {
			created++
		} else if code != 429 && code != 409 {
			t.Fatal("unexpected setup response", code)
		}
	}
	if created != 1 {
		t.Fatal("administrator not exclusively created", created)
	}
	_, handler, _ = setupFixture(t)
	for i := 0; i < 5; i++ {
		authRequest(handler, "POST", "https://vm.test:9443/api/auth/setup", "https://vm.test:9443", `{}`, nil, "")
	}
	if w := authRequest(handler, "POST", "https://vm.test:9443/api/auth/setup", "https://vm.test:9443", `{}`, nil, ""); w.Code != 429 {
		t.Fatal("setup not throttled")
	}
	if w := authRequest(handler, "POST", "https://vm.test:9443/api/auth/setup", "https://vm.test:9443", setupBody(), nil, ""); w.Code != 429 {
		t.Fatal("valid setup bypassed anonymous throttle", w.Code)
	}
}

func TestBrowserSetupFailsClosedForDamagedAdministrator(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "admin.json"), []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadServerAccess(root, "", true); err == nil {
		t.Fatal("damaged admin reopened setup")
	}
	if _, err := loadServerAccess(filepath.Join(t.TempDir(), "absent"), "", false); err == nil {
		t.Fatal("legacy startup became implicit setup")
	}
}

func TestUnrestrictedBrowserPasswords(t *testing.T) {
	for _, password := range []string{"x", strings.Repeat("界\\\"\n\x00", 300), "trailing\n"} {
		a, handler, _ := setupFixture(t)
		a.directTLS, a.secure, a.allowHTTP = false, false, true
		body, _ := json.Marshal(map[string]string{"password": password, "confirmation": password})
		if w := authRequest(handler, "POST", "http://localhost/api/auth/setup", "http://localhost", string(body), nil, ""); w.Code != 201 {
			t.Fatal("password choice rejected", w.Code)
		}
		login, _ := json.Marshal(map[string]string{"username": "admin", "password": password})
		if w := authRequest(handler, "POST", "http://localhost/api/auth/login", "http://localhost", string(login), nil, ""); w.Code != 200 {
			t.Fatal("password changed before hashing", w.Code)
		}
	}
}

func TestExplicitBlankSetupAndPasswordlessSessions(t *testing.T) {
	for _, body := range []string{`{}`, `{"password":null,"confirmation":null}`, `{"password":""}`, `{"confirmation":""}`, `{"password":"","confirmation":"x"}`} {
		_, handler, root := setupFixture(t)
		if w := authRequest(handler, "POST", "https://vm.test/api/auth/setup", "https://vm.test", body, nil, ""); w.Code != 400 {
			t.Fatal("implicit blank setup accepted", w.Code)
		}
		if _, err := os.Lstat(filepath.Join(root, "admin.json")); !os.IsNotExist(err) {
			t.Fatal("invalid setup wrote administrator")
		}
	}
	a, handler, root := setupFixture(t)
	a.directTLS, a.secure, a.allowHTTP = false, false, true
	origin := "http://localhost"
	if w := authRequest(handler, "POST", origin+"/api/auth/setup", origin, `{"password":"","confirmation":""}`, nil, ""); w.Code != 201 || !strings.Contains(w.Body.String(), `"passwordless":true`) {
		t.Fatal("explicit passwordless setup failed", w.Code)
	}
	credential, err := loadAdminCredential(root)
	if err != nil || credential.Version != 2 || credential.Mode != "passwordless" {
		t.Fatal("passwordless choice not persisted", err)
	}
	for _, path := range []string{"/api/health", "/api/auth/setup"} {
		authRequest(handler, "GET", origin+path, "", "", nil, "")
	}
	if len(a.sessions) != 0 {
		t.Fatal("public status checks minted sessions")
	}
	if w := authRequest(handler, "POST", origin+"/api/auth/logout", origin, `{}`, nil, ""); w.Code != 401 || len(w.Result().Cookies()) != 0 {
		t.Fatal("POST bootstrapped a session", w.Code)
	}
	w := authRequest(handler, "GET", origin+"/api/session", "", "", nil, "")
	var state struct {
		CSRFToken    string
		Passwordless bool
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil || !state.Passwordless || len(state.CSRFToken) != 43 || len(w.Result().Cookies()) != 1 {
		t.Fatal("passwordless browser context missing")
	}
	cookie := w.Result().Cookies()[0]
	for _, check := range []struct{ other, csrf string }{{"https://evil.test", state.CSRFToken}, {origin, ""}} {
		if w := authRequest(handler, "POST", origin+"/api/auth/logout", check.other, `{}`, cookie, check.csrf); w.Code != 403 {
			t.Fatal("passwordless mutation bypassed origin/CSRF", w.Code)
		}
	}
	if w := authRequest(handler, "POST", origin+"/api/auth/logout", origin, `{}`, cookie, state.CSRFToken); w.Code != 200 {
		t.Fatal("valid per-browser CSRF rejected", w.Code)
	}
	for _, path := range []string{"/login", "/setup"} {
		if w := authRequest(handler, "GET", origin+path, "", "", nil, ""); w.Code != 303 || w.Header().Get("Location") != "/" {
			t.Fatal("passwordless visitor sent to sign-in", path, w.Code)
		}
	}
	for i := 0; i < 40; i++ {
		authRequest(handler, "GET", origin+"/api/session", "", "", nil, "")
	}
	if len(a.sessions) > 32 {
		t.Fatal("passwordless session map unbounded")
	}
	restarted, err := loadServerAccess(root, "", true)
	if err != nil {
		t.Fatal(err)
	}
	restarted.allowHTTP = true
	next := newApplicationHandler(demoSource{}, newLibrary(root, "", false, demoSource{}, demoCompose{}), restarted)
	if required, _ := restarted.setupState(); required {
		t.Fatal("restart reopened explicit passwordless setup")
	}
	if w := authRequest(next, "GET", origin+"/api/session", "", "", cookie, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"passwordless":true`) {
		t.Fatal("restart required sign-in", w.Code)
	}
	if err := setAdminPassword(root, strings.NewReader("z"), true); err != nil {
		t.Fatal("offline reset could not restore password mode", err)
	}
	protected, err := loadServerAccess(root, "", true)
	if err != nil || protected.passwordless() {
		t.Fatal("reset retained passwordless mode", err)
	}
}

func TestOfflinePasswordTransportBoundsAndLiteralInput(t *testing.T) {
	password := "x\n\x00界"
	root := t.TempDir()
	if err := setAdminPassword(root, strings.NewReader(password), false); err != nil {
		t.Fatal(err)
	}
	a, err := loadServerAccess(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	handler := newApplicationHandler(demoSource{}, newLibrary(root, "", false, demoSource{}, demoCompose{}), a)
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": password})
	if w := authRequest(handler, "POST", "http://localhost/api/auth/login", "http://localhost", string(body), nil, ""); w.Code != 200 {
		t.Fatal("offline input was trimmed", w.Code)
	}
	for _, input := range []string{"", strings.Repeat("x", authRequestLimit+1), strings.Repeat("\x00", authRequestLimit/6)} {
		root := t.TempDir()
		if err := setAdminPassword(root, strings.NewReader(input), false); err == nil {
			t.Fatal("unrepresentable input persisted")
		}
		if _, err := os.Lstat(filepath.Join(root, "admin.json")); !os.IsNotExist(err) {
			t.Fatal("invalid input wrote credential")
		}
	}
}
