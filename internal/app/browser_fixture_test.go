package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type browserReviewOnlyCompose struct{ mutations atomic.Int32 }

func (r *browserReviewOnlyCompose) Run(_ context.Context, _, _ string, args ...string) ([]byte, error) {
	if len(args) > 0 && args[0] == "config" {
		return nil, nil
	}
	r.mutations.Add(1)
	return nil, errors.New("Browser fixture refuses container operations")
}

// Explicit opt-in fixture: real auth/routes/library, fictional inventory, no Docker adapter.
func TestBrowserSmokeFixture(t *testing.T) {
	if os.Getenv("DOCKSPIRE_BROWSER_FIXTURE") != "1" {
		t.Skip("opt-in real-browser fixture")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	origin := "http://" + listener.Addr().String()
	root := filepath.Join(t.TempDir(), "data")
	access, err := loadServerAccess(root, origin, true)
	if err != nil {
		t.Fatal(err)
	}
	access.allowHTTP = true
	source := &fixtureSource{data: Inventory{Mode: "test", Host: Host{Name: "browser-fixture"}, Stacks: []Stack{}, Containers: []Container{}, Images: []Image{}, Networks: []Network{}, Volumes: []Volume{}}}
	runner := &browserReviewOnlyCompose{}
	library := newLibrary(root, "", false, source, runner)
	server := &http.Server{Handler: newApplicationHandler(source, library, access), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 5 * time.Second}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	fmt.Printf("DOCKSPIRE_BROWSER_URL=%s\n", origin)
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); close(done) }()
	select {
	case <-done:
	case <-time.After(100 * time.Second):
		t.Error("Browser fixture lifetime exceeded")
	}
	_ = server.Close()
	if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Error(err)
	}
	if runner.mutations.Load() != 0 {
		t.Error("Browser smoke attempted container operations")
	}
	library.jobsMu.Lock()
	defer library.jobsMu.Unlock()
	if len(library.jobs) != 0 {
		t.Error("Review smoke unexpectedly created an operation")
	}
}
