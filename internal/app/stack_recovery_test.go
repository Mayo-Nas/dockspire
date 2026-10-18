package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This runner models a first apply that creates one service before failing.
// It checks durable intent at the boundary where a real Compose write would run.
type partialDeploymentRunner struct {
	library     *stackLibrary
	source      *lifecycleFixture
	failUp      bool
	failPull    bool
	calls       [][]string
	intentError error
	afterPull   func()
}

func (r *partialDeploymentRunner) Run(_ context.Context, name, directory string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{}, args...))
	if args[0] == "pull" && r.failPull {
		return nil, errors.New("fixture pull failed")
	}
	if args[0] == "pull" && r.afterPull != nil {
		r.afterPull()
	}
	if args[0] != "up" {
		return nil, nil
	}
	record, err := r.library.load(name)
	if err != nil || record.LastDeploymentAttempt == nil {
		r.intentError = errors.New("apply started without a persisted attempt")
		return nil, r.intentError
	}
	attempt := record.LastDeploymentAttempt
	job, err := r.library.job(attempt.OperationID)
	if err != nil || job.Revision != attempt.Revision || job.ApplyStartedAt == "" || job.Stage != "applying" {
		r.intentError = errors.New("apply started without persisted revision and phase")
		return nil, r.intentError
	}
	r.source.mu.Lock()
	if len(r.source.data.Containers) == 0 {
		r.source.data.Containers = []Container{{ID: strings.Repeat("a", 64), Name: name + "-web-1", Image: "fixture/web", Stack: name, Service: "web", State: "running", ComposeDirectory: directory, ComposeFiles: filepath.Join(directory, "compose.yaml")}}
	}
	r.source.data.Stacks = groupStacks(r.source.data.Containers)
	r.source.mu.Unlock()
	if r.failUp {
		return nil, errors.New("fixture apply failed after creating a container")
	}
	return nil, nil
}

func recoveryLibrary(t *testing.T) (*stackLibrary, *lifecycleFixture, *partialDeploymentRunner) {
	t.Helper()
	source := &lifecycleFixture{data: Inventory{Containers: []Container{}, Stacks: []Stack{}}}
	l := newLibrary(t.TempDir(), "", false, source, nil)
	runner := &partialDeploymentRunner{library: l, source: source, failUp: true}
	l.runner = runner
	_, err := l.create(context.Background(), stackInput{Name: "example", Compose: "services:\n  web:\n    image: fixture/web\n  db:\n    image: fixture/db\n", Environment: "KEY=fixture-value\n"})
	if err != nil {
		t.Fatal(err)
	}
	return l, source, runner
}

