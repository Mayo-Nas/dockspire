package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const hostSocket = "/run/stacklane-host/control.sock"
const hostState = "/var/lib/stacklane-host"

const hostUpdatesDisabled = "Docker and Compose package installation is disabled in this preview. Versions and package-list refreshes remain available. Use SSH for host upgrades."

type HostJob struct {
	ID         string        `json:"id"`
	Kind       string        `json:"kind"`
	Target     string        `json:"target,omitempty"`
	Status     string        `json:"status"`
	Stage      string        `json:"stage"`
	Message    string        `json:"message"`
	Packages   []HostPackage `json:"packages,omitempty"`
	StartedAt  string        `json:"startedAt"`
	FinishedAt string        `json:"finishedAt,omitempty"`
}
type HostStatus struct {
	Connected      bool          `json:"connected"`
	UpdatesEnabled bool          `json:"updatesEnabled"`
	OS             string        `json:"os,omitempty"`
	Packages       []HostPackage `json:"packages"`
	CacheUpdatedAt string        `json:"cacheUpdatedAt,omitempty"`
	Active         *HostJob      `json:"active,omitempty"`
	History        []HostJob     `json:"history"`
	Message        string        `json:"message,omitempty"`
}
type hostManager struct {
	mu            sync.Mutex
	commands      hostCommands
	state, osName string
	jobs          []HostJob
	packages      []HostPackage
	cacheUpdated  string
}

