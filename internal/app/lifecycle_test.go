package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type lifecycleFixture struct {
	mu                  sync.Mutex
	data                Inventory
	calls               []string
	failID, stateFailID string
	entered             chan struct{}
	release             chan struct{}
	after               func(*lifecycleFixture, string)
}

func (f *lifecycleFixture) Snapshot(context.Context) (Inventory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := f.data
	result.Containers = append([]Container{}, f.data.Containers...)
	return result, nil
}
func (f *lifecycleFixture) ProjectContainers(ctx context.Context, project string) ([]Container, error) {
	data, err := f.Snapshot(ctx)
	return filterProjectContainers(data.Containers, project), err
}
func (f *lifecycleFixture) ControlContainer(ctx context.Context, id, action string) error {
	if f.entered != nil {
		f.entered <- struct{}{}
		select {
		case <-f.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, action+" "+id)
	if id == f.failID {
		return errors.New("fixture failure must not reach the browser")
	}
	for i := range f.data.Containers {
		if f.data.Containers[i].ID == id {
			if action == "stop" {
				f.data.Containers[i].State = "exited"
			} else {
				f.data.Containers[i].State = "running"
			}
		}
	}
	if f.after != nil {
		f.after(f, id)
	}
	return nil
}
func (f *lifecycleFixture) ContainerState(_ context.Context, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id == f.stateFailID {
		return "", errors.New("fixture state unavailable")
	}
	for _, item := range f.data.Containers {
		if item.ID == id {
			return item.State, nil
		}
	}
	return "", errors.New("fixture container missing")
}
func lifecycleLibrary(t *testing.T) (*stackLibrary, *lifecycleFixture, *fixtureRunner) {
	t.Helper()
	l, _, runner := testLibrary(t)
	savedDraft(t, l)
	if job := startReviewed(t, l, ""); job.Status != "succeeded" {
		t.Fatal(job.Message)
	}
	record, _ := l.load("example")
	dir, _ := l.contentDirectory(record)
	f := &lifecycleFixture{data: Inventory{Containers: []Container{
		{ID: strings.Repeat("a", 64), Name: "example-web-1", Image: "fixture/web", Stack: "example", Service: "web", State: "running", ComposeDirectory: dir, ComposeFiles: filepath.Join(dir, "compose.yaml")},
		{ID: strings.Repeat("b", 64), Name: "example-web-2", Image: "fixture/web", Stack: "example", Service: "web", State: "running", ComposeDirectory: dir, ComposeFiles: filepath.Join(dir, "compose.yaml")},
	}}}
	l.source = f
	return l, f, runner
}
func runLifecycleFixture(t *testing.T, l *stackLibrary, action, id string) StackJob {
	t.Helper()
	plan, err := l.reviewAction(context.Background(), "example", id, action)
	if err != nil {
		t.Fatal(err)
	}
	job, err := l.startJob(context.Background(), "example", plan.ID, plan.Confirmation)
	if err != nil {
		t.Fatal(err)
	}
	return waitJob(t, l, job)
}
func TestLifecycleSingleContainerAndStackPreserveIdentityAndConfiguration(t *testing.T) {
	l, f, runner := lifecycleLibrary(t)
	before, _ := l.load("example")
	calls := len(runner.calls)
	id := f.data.Containers[0].ID
	for _, action := range []string{"stop", "start", "restart"} {
		job := runLifecycleFixture(t, l, action, id)
		if job.Status != "succeeded" || job.Completed != 1 || job.Total != 1 || job.ContainerID != id || job.ContainerName != "example-web-1" {
			t.Fatalf("Wrong single-container result: %+v", job)
		}
		if f.data.Containers[1].State != "running" {
			t.Fatal("A replica was affected")
		}
	}
	for _, action := range []string{"stop", "start", "restart"} {
		job := runLifecycleFixture(t, l, action, "")
		if job.Status != "succeeded" || job.Completed != 2 || job.Total != 2 || job.ContainerID != "" {
			t.Fatalf("Wrong stack result: %+v", job)
		}
	}
	after, _ := l.load("example")
	if before.AppliedRevision != after.AppliedRevision || before.DeployedAt != after.DeployedAt || before.Fingerprint != after.Fingerprint || len(runner.calls) != calls {
		t.Fatal("Lifecycle invoked Compose or changed deployment configuration")
	}
	if f.data.Containers[0].ID != id || f.data.Containers[1].ID != strings.Repeat("b", 64) {
		t.Fatal("Lifecycle recreated a container")
	}
	history, err := l.history("example")
	if err != nil || len(history) != 7 {
		t.Fatalf("Lifecycle history missing: %v", err)
	}
	reloaded := newLibrary(l.root, "", false, f, runner)
	persisted, err := reloaded.job(history[0].ID)
	if err != nil || persisted.Total == 0 || persisted.Completed != persisted.Total || persisted.Status != "succeeded" {
		t.Fatal("Lifecycle outcome lost across reload")
	}
}
func TestLifecycleScopeAndConfirmation(t *testing.T) {
	l, f, _ := lifecycleLibrary(t)
	id := f.data.Containers[0].ID
	plan, err := l.reviewAction(context.Background(), "example", id, "stop")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Confirmation != "example-web-1" || len(plan.Containers) != 1 {
		t.Fatal("Container confirmation is not scoped")
	}
	if _, err = l.startJob(context.Background(), "example", plan.ID, "example"); err == nil {
		t.Fatal("Stack name accepted for a container action")
	}
	if len(f.calls) != 0 {
		t.Fatal("Failed confirmation sent a Docker write")
	}
	l.jobsMu.Lock()
	expired := l.plans[plan.ID]
	expired.ExpiresAt = time.Now().Add(-time.Second)
	l.plans[plan.ID] = expired
	l.jobsMu.Unlock()
	if _, err = l.startJob(context.Background(), "example", plan.ID, plan.Confirmation); err == nil {
		t.Fatal("Expired review accepted")
	}
	f.data.Containers[1].State = "exited"
	plan, err = l.reviewAction(context.Background(), "example", "", "restart")
	if err != nil || len(plan.Containers) != 1 || plan.Containers[0].ID != id {
		t.Fatal("Restart included a stopped container")
	}
	job, err := l.startJob(context.Background(), "example", plan.ID, plan.Confirmation)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, l, job)
	if _, err = l.startJob(context.Background(), "example", plan.ID, plan.Confirmation); err == nil {
		t.Fatal("Review replay accepted")
	}
	if f.data.Containers[1].State != "exited" {
		t.Fatal("Restart started a stopped replica")
	}
}
func TestLifecycleKeepsUnappliedEditsSeparate(t *testing.T) {
	l, f, runner := lifecycleLibrary(t)
	before, _ := l.load("example")
	draft, _ := l.definition("example")
	_, err := l.edit(context.Background(), "example", editInput{Compose: strings.Replace(draft.Compose, "nginx:alpine", "nginx:stable", 1), Revision: draft.Fingerprint}, true)
	if err != nil {
		t.Fatal(err)
	}
	calls := len(runner.calls)
	job := runLifecycleFixture(t, l, "stop", f.data.Containers[0].ID)
	after, _ := l.load("example")
	if job.Status != "succeeded" || before.AppliedRevision != after.AppliedRevision || len(runner.calls) != calls {
		t.Fatal("Lifecycle applied saved edits or invoked Compose")
	}
}
func TestLifecycleRejectsUnmanagedAndStaleTargets(t *testing.T) {
	for _, change := range []string{"foreign", "oneoff", "imported", "draft", "paused", "missing", "state", "revision"} {
		t.Run(change, func(t *testing.T) {
			l, f, _ := lifecycleLibrary(t)
			plan, err := l.reviewAction(context.Background(), "example", "", "stop")
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "foreign":
				f.data.Containers[1].ComposeDirectory = "/data/compose/1"
			case "oneoff":
				f.data.Containers[0].ComposeOneOff = "True"
			case "imported":
				record, _ := l.load("example")
				record.Origin = "portainer"
				l.writeRecord(record)
			case "draft":
				record, _ := l.load("example")
				record.DeployedAt = ""
				record.LastDeploymentAttempt = nil
				l.writeRecord(record)
			case "paused":
				f.data.Containers[0].State = "paused"
			case "missing":
				f.data.Containers[0].ID = strings.Repeat("c", 64)
			case "state":
				f.data.Containers[0].State = "exited"
			case "revision":
				record, _ := l.definition("example")
				_, err = l.edit(context.Background(), "example", editInput{Compose: strings.Replace(record.Compose, "nginx:alpine", "nginx:stable", 1), Revision: record.Fingerprint}, true)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err = l.startJob(context.Background(), "example", plan.ID, plan.Confirmation); err == nil {
				t.Fatal("Stale or unsafe review accepted")
			}
			if len(f.calls) != 0 {
				t.Fatal("Rejected review modified containers")
			}
		})
	}
	l, _, _ := lifecycleLibrary(t)
	if _, err := l.reviewAction(context.Background(), "example", "", "delete"); err == nil {
		t.Fatal("Unsupported lifecycle action accepted")
	}
}
func TestLifecyclePartialFailuresStopFurtherActions(t *testing.T) {
	for _, mode := range []string{"docker", "replaced", "verify"} {
		t.Run(mode, func(t *testing.T) {
			l, f, _ := lifecycleLibrary(t)
			// Stack stop order is web-2 then web-1.
			if mode == "docker" {
				f.failID = f.data.Containers[0].ID
			}
			if mode == "verify" {
				f.stateFailID = f.data.Containers[1].ID
			}
			if mode == "replaced" {
				f.after = func(f *lifecycleFixture, id string) {
					if id == f.data.Containers[1].ID {
						f.data.Containers[0].ID = strings.Repeat("c", 64)
					}
				}
			}
			job := runLifecycleFixture(t, l, "stop", "")
			if job.Status != "failed" || !strings.Contains(job.Message, "Some containers may have changed") || strings.Contains(job.Message, "fixture failure") {
				t.Fatalf("Unsafe partial-failure result: %+v", job)
			}
			want := 1
			if mode == "docker" {
				want = 2
			}
			if len(f.calls) != want {
				t.Fatalf("Unexpected writes after failure: %v", f.calls)
			}
			if mode == "verify" && job.Completed != 0 {
				t.Fatal("Unverified change counted as successful")
			}
		})
	}
}
func TestLifecycleBusyAndHostOperationGuards(t *testing.T) {
	l, f, _ := lifecycleLibrary(t)
	plan, err := l.reviewAction(context.Background(), "example", "", "stop")
	if err != nil {
		t.Fatal(err)
	}
	m, _ := hostFixture(t)
	m.jobs = append(m.jobs, HostJob{ID: randomID(), Status: "running"})
	server := httptest.NewServer(m.handler())
	defer server.Close()
	base, _ := url.Parse(server.URL)
	l.host = &hostHelperClient{client: &http.Client{Transport: hostTestTransport{base, http.DefaultTransport}}}
	if _, err = l.startJob(context.Background(), "example", plan.ID, plan.Confirmation); err == nil {
		t.Fatal("Lifecycle allowed during host operation")
	}
	l.host = nil
	f.entered = make(chan struct{}, 2)
	f.release = make(chan struct{})
	job, err := l.startJob(context.Background(), "example", plan.ID, plan.Confirmation)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.entered:
	case <-time.After(time.Second):
		t.Fatal("Lifecycle did not start")
	}
	if _, err = l.startJob(context.Background(), "example", plan.ID, plan.Confirmation); err == nil {
		t.Fatal("Concurrent operation accepted")
	}
	definition, _ := l.definition("example")
	if _, err = l.edit(context.Background(), "example", editInput{Compose: definition.Compose, Revision: definition.Fingerprint}, true); err == nil {
		t.Fatal("Edit allowed during lifecycle operation")
	}
	close(f.release)
	if result := waitJob(t, l, job); result.Status != "succeeded" {
		t.Fatal(result.Message)
	}
}
func TestDockerLifecycleUsesOnlyFixedEndpoints(t *testing.T) {
	var calls []string
	status := 204
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		if r.URL.Path == "/version" {
			w.Write([]byte(`{"ApiVersion":"1.55","MinAPIVersion":"1.40"}`))
			return
		}
		if r.Method != "POST" || r.ContentLength > 0 {
			t.Error("Invalid Docker lifecycle request")
		}
		w.WriteHeader(status)
		w.Write([]byte("private-docker-error"))
	}))
	defer server.Close()
	d := &dockerSource{client: server.Client(), base: server.URL}
	id := strings.Repeat("a", 64)
	for _, action := range []string{"start", "stop", "restart"} {
		if err := d.ControlContainer(context.Background(), id, action); err != nil {
			t.Fatal(err)
		}
	}
	if calls[1] != "POST /v1.55/containers/"+id+"/start" || calls[3] != "POST /v1.55/containers/"+id+"/stop?t=30" || calls[5] != "POST /v1.55/containers/"+id+"/restart?t=30" {
		t.Fatalf("Wrong Docker endpoints: %v", calls)
	}
	before := len(calls)
	if d.ControlContainer(context.Background(), id, "kill") == nil || d.ControlContainer(context.Background(), "../other", "stop") == nil || len(calls) != before {
		t.Fatal("Unsafe endpoint was dispatched")
	}
	status = 500
	if err := d.ControlContainer(context.Background(), id, "stop"); err == nil || strings.Contains(err.Error(), "private-docker-error") {
		t.Fatal("Docker error body exposed")
	}
}
func TestLifecycleHTTPRequiresCSRFAndConfirmation(t *testing.T) {
	l, f, _ := lifecycleLibrary(t)
	handler := newHandler(f, l)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/session", nil))
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	json.Unmarshal(w.Body.Bytes(), &session)
	post := func(path, body, token, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Stacklane-CSRF", token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if post("/api/stacks/example/review", `{"action":"stop"}`, "wrong", "http://localhost").Code != 403 || post("/api/stacks/example/review", `{"action":"stop"}`, session.CSRFToken, "http://attacker.example").Code != 403 {
		t.Fatal("Lifecycle review accepted missing CSRF or wrong origin")
	}
	w = post("/api/stacks/example/review", `{"action":"stop"}`, session.CSRFToken, "http://localhost")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var plan UpdatePlan
	json.Unmarshal(w.Body.Bytes(), &plan)
	if post("/api/stacks/example/operations", `{"planId":"`+plan.ID+`","confirmation":"wrong"}`, session.CSRFToken, "http://localhost").Code != 400 || len(f.calls) != 0 {
		t.Fatal("Lifecycle started without confirmation")
	}
}