func TestPartialFirstDeploymentCanBeReviewedAndRecoveredAfterRestart(t *testing.T) {
	l, source, runner := recoveryLibrary(t)
	failed := startReviewed(t, l, "")
	if failed.Status != "failed" || runner.intentError != nil {
		t.Fatalf("unexpected first apply: %s, %v", failed.Status, runner.intentError)
	}
	record, err := l.load("example")
	if err != nil || record.DeployedAt != "" || record.AppliedRevision != "" || record.LastDeploymentAttempt == nil || record.LastDeploymentAttempt.OperationID != failed.ID {
		t.Fatalf("partial apply was lost or recorded as successful: %#v, %v", record, err)
	}
	if failed.Revision != record.LastDeploymentAttempt.Revision || failed.ApplyStartedAt == "" {
		t.Fatal("attempt history omitted revision or apply phase")
	}
	reloaded := newLibrary(l.root, "", false, source, runner)
	runner.library, runner.failUp = reloaded, false
	definition, err := reloaded.definition("example")
	if err != nil || !definition.RecoveryRequired {
		t.Fatal("restart lost recovery requirement", err)
	}
	plan, err := reloaded.review(context.Background(), "example", "")
	if err != nil || plan.Kind != "recover" || len(plan.Targets) != 1 || len(plan.Services) != 2 {
		t.Fatalf("incorrect recovery scope: %#v, %v", plan, err)
	}
	before := len(runner.calls)
	if _, err = reloaded.startJob(context.Background(), "example", plan.ID, "wrong"); err == nil || len(runner.calls) != before {
		t.Fatal("recovery bypassed confirmation")
	}
	job, err := reloaded.startJob(context.Background(), "example", plan.ID, "example")
	if err != nil {
		t.Fatal(err)
	}
	result := waitJob(t, reloaded, job)
	if result.Status != "succeeded" || runner.intentError != nil {
		t.Fatal(result.Message, runner.intentError)
	}
	for _, call := range runner.calls[before:] {
		if call[0] == "pull" && strings.Join(call, " ") != "pull --policy missing" {
			t.Fatal("recovery unexpectedly refreshed existing image tags", call)
		}
		for _, arg := range call {
			if arg == "down" || arg == "--remove-orphans" || arg == "--renew-anon-volumes" || arg == "--force-recreate" {
				t.Fatal("recovery included an unreviewed destructive action", call)
			}
		}
	}
	definition, err = reloaded.definition("example")
	if err != nil || definition.RecoveryRequired || definition.DeployedAt == "" || definition.AppliedRevision != plan.Revision {
		t.Fatal("recovery did not record success", err)
	}
	source.mu.Lock()
	retained := len(source.data.Containers) == 1 && source.data.Containers[0].ID == strings.Repeat("a", 64)
	source.mu.Unlock()
	if !retained {
		t.Fatal("fixture container was removed or replaced")
	}
	if _, err = reloaded.startJob(context.Background(), "example", plan.ID, "example"); err == nil {
		t.Fatal("recovery review was replayed")
	}
}

func TestPartialDeploymentLifecycleKeepsRecoveryRequired(t *testing.T) {
	l, source, _ := recoveryLibrary(t)
	startReviewed(t, l, "")
	result := runLifecycleFixture(t, l, "stop", strings.Repeat("a", 64))
	if result.Status != "succeeded" {
		t.Fatal(result.Message)
	}
	source.mu.Lock()
	stopped := source.data.Containers[0].State == "exited"
	source.mu.Unlock()
	definition, err := l.definition("example")
	if err != nil || !stopped || !definition.RecoveryRequired || definition.DeployedAt != "" || definition.AppliedRevision != "" {
		t.Fatal("lifecycle incorrectly completed the deployment", err)
	}
	if _, err := l.review(context.Background(), "example", strings.Repeat("a", 64)); err == nil {
		t.Fatal("service-only update was allowed before whole-stack recovery")
	}
}

func TestRecoveryRejectsForeignOneOffAndChangedTargets(t *testing.T) {
	for _, change := range []string{"foreign", "one-off", "replaced-after-review", "attempt-after-review"} {
		t.Run(change, func(t *testing.T) {
			l, source, runner := recoveryLibrary(t)
			startReviewed(t, l, "")
			plan, err := l.review(context.Background(), "example", "")
			if err != nil {
				t.Fatal(err)
			}
			before := len(runner.calls)
			if change == "attempt-after-review" {
				record, _ := l.load("example")
				record.LastDeploymentAttempt.OperationID = randomID()
				if err := l.writeRecord(record); err != nil {
					t.Fatal(err)
				}
			} else {
				source.mu.Lock()
				switch change {
				case "foreign":
					source.data.Containers[0].ComposeDirectory = "/data/compose/4"
				case "one-off":
					source.data.Containers[0].ComposeOneOff = "True"
				case "replaced-after-review":
					source.data.Containers[0].ID = strings.Repeat("b", 64)
				}
				source.mu.Unlock()
			}
			if _, err := l.startJob(context.Background(), "example", plan.ID, "example"); err == nil || len(runner.calls) != before {
				t.Fatal("stale or unsafe recovery was accepted")
			}
			if change == "foreign" || change == "one-off" {
				if _, err := l.review(context.Background(), "example", ""); err == nil {
					t.Fatal("foreign or one-off project became recoverable")
				}
				if _, err := l.reviewAction(context.Background(), "example", "", "stop"); err == nil {
					t.Fatal("unsafe lifecycle review accepted")
				}
			}
		})
	}
}

