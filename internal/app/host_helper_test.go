package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeHostCommands struct {
	mu                               sync.Mutex
	installed                        map[string]string
	candidate                        map[string]string
	held, badSimulation, failInstall bool
	installs, refreshes              int
}

func fakeHost() *fakeHostCommands {
	f := &fakeHostCommands{installed: map[string]string{}, candidate: map[string]string{}}
	for _, name := range hostPackageNames {
		f.installed[name] = "1.0"
		f.candidate[name] = "2.0"
	}
	return f
}
func (f *fakeHostCommands) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch name {
	case "apt-mark":
		if f.held {
			return []byte("docker-compose-plugin\n"), nil
		}
		return nil, nil
	case "dpkg-query":
		p := args[len(args)-1]
		return []byte("installed|" + f.installed[p]), nil
	case "apt-cache":
		p := args[len(args)-1]
		return []byte(fmt.Sprintf("%s:\n Installed: %s\n Candidate: %s\n Version table:\n %s 500\n  500 https://download.docker.com/linux/ubuntu noble/stable amd64 Packages\n", p, f.installed[p], f.candidate[p], f.candidate[p])), nil
	case "dpkg":
		if args[1] == args[3] {
			return nil, errors.New("not greater")
		}
		return nil, nil
	case "systemctl":
		return nil, nil
	case "apt-get":
		if args[0] == "update" {
			f.refreshes++
			return nil, nil
		}
		simulate := false
		for _, arg := range args {
			if arg == "--simulate" {
				simulate = true
			}
		}
		if simulate {
			if f.badSimulation {
				return []byte("Inst unrelated-package (9.0 fixture [amd64])\n"), nil
			}
			output := ""
			for _, arg := range args {
				p, v, ok := strings.Cut(arg, "=")
				if ok && f.installed[p] != "" {
					output += fmt.Sprintf("Inst %s [%s] (%s fixture [amd64])\nConf %s (%s fixture [amd64])\n", p, f.installed[p], v, p, v)
				}
			}
			return []byte(output), nil
		}
		f.installs++
		if f.failInstall {
			return nil, errors.New("fixture failure with secret diagnostic")
		}
		for _, arg := range args {
			p, v, ok := strings.Cut(arg, "=")
			if ok && f.installed[p] != "" {
				f.installed[p] = v
			}
		}
		return nil, nil
	}
	return nil, errors.New("unexpected command")
}
func TestPackagePolicyRejectsUntrustedOrigins(t *testing.T) {
	base := "Candidate: 2.0\n 2.0 500\n  500 %s noble/stable amd64 Packages\n"
	for _, origin := range []struct {
		uri     string
		trusted bool
	}{{"https://download.docker.com/linux/ubuntu", true}, {"http://download.docker.com/linux/ubuntu", false}, {"https://evil.example/linux/ubuntu", false}, {"https://user:fixture@download.docker.com/linux/ubuntu", false}, {"file:/tmp/packages", false}} {
		result := parsePackagePolicy("docker-ce", "1.0", fmt.Sprintf(base, origin.uri))
		if result.Trusted != origin.trusted {
			t.Fatal("incorrect origin policy", origin.uri)
		}
	}
	both := fmt.Sprintf(base, "https://download.docker.com/linux/ubuntu") + "  500 https://evil.example noble/stable amd64 Packages\n"
	if parsePackagePolicy("docker-ce", "1.0", both).Trusted {
		t.Fatal("mixed candidate origins accepted")
	}
	oldOrigin := "Candidate: 2.0\n 2.0 500\n  500 https://evil.example noble/stable amd64 Packages\n 1.0 500\n  500 https://download.docker.com/linux/ubuntu noble/stable amd64 Packages\n"
	if parsePackagePolicy("docker-ce", "1.0", oldOrigin).Trusted {
		t.Fatal("trusted old version authorized untrusted candidate")
	}
}
func hostFixture(t *testing.T) (*hostManager, *fakeHostCommands) {
	t.Helper()
	f := fakeHost()
	m, err := newHostManager(t.TempDir(), "ubuntu", f)
	if err != nil {
		t.Fatal(err)
	}
	return m, f
}
func waitHostJob(t *testing.T, m *hostManager, id string) HostJob {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		for _, job := range m.jobs {
			if job.ID == id && job.Status != "running" {
				m.mu.Unlock()
				return job
			}
		}
		m.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("host job did not finish")
	return HostJob{}
}
func TestHostInstallationDisabledAtHelperAndWorker(t *testing.T) {
	m, f := hostFixture(t)
	for _, kind := range []string{"update", "install", "upgrade", ""} {
		if _, err := m.begin(kind); err == nil || err.Error() != hostUpdatesDisabled {
			t.Fatalf("%q was not rejected: %v", kind, err)
		}
	}
	for _, path := range []string{"/review", "/update"} {
		request := httptest.NewRequest("POST", "http://localhost"+path, strings.NewReader(`{"target":"compose","planId":"previous-review","confirmation":"UPDATE COMPOSE","acknowledge":true}`))
		result := httptest.NewRecorder()
		m.handler().ServeHTTP(result, request)
		if result.Code != http.StatusForbidden || !strings.Contains(result.Body.String(), hostUpdatesDisabled) {
			t.Fatalf("helper %s: %d %s", path, result.Code, result.Body.String())
		}
	}
	if len(m.jobs) != 0 {
		t.Fatal("disabled requests created a host job")
	}
	legacy := HostJob{ID: randomID(), Kind: "update", Status: "running", Stage: "queued"}
	m.jobs = append(m.jobs, legacy)
	m.run(legacy)
	if m.jobs[0].Status != "failed" || m.jobs[0].Message != hostUpdatesDisabled {
		t.Fatal("worker accepted a legacy update")
	}
	if f.installs != 0 || f.refreshes != 0 || f.installed["docker-compose-plugin"] != "1.0" {
		t.Fatal("disabled operations changed host packages")
	}
}
func TestHostInspectionAndSimulationsRemainAvailable(t *testing.T) {
	m, f := hostFixture(t)
	status := m.status(context.Background())
	if !status.Connected || status.UpdatesEnabled || len(status.Packages) != len(hostPackageNames) {
		t.Fatal("incorrect host capabilities", status)
	}
	for _, target := range []string{"engine", "compose"} {
		changes, err := packageChanges(status.Packages, target)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range changes {
			if (item.Name == "docker-compose-plugin") != (target == "compose") {
				t.Fatal("incorrect simulation scope")
			}
		}
		if err := simulateHostUpdate(context.Background(), f, changes); err != nil {
			t.Fatal(err)
		}
	}
	f.held = true
	packages, err := inspectHostPackages(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := packageChanges(packages, "compose"); err == nil {
		t.Fatal("simulation ignored package hold")
	}
	f.badSimulation = true
	changes, err := packageChanges(status.Packages, "engine")
	if err != nil {
		t.Fatal(err)
	}
	if err := simulateHostUpdate(context.Background(), f, changes); err == nil {
		t.Fatal("unreviewed dependency accepted")
	}
	if f.installs != 0 || f.refreshes != 0 {
		t.Fatal("inspection or simulation modified packages")
	}
}
func TestHostAPTRejectsPackageWrites(t *testing.T) {
	changes := []HostPackage{{Name: "docker-ce", Candidate: "2.0"}}
	if !allowedHostAPT(aptSimulationArgs(changes)) || !allowedHostAPT([]string{"update", "-o", "APT::Update::Error-Mode=any"}) {
		t.Fatal("safe package check rejected")
	}
	unsafe := [][]string{
		{"install", "docker-ce=2.0"}, {"upgrade"}, {"remove", "docker-ce"},
		{"--simulate", "-o", "APT::Get::Simulate=false", "install", "docker-ce=2.0"},
		append(aptSimulationArgs(changes), "-oAPT::Get::Simulate=false"),
		append(aptSimulationArgs(changes), "unrelated=1.0"),
		{"update", "-o", "APT::Update::Post-Invoke::=unexpected-command"},
	}
	for _, args := range unsafe {
		if allowedHostAPT(args) {
			t.Fatalf("unsafe APT arguments accepted: %v", args)
		}
		// Rejection must happen before looking for an APT executable, on any OS.
		if _, err := (systemHostCommands{}).Run(context.Background(), "apt-get", args...); err == nil || err.Error() != hostUpdatesDisabled {
			t.Fatalf("executor did not block %v: %v", args, err)
		}
	}
}
func TestSimulationRejectsRemovalWrongVersionAndExtraConfiguration(t *testing.T) {
	changes := []HostPackage{{Name: "docker-ce", Installed: "1.0", Candidate: "2.0"}}
	for _, raw := range []string{"Remv docker-ce [1.0]\n", "Inst docker-ce [1.0] (3.0 fixture [amd64])\n", "Inst docker-ce [1.0] (2.0 fixture [amd64])\nConf unrelated (1.0 fixture)\n", "0 upgraded, 0 newly installed"} {
		if validateSimulation([]byte(raw), changes) == nil {
			t.Fatal("unsafe simulation accepted")
		}
	}
}
func TestRefreshAndInterruptedHistoryNeverInstallPackages(t *testing.T) {
	m, f := hostFixture(t)
	job, err := m.begin("refresh")
	if err != nil {
		t.Fatal(err)
	}
	if waitHostJob(t, m, job.ID).Status != "succeeded" {
		t.Fatal("refresh failed")
	}
	f.mu.Lock()
	if f.installs != 0 || f.refreshes != 1 {
		t.Error("refresh installed packages")
	}
	f.mu.Unlock()
	m.jobs = append(m.jobs, HostJob{ID: randomID(), Kind: "update", Target: "engine", Status: "running", Stage: "upgrading"})
	if err = atomicJSON(filepath.Join(m.state, "history.json"), m.jobs); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newHostManager(m.state, "ubuntu", f)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.jobs[0].Status != "succeeded" || reloaded.jobs[1].Status != "interrupted" || reloaded.jobs[1].Kind != "update" || reloaded.cacheUpdated == "" {
		t.Fatal("interrupted upgrade assumed successful")
	}
}

type hostTestTransport struct {
	base     *url.URL
	delegate http.RoundTripper
}

func (t hostTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Scheme = t.base.Scheme
	r.URL.Host = t.base.Host
	return t.delegate.RoundTrip(r)
}
func TestHostRoutesRequireCSRFAndCoordinateWithStackJobs(t *testing.T) {
	m, _ := hostFixture(t)
	server := httptest.NewServer(m.handler())
	defer server.Close()
	base, _ := url.Parse(server.URL)
	l, source, _ := testLibrary(t)
	l.host = &hostHelperClient{client: &http.Client{Transport: hostTestTransport{base, http.DefaultTransport}}}
	handler := newHandler(source, l)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "http://localhost/api/session", nil))
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	json.Unmarshal(response.Body.Bytes(), &session)
	post := func(token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "http://localhost/api/system/refresh", strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "http://localhost")
		request.Header.Set("X-Dockspire-CSRF", token)
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		return result
	}
	if post("wrong").Code != 403 {
		t.Fatal("host update route accepted no CSRF token")
	}
	l.active["example"] = "job"
	if post(session.CSRFToken).Code != 400 {
		t.Fatal("host review allowed during stack operation")
	}
	delete(l.active, "example")
	result := post(session.CSRFToken)
	if result.Code != 200 {
		t.Fatal(result.Body.String())
	}
	var refresh HostJob
	if err := json.Unmarshal(result.Body.Bytes(), &refresh); err != nil {
		t.Fatal(err)
	}
	waitHostJob(t, m, refresh.ID)
	m.mu.Lock()
	m.jobs = append(m.jobs, HostJob{ID: randomID(), Status: "running"})
	m.mu.Unlock()
	if err := l.host.available(context.Background()); err == nil {
		t.Fatal("stack updates allowed during host maintenance")
	}
}

