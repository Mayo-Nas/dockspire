package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProjectContainersUsesOnlyFreshVersionAndFilteredContainers(t *testing.T) {
	var requests []string
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		if r.Method != "GET" {
			t.Error("project read attempted a write")
		}
		switch r.URL.Path {
		case "/version":
			_, _ = w.Write([]byte(`{"ApiVersion":"1.55","MinAPIVersion":"1.44"}`))
		case "/v1.55/containers/json":
			var filters map[string][]string
			if r.URL.Query().Get("all") != "true" || json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filters) != nil || len(filters["label"]) != 1 || filters["label"][0] != "com.docker.compose.project=example" {
				t.Error("missing exact project filter or stopped containers")
			}
			reads++
			state := "running"
			if reads == 2 {
				state = "exited"
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"Id": "one", "Names": []string{"/example-web-1"}, "ImageID": "sha256:" + strings.Repeat("a", 64), "State": state, "Labels": map[string]string{"com.docker.compose.project": "example", "com.docker.compose.service": "web", "com.docker.compose.oneoff": "True", "com.docker.compose.project.working_dir": "/private/source", "private.token": "fixture-sensitive"}},
				{"Id": "foreign", "Labels": map[string]string{"com.docker.compose.project": "other"}},
			})
		default:
			t.Error("project read requested unrelated inventory", r.URL.Path)
			http.Error(w, "unavailable", 500)
		}
	}))
	defer server.Close()
	source := &dockerSource{client: server.Client(), base: server.URL}
	for _, state := range []string{"running", "exited"} {
		containers, err := source.ProjectContainers(context.Background(), "example")
		if err != nil || len(containers) != 1 || containers[0].State != state || containers[0].ComposeOneOff != "True" || containers[0].Service != "web" || containers[0].ImageID != "sha256:"+strings.Repeat("a", 64) {
			t.Fatal("fresh scoped metadata lost", containers, err)
		}
		encoded, _ := json.Marshal(containers)
		if strings.Contains(string(encoded), "fixture-sensitive") || strings.Contains(string(encoded), "/private/source") {
			t.Fatal("internal Docker fields leaked")
		}
	}
	if strings.Join(requests, ",") != "/version,/v1.55/containers/json,/version,/v1.55/containers/json" {
		t.Fatal(requests)
	}
	before := len(requests)
	if _, err := source.ProjectContainers(context.Background(), "../invalid"); err == nil || len(requests) != before {
		t.Fatal("invalid project reached Docker")
	}
}

func TestStackChecksWorkWhenUnrelatedDockerInventoryFails(t *testing.T) {
	var unrelated atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			_, _ = w.Write([]byte(`{"ApiVersion":"1.55","MinAPIVersion":"1.44"}`))
		case "/v1.55/containers/json":
			_, _ = w.Write([]byte(`[]`))
		default:
			unrelated.Add(1)
			http.Error(w, "fixture-sensitive-docker-error", 500)
		}
	}))
	defer server.Close()
	source := &dockerSource{client: server.Client(), base: server.URL}
	l := newLibrary(t.TempDir(), "", false, source, &fixtureRunner{})
	if _, err := l.create(context.Background(), stackInput{Name: "example", Compose: testCompose}); err != nil {
		t.Fatal("draft depended on unrelated inventory", err)
	}
	if _, err := l.review(context.Background(), "example", ""); err != nil {
		t.Fatal("review depended on unrelated inventory", err)
	}
	if unrelated.Load() != 0 {
		t.Fatal("stack checks queried full inventory")
	}
	if _, err := source.Snapshot(context.Background()); err == nil || strings.Contains(err.Error(), "fixture-sensitive") {
		t.Fatal("full inventory error handling changed", err)
	}
}

func TestProjectContainerErrorsAndCancellationRemainSafe(t *testing.T) {
	for _, mode := range []string{"error", "incompatible", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "cancel" {
					entered <- struct{}{}
					<-r.Context().Done()
					return
				}
				if r.URL.Path == "/version" {
					if mode == "incompatible" {
						_, _ = w.Write([]byte(`{"ApiVersion":"1.43","MinAPIVersion":"1.24"}`))
					} else {
						_, _ = w.Write([]byte(`{"ApiVersion":"1.55","MinAPIVersion":"1.44"}`))
					}
					return
				}
				http.Error(w, "fixture-sensitive-body", 500)
			}))
			defer server.Close()
			source := &dockerSource{client: server.Client(), base: server.URL}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := source.ProjectContainers(ctx, "example"); result <- err }()
			if mode == "cancel" {
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					t.Fatal("request did not start")
				}
				cancel()
			}
			err := receiveError(t, result)
			if err == nil || strings.Contains(err.Error(), "fixture-sensitive") {
				t.Fatal("unsafe or successful failed read", err)
			}
		})
	}
}