func TestPullFailureDoesNotAuthorizeDraftOwnership(t *testing.T) {
	l, source, runner := recoveryLibrary(t)
	runner.failPull = true
	result := startReviewed(t, l, "")
	record, err := l.load("example")
	if err != nil || result.Status != "failed" || record.LastDeploymentAttempt != nil || result.ApplyStartedAt != "" {
		t.Fatal("pre-apply failure created deployment ownership", err)
	}
	dir, _ := l.directory("example")
	source.data.Containers = []Container{{ID: strings.Repeat("a", 64), Name: "example-web-1", Stack: "example", Service: "web", State: "running", ComposeDirectory: dir, ComposeFiles: filepath.Join(dir, "compose.yaml")}}
	if _, err := l.review(context.Background(), "example", ""); err == nil {
		t.Fatal("matching paths alone adopted a draft project")
	}
	if _, err := l.reviewAction(context.Background(), "example", "", "stop"); err == nil {
		t.Fatal("matching paths alone authorized lifecycle writes")
	}
}

func TestInterruptedApplyRequiresReviewWithoutAutomaticReplay(t *testing.T) {
	l, source, runner := recoveryLibrary(t)
	failed := startReviewed(t, l, "")
	job, _ := l.job(failed.ID)
	job.Status, job.Stage, job.FinishedAt = "running", "applying", ""
	if err := l.persistJob(job); err != nil {
		t.Fatal(err)
	}
	before := len(runner.calls)
	reloaded := newLibrary(l.root, "", false, source, runner)
	definition, err := reloaded.definition("example")
	if err != nil || !definition.RecoveryRequired || len(runner.calls) != before {
		t.Fatal("restart lost unknown outcome or replayed a write", err)
	}
	persisted, err := reloaded.job(job.ID)
	if err != nil || persisted.Status != "interrupted" || persisted.Revision != failed.Revision {
		t.Fatal("interrupted history lost reviewed revision", err)
	}
	plan, err := reloaded.review(context.Background(), "example", "")
	if err != nil || plan.Kind != "recover" || len(runner.calls) != before {
		t.Fatal("interrupted apply cannot be reviewed safely", err)
	}
}