func TestAppBlocksInstallationWithLegacyOrMissingHelper(t *testing.T) {
	posts := 0
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts++
		}
		writeJSON(w, 200, HostStatus{Connected: true, UpdatesEnabled: true, Packages: []HostPackage{{Name: "docker-ce", Installed: "1.0", Candidate: "2.0"}}, History: []HostJob{{ID: "old-job", Kind: "update", Status: "succeeded"}}})
	}))
	defer legacy.Close()
	base, _ := url.Parse(legacy.URL)
	l, source, _ := testLibrary(t)
	handler := newHandler(source, l)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "http://localhost/api/session", nil))
	var session struct{ CSRFToken string }
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	for _, connected := range []bool{false, true} {
		if connected {
			l.host = &hostHelperClient{client: &http.Client{Transport: hostTestTransport{base, http.DefaultTransport}}}
		}
		for _, action := range []string{"review", "update"} {
			request := httptest.NewRequest("POST", "http://localhost/api/system/"+action, strings.NewReader(`{"target":"engine","planId":"old-plan","confirmation":"UPDATE DOCKER","acknowledge":true}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", "http://localhost")
			request.Header.Set("X-Dockspire-CSRF", session.CSRFToken)
			result := httptest.NewRecorder()
			handler.ServeHTTP(result, request)
			if result.Code != http.StatusForbidden || !strings.Contains(result.Body.String(), hostUpdatesDisabled) {
				t.Fatalf("app %s connected=%v: %d %s", action, connected, result.Code, result.Body.String())
			}
		}
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, httptest.NewRequest("GET", "http://localhost/api/system/host", nil))
		var status HostStatus
		if err := json.Unmarshal(result.Body.Bytes(), &status); err != nil || status.UpdatesEnabled {
			t.Fatal("app advertised host package installation", result.Body.String())
		}
		if connected && (len(status.Packages) != 1 || len(status.History) != 1 || status.History[0].ID != "old-job") {
			t.Fatal("version information or historical operations were lost")
		}
	}
	if posts != 0 {
		t.Fatal("disabled requests were forwarded to the older helper")
	}
}
