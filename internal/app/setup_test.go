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

func setupBody(a *adminAccess) string {
	data, _ := json.Marshal(map[string]string{"token": a.setupToken, "password": fixtureAdminPassword, "confirmation": fixtureAdminPassword})
	return string(data)
}

func TestBrowserSetupTLSLoginAndRestart(t *testing.T) {
	a, handler, root := setupFixture(t)
	server := httptest.NewTLSServer(handler)
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
	if !strings.Contains(string(data), `"required":true`) || strings.Contains(string(data), a.setupToken) {
		t.Fatal("bad public setup status")
	}
	if response := request("POST", "/api/auth/setup", setupBody(a)); response.StatusCode != 201 {
		t.Fatal("setup failed", response.StatusCode)
	}
	if a.setupToken != "" {
		t.Fatal("setup token retained after success")
	}
	before, err := os.ReadFile(filepath.Join(root, "admin.json"))
	if err != nil || bytes.Contains(before, []byte(fixtureAdminPassword)) {
		t.Fatal("password not privately hashed")
	}
	if response := request("POST", "/api/auth/setup", setupBody(a)); response.StatusCode != 409 {
		t.Fatal("administrator replacement allowed")
	}
	login, _ := json.Marshal(map[string]string{"username": "admin", "password": fixtureAdminPassword})
	response := request("POST", "/api/auth/login", string(login))
	if response.StatusCode != 200 || len(response.Cookies()) != 1 || !response.Cookies()[0].Secure || response.Cookies()[0].Name != "__Host-stacklane_session" {
		t.Fatal("HTTPS login/cookie failed")
	}
	if response := request("GET", "/api/session", ""); response.StatusCode != 200 {
		t.Fatal("TLS session not retained")
	}
	restarted, err := loadServerAccess(root, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if required, _ := restarted.setupState(); required || restarted.setupToken != "" {
		t.Fatal("restart reopened setup")
	}
	after, _ := os.ReadFile(filepath.Join(root, "admin.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("existing administrator changed")
	}
}

func TestBrowserSetupGuardsAndTimeout(t *testing.T) {
	a, handler, root := setupFixture(t)
	body := setupBody(a)
	for _, test := range []struct {
		path, origin, body string
		status             int
	}{
		{"https://vm.test:9443/api/auth/setup", "https://evil.test", body, 403},
		{"https://vm.test:9443/api/auth/setup", "", body, 403},
		{"http://vm.test:9443/api/auth/setup", "http://vm.test:9443", body, 403},
		{"https://vm.test:9443/api/auth/setup", "https://vm.test:9443", strings.Replace(body, a.setupToken, "wrong", 1), 400},
		{"https://vm.test:9443/api/auth/setup", "https://vm.test:9443", `{"password":"short","confirmation":"short"}`, 400},
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
	if err != nil || restarted.setupToken == a.setupToken {
		t.Fatal("restart failed to rotate setup token")
	}
	if required, expired := restarted.setupState(); !required || expired {
		t.Fatal("restart did not reopen setup window")
	}
}

func TestBrowserSetupConcurrentRequestsAndThrottle(t *testing.T) {
	a, handler, _ := setupFixture(t)
	body := setupBody(a)
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
	a, handler, _ = setupFixture(t)
	for i := 0; i < 5; i++ {
		authRequest(handler, "POST", "https://vm.test:9443/api/auth/setup", "https://vm.test:9443", `{}`, nil, "")
	}
	if w := authRequest(handler, "POST", "https://vm.test:9443/api/auth/setup", "https://vm.test:9443", `{}`, nil, ""); w.Code != 429 {
		t.Fatal("setup not throttled")
	}
	if w := authRequest(handler, "POST", "https://vm.test:9443/api/auth/setup", "https://vm.test:9443", setupBody(a), nil, ""); w.Code != 201 {
		t.Fatal("valid setup token did not recover from anonymous throttle", w.Code)
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