func TestDamagedStackAndHistoryDoNotHideHealthyResources(t *testing.T) {
	l, source, _ := testLibrary(t)
	savedDraft(t, l)
	for _, name := range []string{"incomplete", "invalid"} {
		dir, _ := l.directory(name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if name == "invalid" {
			if err := os.WriteFile(filepath.Join(dir, "record.json"), []byte("{fixture-sensitive-body"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.Mkdir(filepath.Join(l.root, "stacks", ".stack-orphan"), 0700); err != nil {
		t.Fatal(err)
	}
	records, err := l.list()
	if err != nil || len(records) != 3 {
		t.Fatal("healthy library hidden", err)
	}
	for _, record := range records {
		if record.Name != "example" && !record.Unavailable {
			t.Fatal("damaged record not marked unavailable")
		}
	}
	if _, err := l.review(context.Background(), "invalid", ""); err == nil {
		t.Fatal("damaged stack allowed writes")
	}
	healthy := StackJob{ID: randomID(), Name: "example", Status: "succeeded", StartedAt: "2026-10-16T12:00:00Z"}
	if err := l.persistJob(healthy); err != nil {
		t.Fatal(err)
	}
	brokenPath := filepath.Join(l.root, "jobs", randomID()+".json")
	if err := os.WriteFile(brokenPath, []byte("{fixture-sensitive-body"), 0600); err != nil {
		t.Fatal(err)
	}
	history, err := l.historyDetails("example")
	if err != nil || len(history.Jobs) != 1 || len(history.Warnings) != 1 {
		t.Fatal("damaged job broke healthy history", err)
	}
	if _, err := os.Stat(brokenPath); err != nil {
		t.Fatal("damaged history was deleted")
	}
	handler := newHandler(source, l)
	for _, path := range []string{"/api/inventory", "/api/stacks", "/api/stacks/example/operations", "/api/stacks/example/operations?details=1"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "http://localhost:8787"+path, nil))
		if response.Code != 200 || strings.Contains(response.Body.String(), "fixture-sensitive-body") {
			t.Fatalf("unsafe or unavailable %s: %d", path, response.Code)
		}
		if path == "/api/inventory" {
			var data Inventory
			if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil || len(data.Warnings) != 2 || len(data.Stacks) != 3 {
				t.Fatal("inventory omitted unavailable entries", err)
			}
		}
		if path == "/api/stacks/example/operations" {
			var jobs []StackJob
			if err := json.Unmarshal(response.Body.Bytes(), &jobs); err != nil || len(jobs) != 1 {
				t.Fatal("legacy history response changed", err)
			}
		}
	}
}

func TestStackPublicationStagesCompleteFilesAndPreservesCollisions(t *testing.T) {
	l, _, _ := testLibrary(t)
	inputFiles := map[string]string{".env": "KEY=fixture"}
	if err := l.saveNew(StackRecord{Name: "example", Origin: "new"}, testCompose, inputFiles); err != nil {
		t.Fatal(err)
	}
	if len(inputFiles) != 1 {
		t.Fatal("save mutated caller file map")
	}
	dir, _ := l.directory("example")
	for _, name := range []string{"record.json", "compose.yaml", ".env"} {
		if _, err := readLimited(filepath.Join(dir, name)); err != nil {
			t.Fatal("published bundle incomplete", err)
		}
	}
	if err := l.saveNew(StackRecord{Name: "bad", Origin: "new"}, testCompose, map[string]string{"unsupported": "fixture"}); err == nil {
		t.Fatal("unsupported file accepted")
	}
	bad, _ := l.directory("bad")
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatal("failed save published an incomplete directory")
	}
	reserved, _ := l.directory("reserved")
	if err := os.Mkdir(reserved, 0700); err != nil {
		t.Fatal(err)
	}
	if err := l.saveNew(StackRecord{Name: "reserved", Origin: "new"}, testCompose, nil); err == nil {
		t.Fatal("damaged existing stack was overwritten")
	}
	entries, err := os.ReadDir(filepath.Join(l.root, "stacks"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".stack-") {
			t.Fatal("staged save was not cleaned up")
		}
	}
}

func TestFailedUpdateRetainsSuccessfulRevisionAndRecoveryScope(t *testing.T) {
	l, _, runner := recoveryLibrary(t)
	runner.failUp = false
	success := startReviewed(t, l, "")
	if success.Status != "succeeded" {
		t.Fatal(success.Message)
	}
	before, _ := l.definition("example")
	_, err := l.edit(context.Background(), "example", editInput{Compose: strings.Replace(before.Compose, "fixture/web", "fixture/web:v2", 1), Revision: before.Fingerprint}, true)
	if err != nil {
		t.Fatal(err)
	}
	runner.failUp = true
	failed := startReviewed(t, l, "")
	after, err := l.definition("example")
	if err != nil || failed.Status != "failed" || !after.RecoveryRequired || after.AppliedRevision != before.AppliedRevision || after.DeployedAt != before.DeployedAt || failed.PreviousAppliedRevision != before.AppliedRevision || failed.Revision == before.AppliedRevision {
		t.Fatal("failed update lost the successful deployment baseline", err)
	}
	if _, err := l.review(context.Background(), "example", strings.Repeat("a", 64)); err == nil {
		t.Fatal("service-only update bypassed recovery")
	}
	plan, err := l.review(context.Background(), "example", "")
	if err != nil || plan.Kind != "recover" || plan.Revision != after.Fingerprint || len(plan.Services) != 2 {
		t.Fatal("recovery did not review the selected configuration", err)
	}
}

func TestRecoveryRechecksContainersAfterPull(t *testing.T) {
	l, source, runner := recoveryLibrary(t)
	first := startReviewed(t, l, "")
	before := len(runner.calls)
	runner.afterPull = func() {
		source.mu.Lock()
		source.data.Containers[0].ID = strings.Repeat("b", 64)
		source.mu.Unlock()
	}
	failed := startReviewed(t, l, "")
	if failed.Status != "failed" {
		t.Fatal("changed containers were applied")
	}
	for _, call := range runner.calls[before:] {
		if call[0] == "up" {
			t.Fatal("Compose up ran after a reviewed target changed")
		}
	}
	record, err := l.load("example")
	if err != nil || record.LastDeploymentAttempt.OperationID != first.ID {
		t.Fatal("a pre-apply failure replaced the recorded attempt", err)
	}
}

type publicationFaultSource struct {
	*lifecycleFixture
	snapshots   int
	beforeApply func() error
	faultError  error
}

func (s *publicationFaultSource) ProjectContainers(ctx context.Context, project string) ([]Container, error) {
	data, err := s.lifecycleFixture.ProjectContainers(ctx, project)
	s.snapshots++
	// Review, acceptance, and the worker's post-pull check. The record was read
	// before the third project read, allowing us to fail its subsequent atomic save.
	if s.snapshots == 3 {
		s.faultError = s.beforeApply()
	}
	return data, err
}

func TestAttemptPublicationFailureSendsNoComposeApply(t *testing.T) {
	l, source, runner := recoveryLibrary(t)
	dir, _ := l.directory("example")
	fault := &publicationFaultSource{lifecycleFixture: source, beforeApply: func() error {
		if err := os.Rename(filepath.Join(dir, "record.json"), filepath.Join(dir, "record.saved")); err != nil {
			return err
		}
		return os.Mkdir(filepath.Join(dir, "record.json"), 0700)
	}}
	l.source = fault
	result := startReviewed(t, l, "")
	if fault.faultError != nil {
		t.Fatal(fault.faultError)
	}
	if result.Status != "failed" || result.ApplyStartedAt == "" || !strings.Contains(result.Message, "no Compose apply was attempted") {
		t.Fatal("incorrect attempt-publication failure", result.Message)
	}
	for _, call := range runner.calls {
		if call[0] == "up" {
			t.Fatal("Compose apply ran without a saved attempt")
		}
	}
	if err := os.Remove(filepath.Join(dir, "record.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "record.saved"), filepath.Join(dir, "record.json")); err != nil {
		t.Fatal(err)
	}
	record, err := l.load("example")
	if err != nil || record.LastDeploymentAttempt != nil {
		t.Fatal("failed publication changed the previous record", err)
	}
}

func TestLegacySuccessfulStackWithoutAttemptRemainsManageable(t *testing.T) {
	l, source, runner := recoveryLibrary(t)
	runner.failUp = false
	startReviewed(t, l, "")
	record, _ := l.load("example")
	record.LastDeploymentAttempt = nil
	if err := l.writeRecord(record); err != nil {
		t.Fatal(err)
	}
	reloaded := newLibrary(l.root, "", false, source, runner)
	plan, err := reloaded.review(context.Background(), "example", "")
	if err != nil || plan.Kind != "update" {
		t.Fatal("legacy successful stack became unavailable", err)
	}
	if _, err := reloaded.reviewAction(context.Background(), "example", "", "stop"); err != nil {
		t.Fatal("legacy lifecycle controls rejected", err)
	}
}

func TestMissingAttemptResultRequiresRecoveryAndInvalidMarkerBlocksWrites(t *testing.T) {
	l, source, runner := recoveryLibrary(t)
	failed := startReviewed(t, l, "")
	if err := os.Remove(filepath.Join(l.root, "jobs", failed.ID+".json")); err != nil {
		t.Fatal(err)
	}
	reloaded := newLibrary(l.root, "", false, source, runner)
	definition, err := reloaded.definition("example")
	if err != nil || !definition.RecoveryRequired {
		t.Fatal("missing result hid recovery requirement", err)
	}
	record, _ := l.load("example")
	record.LastDeploymentAttempt.OperationID = "../invalid"
	if err := l.writeRecord(record); err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.review(context.Background(), "example", ""); err == nil {
		t.Fatal("invalid attempt authorized recovery")
	}
	if _, err := reloaded.reviewAction(context.Background(), "example", "", "stop"); err == nil {
		t.Fatal("invalid attempt authorized lifecycle writes")
	}
	records, err := reloaded.list()
	if err != nil || len(records) != 1 || !records[0].Unavailable {
		t.Fatal("invalid metadata broke listing or was treated as usable", err)
	}
}