func receiveError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("operation blocked while unrelated external work was pending")
		return nil
	}
}

func coordinationLibrary(t *testing.T) (*stackLibrary, *lifecycleFixture) {
	t.Helper()
	source := &lifecycleFixture{data: Inventory{Containers: []Container{}, Stacks: []Stack{}}}
	l := newLibrary(t.TempDir(), "", false, source, &fixtureRunner{})
	for _, name := range []string{"example", "other"} {
		if _, err := l.create(context.Background(), stackInput{Name: name, Compose: testCompose}); err != nil {
			t.Fatal(err)
		}
	}
	return l, source
}

type validationBlocker struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func (r *validationBlocker) Run(ctx context.Context, _ string, _ string, _ ...string) ([]byte, error) {
	r.calls.Add(1)
	var err error
	r.once.Do(func() {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			err = ctx.Err()
		}
	})
	return nil, err
}

func newValidationBlocker(t *testing.T) *validationBlocker {
	r := &validationBlocker{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-r.release:
		default:
			close(r.release)
		}
	})
	return r
}

func waitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("fixture did not reach expected phase")
	}
}

func TestSlowValidationDoesNotBlockOtherReviewOrLifecycle(t *testing.T) {
	l, source := coordinationLibrary(t)
	other, _ := l.load("other")
	other.DeployedAt = time.Now().UTC().Format(time.RFC3339)
	if err := l.writeRecord(other); err != nil {
		t.Fatal(err)
	}
	dir, _ := l.directory("other")
	source.data.Containers = []Container{{ID: strings.Repeat("a", 64), Name: "other-web-1", Stack: "other", Service: "web", State: "exited", ComposeDirectory: dir, ComposeFiles: filepath.Join(dir, "compose.yaml")}}
	draft, _ := l.definition("example")
	blocker := newValidationBlocker(t)
	l.runner = blocker
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	editResult := make(chan error, 1)
	go func() {
		_, err := l.edit(ctx, "example", editInput{Compose: draft.Compose, Revision: draft.Fingerprint}, true)
		editResult <- err
	}()
	waitSignal(t, blocker.entered)
	if !l.mu.TryLock() {
		t.Fatal("validation still holds global mutation lock")
	}
	l.mu.Unlock()
	otherResult := make(chan error, 1)
	acceptedJob := make(chan StackJob, 1)
	go func() {
		plan, err := l.reviewAction(ctx, "other", "", "start")
		if err == nil {
			var job StackJob
			job, err = l.startJob(ctx, "other", plan.ID, "other")
			if err == nil {
				acceptedJob <- job
			}
		}
		otherResult <- err
	}()
	if err := receiveError(t, otherResult); err != nil {
		t.Fatal("unrelated lifecycle blocked", err)
	}
	if result := waitJob(t, l, <-acceptedJob); result.Status != "succeeded" {
		t.Fatal("unrelated lifecycle did not complete", result.Message)
	}
	close(blocker.release)
	if err := receiveError(t, editResult); err != nil {
		t.Fatal(err)
	}
}

func waitValidationDirectories(t *testing.T, l *stackLibrary, count int) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		entries, _ := os.ReadDir(filepath.Join(l.root, "tmp"))
		if len(entries) >= count {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatal("validators did not stage their immutable inputs")
		}
	}
}