func newHostManager(state, osName string, commands hostCommands) (*hostManager, error) {
	m := &hostManager{state: state, osName: osName, commands: commands, jobs: []HostJob{}, packages: []HostPackage{}}
	if err := os.MkdirAll(state, 0700); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(state, "history.json"))
	if err == nil && json.Unmarshal(raw, &m.jobs) != nil {
		return nil, errors.New("Host update history is invalid")
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for i := range m.jobs {
		if m.jobs[i].Status == "running" {
			m.jobs[i].Status, m.jobs[i].Stage, m.jobs[i].Message = "interrupted", "interrupted", "The host helper restarted. Inspect Docker and dpkg status before retrying; no update was automatically resumed."
			m.jobs[i].FinishedAt = time.Now().UTC().Format(time.RFC3339)
		}
	}
	if len(m.jobs) > 0 {
		if err = atomicJSON(filepath.Join(state, "history.json"), m.jobs); err != nil {
			return nil, err
		}
	}
	if raw, err = os.ReadFile(filepath.Join(state, "cache.json")); err == nil {
		var cache struct{ UpdatedAt string }
		if json.Unmarshal(raw, &cache) == nil {
			m.cacheUpdated = cache.UpdatedAt
		}
	}
	return m, nil
}
func (m *hostManager) activeLocked() *HostJob {
	for _, job := range m.jobs {
		if job.Status == "running" {
			copy := job
			return &copy
		}
	}
	return nil
}
func (m *hostManager) status(ctx context.Context) HostStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := HostStatus{Connected: true, OS: m.osName, Packages: m.packages, CacheUpdatedAt: m.cacheUpdated, History: []HostJob{}, Active: m.activeLocked()}
	if result.Active == nil {
		packages, err := inspectHostPackages(ctx, m.commands)
		if err != nil {
			result.Message = err.Error()
		} else {
			m.packages = packages
			result.Packages = packages
		}
	}
	for i := len(m.jobs) - 1; i >= 0 && len(result.History) < 10; i-- {
		result.History = append(result.History, m.jobs[i])
	}
	return result
}
func (m *hostManager) begin(kind string) (HostJob, error) {
	if kind != "refresh" {
		return HostJob{}, errors.New(hostUpdatesDisabled)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeLocked() != nil {
		return HostJob{}, errors.New("A host operation is already running")
	}
	job := HostJob{ID: randomID(), Kind: kind, Status: "running", Stage: "queued", Message: "Package-list refresh accepted; no packages will be installed", StartedAt: time.Now().UTC().Format(time.RFC3339)}
	m.jobs = append(m.jobs, job)
	if err := atomicJSON(filepath.Join(m.state, "history.json"), m.jobs); err != nil {
		m.jobs = m.jobs[:len(m.jobs)-1]
		return HostJob{}, errors.New("Cannot save host operation history; no operation started")
	}
	go m.run(job)
	return job, nil
}
func (m *hostManager) setJob(id, status, stage, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.jobs {
		if m.jobs[i].ID == id {
			m.jobs[i].Status, m.jobs[i].Stage, m.jobs[i].Message = status, stage, message
			if status != "running" {
				m.jobs[i].FinishedAt = time.Now().UTC().Format(time.RFC3339)
			}
			return atomicJSON(filepath.Join(m.state, "history.json"), m.jobs)
		}
	}
	return errors.New("Host operation not found")
}
func (m *hostManager) run(job HostJob) {
	fail := func(message string) { _ = m.setJob(job.ID, "failed", "failed", message) }
	// Fail closed even if an old update record is passed directly to the worker.
	if job.Kind != "refresh" {
		fail(hostUpdatesDisabled)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if m.setJob(job.ID, "running", "refreshing", "Refreshing signed package indexes; no packages are being installed") != nil {
		fail("Host history could not be saved. Inspect the host before retrying.")
		return
	}
	if _, err := m.commands.Run(ctx, "apt-get", "update", "-o", "APT::Update::Error-Mode=any"); err != nil {
		fail("Package list refresh failed. Check host repository access or package-manager locks. No Docker upgrade was attempted.")
		return
	}
	m.mu.Lock()
	m.cacheUpdated = time.Now().UTC().Format(time.RFC3339)
	err := atomicJSON(filepath.Join(m.state, "cache.json"), map[string]string{"updatedAt": m.cacheUpdated})
	m.mu.Unlock()
	if err != nil {
		fail("Package lists refreshed, but the check timestamp could not be saved")
		return
	}
	if err := m.setJob(job.ID, "succeeded", "complete", "Package lists refreshed; no packages installed"); err != nil {
		fail("Package lists refreshed, but final history could not be saved. Inspect the host before retrying.")
	}
}
func hostDecode(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		writeJSON(w, 400, map[string]string{"error": "Invalid host operation request"})
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "Use one JSON request"})
		return false
	}
	return true
}
func (m *hostManager) handler() http.Handler {
	mux := http.NewServeMux()
	respond := func(w http.ResponseWriter, value any, err error) {
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
		} else {
			writeJSON(w, 200, value)
		}
	}
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		writeJSON(w, 200, m.status(ctx))
	})
	for _, action := range []string{"review", "update"} {
		mux.HandleFunc("POST /"+action, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": hostUpdatesDisabled})
		})
	}
	mux.HandleFunc("POST /refresh", func(w http.ResponseWriter, r *http.Request) {
		var input struct{}
		if !hostDecode(w, r, &input) {
			return
		}
		job, err := m.begin("refresh")
		respond(w, job, err)
	})
	mux.HandleFunc("GET /jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, job := range m.jobs {
			if job.ID == r.PathValue("id") {
				writeJSON(w, 200, job)
				return
			}
		}
		writeJSON(w, 404, map[string]string{"error": "Host operation not found"})
	})
	return mux
}
func serveHostHelper() error {
	if os.Geteuid() != 0 {
		return errors.New("Install the host helper as a root systemd service")
	}
	osName, err := supportedHost()
	if err != nil {
		return err
	}
	manager, err := newHostManager(hostState, osName, systemHostCommands{})
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(hostSocket), 0750); err != nil {
		return err
	}
	if info, statErr := os.Lstat(hostSocket); statErr == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("Host helper socket path is occupied by a non-socket")
		}
		if err = os.Remove(hostSocket); err != nil {
			return err
		}
	}
	listener, err := net.Listen("unix", hostSocket)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err = os.Chmod(hostSocket, 0600); err != nil {
		return err
	}
	server := &http.Server{Handler: manager.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 75 * time.Second, IdleTimeout: 30 * time.Second}
	fmt.Println("Host helper listening on restricted local Unix socket")
	return server.Serve(listener)
}
func hostInspect(target string) error {
	osName, err := supportedHost()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	packages, err := inspectHostPackages(ctx, systemHostCommands{})
	if err != nil {
		return err
	}
	if target != "" {
		changes, err := packageChanges(packages, target)
		if err != nil {
			return err
		}
		if err = simulateHostUpdate(ctx, systemHostCommands{}, changes); err != nil {
			return err
		}
	}
	return json.NewEncoder(os.Stdout).Encode(HostStatus{Connected: false, OS: osName, Packages: packages, Message: strings.TrimSpace("Read-only inspection; simulation target: " + target)})
}
