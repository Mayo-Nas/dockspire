package app

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuthFailuresDoNotLockOutOtherSources(t *testing.T) {
	_, handler := authFixture(t, "")
	request := func(peer, forwarded, body string) int {
		r := httptest.NewRequest("POST", "http://localhost/api/auth/login", strings.NewReader(body))
		r.RemoteAddr = peer
		r.Header.Set("Origin", "http://localhost")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Forwarded-For", forwarded)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	for i := 0; i < 5; i++ {
		if code := request("203.0.113.10:1234", fmt.Sprintf("203.0.113.%d", i+30), `{"username":"admin","password":"wrong"}`); code != 401 {
			t.Fatal(code)
		}
	}
	for _, peer := range []string{"203.0.113.10:9999", "[::ffff:203.0.113.10]:1234"} {
		if code := request(peer, "203.0.113.20", `{}`); code != 429 {
			t.Fatal("port/mapped-IP bypassed source limit", code)
		}
	}
	if code := request("203.0.113.20:1234", "203.0.113.10", `{"username":"admin","password":"`+fixtureAdminPassword+`"}`); code != 200 {
		t.Fatal("another source was locked out by anonymous failures", code)
	}
}

type signInBodyGate struct {
	entered chan struct{}
	release chan struct{}
	reader  io.Reader
}

func (g *signInBodyGate) Read(p []byte) (int, error) {
	select {
	case <-g.entered:
	default:
		close(g.entered)
	}
	<-g.release
	return g.reader.Read(p)
}

func TestSlowLoginBodyDoesNotHoldPasswordWorkSlot(t *testing.T) {
	_, handler := authFixture(t, "")
	gate := &signInBodyGate{entered: make(chan struct{}), release: make(chan struct{}), reader: strings.NewReader(`{}`)}
	r := httptest.NewRequest("POST", "http://localhost/api/auth/login", gate)
	r.RemoteAddr = "203.0.113.10:1234"
	r.Header.Set("Origin", "http://localhost")
	r.Header.Set("Content-Type", "application/json")
	done := make(chan struct{})
	go func() { defer close(done); handler.ServeHTTP(httptest.NewRecorder(), r) }()
	defer func() {
		close(gate.release)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("slow request did not finish after release")
		}
	}()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("login did not read bounded body")
	}
	other := httptest.NewRequest("POST", "http://localhost/api/auth/login", strings.NewReader(`{"username":"admin","password":"`+fixtureAdminPassword+`"}`))
	other.RemoteAddr = "203.0.113.20:1234"
	other.Header = r.Header.Clone()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, other)
	if w.Code != 200 {
		t.Fatal("slow unauthenticated body blocked another source", w.Code)
	}
}

func TestAuthSourceTableIsBoundedAndExpires(t *testing.T) {
	a, _ := authFixture(t, "")
	now := time.Now()
	a.now = func() time.Time { return now }
	for i := 0; i < maxAuthSources+10; i++ {
		r := httptest.NewRequest(http.MethodPost, "http://localhost", nil)
		r.RemoteAddr = fmt.Sprintf("[2001:db8::%x]:1234", i+1)
		if !a.allowAuthAttempt(httptest.NewRecorder(), r) {
			t.Fatal("new source blocked by full table")
		}
		if len(a.attempts) > maxAuthSources {
			t.Fatal("source table exceeded its limit")
		}
		now = now.Add(time.Millisecond)
	}
	now = now.Add(time.Minute)
	r := httptest.NewRequest(http.MethodPost, "http://localhost", nil)
	r.RemoteAddr = "203.0.113.20:1234"
	if !a.allowAuthAttempt(httptest.NewRecorder(), r) || len(a.attempts) != 1 {
		t.Fatal("expired source entries were retained")
	}
}