func TestConcurrentEditorsCommitOnlyOneVersion(t *testing.T) {
	for _, sameContent := range []bool{false, true} {
		t.Run(map[bool]string{false: "different_content", true: "same_content"}[sameContent], func(t *testing.T) {
			l, _ := coordinationLibrary(t)
			draft, _ := l.definition("example")
			if _, err := l.edit(context.Background(), "example", editInput{Compose: draft.Compose, Revision: draft.Fingerprint}, true); err != nil {
				t.Fatal(err)
			}
			draft, _ = l.definition("example")
			l.composeSlot <- struct{}{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			results := make(chan error, 2)
			for _, tag := range []string{"stable", "mainline"} {
				compose := draft.Compose
				if !sameContent {
					compose = strings.Replace(compose, "nginx:alpine", "nginx:"+tag, 1)
				}
				go func(compose string) {
					_, err := l.edit(ctx, "example", editInput{Compose: compose, Revision: draft.Fingerprint}, true)
					results <- err
				}(compose)
			}
			waitValidationDirectories(t, l, 2)
			<-l.composeSlot
			succeeded := 0
			for i := 0; i < 2; i++ {
				if receiveError(t, results) == nil {
					succeeded++
				}
			}
			after, err := l.definition("example")
			if err != nil || succeeded != 1 || after.EditVersion != draft.EditVersion+1 {
				t.Fatal("concurrent editor overwrote a save", succeeded, err)
			}
			if sameContent && after.Fingerprint != draft.Fingerprint {
				t.Fatal("same-content case changed its fingerprint")
			}
		})
	}
}

func TestQueuedValidationCancelsWithoutStartingCompose(t *testing.T) {
	l, _ := coordinationLibrary(t)
	l.composeSlot <- struct{}{}
	defer func() { <-l.composeSlot }()
	runner := &fixtureRunner{}
	l.runner = runner
	draft, _ := l.definition("example")
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := l.edit(ctx, "example", editInput{Compose: draft.Compose, Revision: draft.Fingerprint}, true)
		result <- err
	}()
	waitValidationDirectories(t, l, 1)
	cancel()
	if err := receiveError(t, result); !errors.Is(err, context.Canceled) || len(runner.calls) != 0 {
		t.Fatal("canceled validator reached Compose", err)
	}
	after, _ := l.definition("example")
	if after.EditVersion != draft.EditVersion {
		t.Fatal("canceled validator saved a revision")
	}
	entries, err := os.ReadDir(filepath.Join(l.root, "tmp"))
	if err != nil || len(entries) != 0 {
		t.Fatal("validation files were not removed", err)
	}
}

func TestAcceptedDeploymentBlocksOlderPendingEditor(t *testing.T) {
	l, _ := coordinationLibrary(t)
	draft, _ := l.definition("example")
	blocker := newValidationBlocker(t)
	l.runner = blocker
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := l.edit(ctx, "example", editInput{Compose: strings.Replace(draft.Compose, "nginx:alpine", "nginx:stable", 1), Revision: draft.Fingerprint}, true)
		result <- err
	}()
	waitSignal(t, blocker.entered)
	plan, err := l.review(ctx, "example", "")
	if err != nil {
		t.Fatal(err)
	}
	job, err := l.startJob(ctx, "example", plan.ID, "example")
	if err != nil {
		t.Fatal(err)
	}
	close(blocker.release)
	if err := receiveError(t, result); err == nil {
		t.Fatal("pending editor replaced an accepted deployment")
	}
	if result := waitJob(t, l, job); result.Status != "succeeded" {
		t.Fatal(result.Message)
	}
	after, err := l.definition("example")
	if err != nil || after.EditVersion != draft.EditVersion || after.AppliedRevision != draft.Fingerprint || after.DeployedAt == "" {
		t.Fatal("pending editor overwrote deployment metadata", err)
	}
}

type projectReadBlocker struct {
	*lifecycleFixture
	project string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *projectReadBlocker) ProjectContainers(ctx context.Context, project string) ([]Container, error) {
	if project == s.project {
		var err error
		s.once.Do(func() {
			close(s.entered)
			select {
			case <-s.release:
			case <-ctx.Done():
				err = ctx.Err()
			}
		})
		if err != nil {
			return nil, err
		}
	}
	return s.lifecycleFixture.ProjectContainers(ctx, project)
}

func TestSlowReviewAllowsEditsAndRejectsItsStaleResult(t *testing.T) {
	l, source := coordinationLibrary(t)
	blocker := &projectReadBlocker{lifecycleFixture: source, project: "example", entered: make(chan struct{}), release: make(chan struct{})}
	l.source = blocker
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := l.review(ctx, "example", ""); result <- err }()
	waitSignal(t, blocker.entered)
	otherResult := make(chan error, 1)
	go func() { _, err := l.review(ctx, "other", ""); otherResult <- err }()
	if err := receiveError(t, otherResult); err != nil {
		t.Fatal(err)
	}
	draft, _ := l.definition("example")
	if _, err := l.edit(ctx, "example", editInput{Compose: draft.Compose, Revision: draft.Fingerprint}, true); err != nil {
		t.Fatal("slow review blocked a save", err)
	}
	close(blocker.release)
	if err := receiveError(t, result); err == nil {
		t.Fatal("stale review was published after a concurrent edit")
	}
}

