package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func savedDraft(t *testing.T, l *stackLibrary) StackDefinition {
	t.Helper()
	_, err := l.create(context.Background(), stackInput{Name: "example", Compose: testCompose, Environment: "KEY=fixture-file-value\n"})
	if err != nil {
		t.Fatal(err)
	}
	definition, err := l.definition("example")
	if err != nil {
		t.Fatal(err)
	}
	return definition
}
func TestEditKeepsHiddenValuesAndPreviousRevision(t *testing.T) {
	l, _, runner := testLibrary(t)
	draft := savedDraft(t, l)
	input := editInput{Compose: strings.Replace(draft.Compose, "nginx:alpine", "nginx:stable", 1), Revision: draft.Fingerprint}
	result, err := l.edit(context.Background(), "example", input, true)
	if err != nil {
		t.Fatal(err)
	}
	files, err := l.content(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(files["compose.yaml"], "fixture-not-a-real-secret") || !strings.Contains(files[".env"], "fixture-file-value") || strings.Contains(files["compose.yaml"], "[hidden]") {
		t.Fatal("saved secrets were replaced by masked values")
	}
	original, _ := l.directory("example")
	old, _ := os.ReadFile(filepath.Join(original, "compose.yaml"))
	if string(old) != testCompose {
		t.Fatal("previous revision was overwritten")
	}
	if result.ConfigRevision == "" || result.AppliedRevision != "" {
		t.Fatal("saving edit deployed it")
	}
	for _, call := range runner.calls {
		if call[2] != "config" {
			t.Fatal("edit ran a container operation")
		}
	}
	if _, err = l.edit(context.Background(), "example", input, true); err == nil {
		t.Fatal("stale editor overwrote a newer revision")
	}
	current, _ := l.definition("example")
	_, err = l.edit(context.Background(), "example", editInput{Compose: current.Compose, Revision: current.Fingerprint, ReplaceEnvironment: true, Environment: "NEW=fixture-new\n"}, true)
	if err != nil {
		t.Fatal(err)
	}
	record, _ := l.load("example")
	files, _ = l.content(record)
	if files[".env"] != "NEW=fixture-new\n" || files["stack.env"] != files[".env"] {
		t.Fatal("explicit environment replacement did not apply")
	}
	encoded, _ := json.Marshal(current)
	if strings.Contains(string(encoded), "fixture-file-value") || strings.Contains(string(encoded), "fixture-not-a-real-secret") {
		t.Fatal("editor leaked a value")
	}
}
func TestHiddenListMergeAndValidationOnly(t *testing.T) {
	original := "services:\n web:\n  image: nginx\n  environment: [FIRST=fixture-one, SECOND=fixture-two]\n"
	incoming := "services:\n web:\n  image: nginx\n  environment: [SECOND=[hidden], FIRST=[hidden]]\n"
	// Quote placeholders because brackets have special meaning in flow YAML.
	incoming = strings.ReplaceAll(incoming, "SECOND=[hidden]", "\"SECOND=[hidden]\"")
	incoming = strings.ReplaceAll(incoming, "FIRST=[hidden]", "\"FIRST=[hidden]\"")
	merged, err := restoreHidden(incoming, original)
	if err != nil || !strings.Contains(merged, "SECOND=fixture-two") || !strings.Contains(merged, "FIRST=fixture-one") {
		t.Fatalf("list values not preserved by key: %v", err)
	}
	l, _, _ := testLibrary(t)
	draft := savedDraft(t, l)
	_, err = l.edit(context.Background(), "example", editInput{Compose: strings.Replace(draft.Compose, "nginx:alpine", "nginx:stable", 1), Revision: draft.Fingerprint}, false)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := l.definition("example")
	if after.Fingerprint != draft.Fingerprint {
		t.Fatal("validation saved a change")
	}
	if _, err = restoreHidden(strings.Replace(draft.Compose, "PASSWORD:", "UNKNOWN:", 1), testCompose); err == nil {
		t.Fatal("unknown hidden field accepted")
	}
}
func ownedContainers(t *testing.T, l *stackLibrary, source *fixtureSource) {
	t.Helper()
	record, _ := l.load("example")
	dir, _ := l.contentDirectory(record)
	source.data.Containers = []Container{
		{ID: "web-id", Name: "example-web-1", Stack: "example", Service: "web", State: "running", ComposeDirectory: dir, ComposeFiles: filepath.Join(dir, "compose.yaml")},
	}
	source.data.Stacks = groupStacks(source.data.Containers)
}
func startReviewed(t *testing.T, l *stackLibrary, containerID string) StackJob {
	t.Helper()
	plan, err := l.review(context.Background(), "example", containerID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := l.startJob(context.Background(), "example", plan.ID, "example")
	if err != nil {
		t.Fatal(err)
	}
	return waitJob(t, l, job)
}
func TestUpdatePullsBeforeApplyingAndScopesService(t *testing.T) {
	l, source, runner := testLibrary(t)
	savedDraft(t, l)
	if job := startReviewed(t, l, ""); job.Status != "succeeded" {
		t.Fatal(job.Message)
	}
	ownedContainers(t, l, source)
	before := len(runner.calls)
	job := startReviewed(t, l, "web-id")
	if job.Status != "succeeded" {
		t.Fatal(job.Message)
	}
	calls := runner.calls[before:]
	if len(calls) != 3 || strings.Join(calls[1][2:], " ") != "pull --policy always web" || !strings.Contains(strings.Join(calls[2][2:], " "), "--no-deps web") {
		t.Fatalf("incorrect update scope: %v", calls)
	}
	for _, call := range calls {
		for _, arg := range call[2:] {
			if arg == "down" || arg == "--remove-orphans" || arg == "--force-recreate" || arg == "--renew-anon-volumes" {
				t.Fatal("unexpected destructive update flag")
			}
		}
	}
	history, err := l.history("example")
	if err != nil || len(history) != 2 {
		t.Fatal("operation history not saved")
	}
	reloaded := newLibrary(l.root, "", false, source, runner)
	persisted, err := reloaded.job(job.ID)
	if err != nil || persisted.Status != "succeeded" {
		t.Fatal("completed operation lost across restart")
	}
}
func TestUpdatesRejectStaleReviewsAndForeignContainers(t *testing.T) {
	l, source, _ := testLibrary(t)
	draft := savedDraft(t, l)
	plan, err := l.review(context.Background(), "example", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = l.edit(context.Background(), "example", editInput{Compose: strings.Replace(draft.Compose, "nginx:alpine", "nginx:stable", 1), Revision: draft.Fingerprint}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = l.startJob(context.Background(), "example", plan.ID, "example"); err == nil {
		t.Fatal("stale definition review accepted")
	}
	if job := startReviewed(t, l, ""); job.Status != "succeeded" {
		t.Fatal(job.Message)
	}
	ownedContainers(t, l, source)
	plan, err = l.review(context.Background(), "example", "web-id")
	if err != nil {
		t.Fatal(err)
	}
	source.data.Containers[0].ID = "replacement-id"
	if _, err = l.startJob(context.Background(), "example", plan.ID, "example"); err == nil {
		t.Fatal("stale container review accepted")
	}
	source.data.Containers[0].ComposeDirectory = "/data/compose/4"
	if _, err = l.review(context.Background(), "example", ""); err == nil {
		t.Fatal("Portainer-owned container targeted")
	}
}
func TestPullAndApplyFailuresHaveAccurateResults(t *testing.T) {
	l, source, runner := testLibrary(t)
	savedDraft(t, l)
	if job := startReviewed(t, l, ""); job.Status != "succeeded" {
		t.Fatal(job.Message)
	}
	ownedContainers(t, l, source)
	before := len(runner.calls)
	runner.failPull = true
	job := startReviewed(t, l, "")
	if job.Status != "failed" || !strings.Contains(job.Message, "not recreated") {
		t.Fatal("pull failure reported inaccurately")
	}
	for _, call := range runner.calls[before:] {
		if call[2] == "up" {
			t.Fatal("containers recreated after failed pull")
		}
	}
	runner.failPull = false
	runner.failUp = true
	job = startReviewed(t, l, "")
	if job.Status != "failed" || !strings.Contains(job.Message, "may have changed") {
		t.Fatal("apply failure hid partial resources")
	}
}
func TestEditedDefinitionRequiresWholeStackUpdate(t *testing.T) {
	l, source, _ := testLibrary(t)
	savedDraft(t, l)
	startReviewed(t, l, "")
	ownedContainers(t, l, source)
	draft, _ := l.definition("example")
	_, err := l.edit(context.Background(), "example", editInput{Compose: strings.Replace(draft.Compose, "nginx:alpine", "nginx:stable", 1), Revision: draft.Fingerprint}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = l.review(context.Background(), "example", "web-id"); err == nil {
		t.Fatal("service update applied only part of edited configuration")
	}
	if job := startReviewed(t, l, ""); job.Status != "succeeded" {
		t.Fatal(job.Message)
	}
	ownedContainers(t, l, source)
	if _, err = l.review(context.Background(), "example", "web-id"); err != nil {
		t.Fatal(err)
	}
}

type gatedRunner struct {
	delegate         *fixtureRunner
	entered, release chan struct{}
}

func (g *gatedRunner) Run(ctx context.Context, name, dir string, args ...string) ([]byte, error) {
	if args[0] == "pull" {
		close(g.entered)
		select {
		case <-g.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return g.delegate.Run(ctx, name, dir, args...)
}
func TestActiveOperationCannotBeReplayedOrEdited(t *testing.T) {
	l, source, runner := testLibrary(t)
	draft := savedDraft(t, l)
	gate := &gatedRunner{runner, make(chan struct{}), make(chan struct{})}
	l.runner = gate
	plan, err := l.review(context.Background(), "example", "")
	if err != nil {
		t.Fatal(err)
	}
	job, err := l.startJob(context.Background(), "example", plan.ID, "example")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("pull did not start")
	}
	if _, err = l.startJob(context.Background(), "example", plan.ID, "example"); err == nil {
		t.Error("operation was replayed")
	}
	if _, err = l.edit(context.Background(), "example", editInput{Compose: draft.Compose, Revision: draft.Fingerprint}, true); err == nil {
		t.Error("active configuration was changed")
	}
	// Changing targets during image download must stop before up.
	source.data.Containers = []Container{{ID: "foreign", Stack: "example"}}
	close(gate.release)
	result := waitJob(t, l, job)
	if result.Status != "failed" {
		t.Fatal("changed project was deployed")
	}
	for _, call := range runner.calls {
		if call[2] == "up" {
			t.Fatal("up ran after target change")
		}
	}
}
func TestReviewIgnoresUptimeButDetectsInterruptedJobs(t *testing.T) {
	l, source, runner := testLibrary(t)
	savedDraft(t, l)
	startReviewed(t, l, "")
	ownedContainers(t, l, source)
	plan, err := l.review(context.Background(), "example", "web-id")
	if err != nil {
		t.Fatal(err)
	}
	source.data.Containers[0].Status = "Up another minute"
	job, err := l.startJob(context.Background(), "example", plan.ID, "example")
	if err != nil {
		t.Fatal("uptime invalidated a valid review", err)
	}
	if waitJob(t, l, job).Status != "succeeded" {
		t.Fatal("update failed")
	}
	interrupted := StackJob{ID: randomID(), Name: "example", Status: "running", Stage: "applying"}
	if err = l.persistJob(interrupted); err != nil {
		t.Fatal(err)
	}
	reloaded := newLibrary(l.root, "", false, source, runner)
	result, err := reloaded.job(interrupted.ID)
	if err != nil || result.Status != "interrupted" {
		t.Fatal("unknown outcome reported as running/succeeded")
	}
}
func TestEditAndOperationRoutes(t *testing.T) {
	l, source, _ := testLibrary(t)
	draft := savedDraft(t, l)
	handler := newHandler(source, l)
	request := httptest.NewRequest("GET", "http://localhost:8787/api/session", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	json.Unmarshal(response.Body.Bytes(), &session)
	post := func(path string, input any, token string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(input)
		req := httptest.NewRequest("POST", "http://localhost:8787"+path, strings.NewReader(string(body)))
		req.Header.Set("Origin", "http://localhost:8787")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Dockspire-CSRF", token)
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, req)
		return r
	}
	response = post("/api/stacks/example/validate-edit", editInput{Compose: draft.Compose, Revision: draft.Fingerprint}, session.CSRFToken)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	response = post("/api/stacks/example/review", map[string]string{}, session.CSRFToken)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var plan UpdatePlan
	json.Unmarshal(response.Body.Bytes(), &plan)
	input := map[string]string{"planId": plan.ID, "confirmation": "example"}
	if response = post("/api/stacks/example/operations", input, "wrong"); response.Code != 403 {
		t.Fatal("operation accepted without token")
	}
	response = post("/api/stacks/example/operations", input, session.CSRFToken)
	if response.Code != 202 {
		t.Fatal(response.Body.String())
	}
	var job StackJob
	json.Unmarshal(response.Body.Bytes(), &job)
	if result := waitJob(t, l, job); result.Status != "succeeded" {
		t.Fatal(result.Message)
	}
}