func TestPendingAcceptanceReservesSlotAndCancellationReleasesIt(t *testing.T) {
	l, source := coordinationLibrary(t)
	first, err := l.review(context.Background(), "example", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := l.review(context.Background(), "other", "")
	if err != nil {
		t.Fatal(err)
	}
	blocker := &projectReadBlocker{lifecycleFixture: source, project: "example", entered: make(chan struct{}), release: make(chan struct{})}
	l.source = blocker
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := l.startJob(ctx, "example", first.ID, "example"); result <- err }()
	waitSignal(t, blocker.entered)
	if !l.isBusy("example") {
		t.Fatal("acceptance did not reserve the target")
	}
	if _, err := l.startJob(context.Background(), "other", other.ID, "other"); err == nil {
		t.Fatal("second operation bypassed pending acceptance")
	}
	draft, _ := l.definition("example")
	if _, err := l.edit(context.Background(), "example", editInput{Compose: draft.Compose, Revision: draft.Fingerprint}, true); err == nil {
		t.Fatal("save bypassed pending acceptance")
	}
	otherDraft, _ := l.definition("other")
	if _, err := l.edit(context.Background(), "other", editInput{Compose: otherDraft.Compose, Revision: otherDraft.Fingerprint}, true); err != nil {
		t.Fatal("unrelated edit was blocked", err)
	}
	cancel()
	if err := receiveError(t, result); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if l.isBusy("example") {
		t.Fatal("canceled acceptance leaked reservation")
	}
	l.source = source
	job, err := l.startJob(context.Background(), "example", first.ID, "example")
	if err != nil {
		t.Fatal("cancellation consumed the review", err)
	}
	if result := waitJob(t, l, job); result.Status != "succeeded" {
		t.Fatal(result.Message)
	}
}

func TestAcceptancePublicationFailureReleasesReservation(t *testing.T) {
	l, _ := coordinationLibrary(t)
	plan, err := l.review(context.Background(), "example", "")
	if err != nil {
		t.Fatal(err)
	}
	jobsPath := filepath.Join(l.root, "jobs")
	if err := os.WriteFile(jobsPath, []byte("fixture storage obstruction"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &fixtureRunner{}
	l.runner = runner
	if _, err := l.startJob(context.Background(), "example", plan.ID, "example"); err == nil {
		t.Fatal("operation accepted without durable history")
	}
	if l.isBusy("example") || len(runner.calls) != 0 || len(l.jobs) != 0 {
		t.Fatal("failed publication retained its reservation or started work")
	}
	if err := os.Remove(jobsPath); err != nil {
		t.Fatal(err)
	}
	job, err := l.startJob(context.Background(), "example", plan.ID, "example")
	if err != nil {
		t.Fatal("failed acceptance consumed its review", err)
	}
	if result := waitJob(t, l, job); result.Status != "succeeded" {
		t.Fatal(result.Message)
	}
}

type applyCheckSource struct {
	*lifecycleFixture
	library *stackLibrary
	reads   int
	unsafe  bool
}

func (s *applyCheckSource) ProjectContainers(ctx context.Context, project string) ([]Container, error) {
	s.reads++
	if s.reads == 3 {
		if len(s.library.composeSlot) != 1 {
			s.unsafe = true
		}
		if !s.library.mu.TryLock() {
			s.unsafe = true
		} else {
			s.library.mu.Unlock()
		}
	}
	return s.lifecycleFixture.ProjectContainers(ctx, project)
}

func TestFinalApplyReadHoldsComposeSlotWithoutMetadataLock(t *testing.T) {
	l, source := coordinationLibrary(t)
	checks := &applyCheckSource{lifecycleFixture: source, library: l}
	l.source = checks
	job := startReviewed(t, l, "")
	// Three ownership reads plus one diagnostic observation after apply.
	if job.Status != "succeeded" || checks.reads != 4 || checks.unsafe {
		t.Fatal("final checks preceded queue acquisition or held global lock", checks.reads, checks.unsafe)
	}
}
