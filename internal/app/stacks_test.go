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
	"time"
)

const testCompose = "services:\n  web:\n    image: nginx:alpine\n    environment:\n      PASSWORD: fixture-not-a-real-secret\n"

type fixtureSource struct{ data Inventory }

func (f *fixtureSource) Snapshot(context.Context) (Inventory, error) { return f.data, nil }
func (f *fixtureSource) ProjectContainers(_ context.Context, project string) ([]Container, error) {
	return filterProjectContainers(f.data.Containers, project), nil
}

type fixtureRunner struct {
	calls    [][]string
	failUp   bool
	failPull bool
}

func (f *fixtureRunner) Run(_ context.Context, name, directory string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name, directory}, args...))
	if len(args) > 0 && args[0] == "pull" && f.failPull {
		return nil, errors.New("fixture pull failure")
	}
	if len(args) > 0 && args[0] == "up" && f.failUp {
		return nil, errors.New("fixture failure")
	}
	return nil, nil
}
func waitJob(t *testing.T, library *stackLibrary, job StackJob) StackJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, err := library.job(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status != "running" {
			// Wait for the reservation to be released as well as the terminal result.
			if !library.isBusy(job.Name) {
				return current
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("operation did not complete")
	return StackJob{}
}
func deployFixture(t *testing.T, library *stackLibrary, name, confirmation string) (StackRecord, error) {
	t.Helper()
	plan, err := library.review(context.Background(), name, "")
	if err != nil {
		return StackRecord{}, err
	}
	job, err := library.startJob(context.Background(), name, plan.ID, confirmation)
	if err != nil {
		return StackRecord{}, err
	}
	result := waitJob(t, library, job)
	if result.Status != "succeeded" {
		return StackRecord{}, errors.New(result.Message)
	}
	return library.load(name)
}
func testLibrary(t *testing.T) (*stackLibrary, *fixtureSource, *fixtureRunner) {
	t.Helper()
	source := &fixtureSource{data: Inventory{Mode: "live", Stacks: []Stack{}, Containers: []Container{}}}
	runner := &fixtureRunner{}
	return newLibrary(t.TempDir(), "", false, source, runner), source, runner
}
func TestComposeInputAndRedaction(t *testing.T) {
	services, redacted, _, err := inspectCompose(testCompose, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 || services[0].Name != "web" || strings.Contains(redacted, "fixture-not-a-real-secret") || !strings.Contains(redacted, "[hidden]") {
		t.Fatalf("unsafe preview: %s", redacted)
	}
	for _, raw := range []string{"broken: [", "services: {}", "services:\n web:\n  image: nginx\n  volumes: [./data:/data]\n", "services:\n web:\n  image: nginx\n  env_file: /etc/passwd\n", "services:\n web:\n  image: nginx\n  build: .\n", testCompose + "---\nservices: {}\n"} {
		if _, _, _, err := inspectCompose(raw, true); err == nil {
			t.Fatalf("unsafe definition accepted: %s", raw)
		}
	}
	_, redacted, _, err = inspectCompose("services:\n web:\n  image: nginx\n  environment: [TOKEN=fixture-token, INHERITED]\n", false)
	if err != nil || strings.Contains(redacted, "fixture-token") || !strings.Contains(redacted, "TOKEN=[hidden]") {
		t.Fatalf("array environment was not hidden: %s, %v", redacted, err)
	}
}
func TestCreateDraftDoesNotDeployOrExposeEnvironment(t *testing.T) {
	library, _, runner := testLibrary(t)
	record, err := library.create(context.Background(), stackInput{Name: "example", Compose: testCompose, Environment: "DB_PASSWORD=fixture-env-secret\n"})
	if err != nil {
		t.Fatal(err)
	}
	if record.DeployedAt != "" || len(runner.calls) != 1 || runner.calls[0][2] != "config" {
		t.Fatalf("saving unexpectedly deployed: %+v %v", record, runner.calls)
	}
	definition, err := library.definition("example")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(definition)
	if strings.Contains(string(encoded), "fixture-env-secret") || strings.Contains(string(encoded), "fixture-not-a-real-secret") {
		t.Fatal("saved preview exposed an environment value")
	}
	dir, _ := library.directory("example")
	raw, _ := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if string(raw) != testCompose {
		t.Fatal("stored source changed")
	}
	if _, err = library.create(context.Background(), stackInput{Name: "example", Compose: testCompose}); err == nil {
		t.Fatal("duplicate draft replaced stored data")
	}
	if _, err = library.definition("../example"); err == nil {
		t.Fatal("path traversal accepted")
	}
}
func TestDeploymentConfirmationAndCollision(t *testing.T) {
	library, source, runner := testLibrary(t)
	_, err := library.create(context.Background(), stackInput{Name: "example", Compose: testCompose})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = deployFixture(t, library, "example", "wrong"); err == nil {
		t.Fatal("missing confirmation accepted")
	}
	source.data.Stacks = []Stack{{Name: "example"}}
	source.data.Containers = []Container{{ID: "collision", Stack: "example"}}
	if _, err = deployFixture(t, library, "example", "example"); err == nil {
		t.Fatal("existing project was targeted")
	}
	if len(runner.calls) != 1 {
		t.Fatal("Compose ran despite collision")
	}
	source.data.Stacks = nil
	source.data.Containers = nil
	record, err := deployFixture(t, library, "example", "example")
	if err != nil {
		t.Fatal(err)
	}
	if record.DeployedAt == "" || runner.calls[len(runner.calls)-1][2] != "up" {
		t.Fatal("deployment not recorded")
	}
	for _, arg := range runner.calls[len(runner.calls)-1] {
		if arg == "--remove-orphans" || arg == "--force-recreate" {
			t.Fatal("destructive deployment option enabled")
		}
	}
	if plan, err := library.review(context.Background(), "example", ""); err != nil || plan.Kind != "update" {
		t.Fatal("deployed stack should offer an explicit update review")
	}
}
func importFixture(t *testing.T) (*stackLibrary, *fixtureRunner, string) {
	t.Helper()
	library, source, runner := testLibrary(t)
	library.allowImport = true
	library.includeEnvironment = true
	library.portainerRoot = t.TempDir()
	dir := filepath.Join(library.portainerRoot, "4")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(testCompose), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stack.env"), []byte("KEY=fixture-file-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source.data.Containers = []Container{{Name: "web", Stack: "existing", ComposeFiles: "/data/compose/4/docker-compose.yml", ComposeDirectory: "/data/compose/4"}}
	return library, runner, dir
}
func TestImportCopiesWithoutRunningCompose(t *testing.T) {
	library, runner, dir := importFixture(t)
	preview, _, err := library.importPreview(context.Background(), "existing")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(preview)
	if strings.Contains(string(encoded), "fixture-file-secret") || strings.Contains(string(encoded), "fixture-not-a-real-secret") {
		t.Fatal("import preview leaked a value")
	}
	if _, err = library.importStack(context.Background(), "existing", preview.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 0 {
		t.Fatal("import changed Docker resources")
	}
	stored, _ := library.directory("existing")
	raw, _ := os.ReadFile(filepath.Join(stored, "stack.env"))
	if string(raw) != "KEY=fixture-file-secret\n" {
		t.Fatal("environment file not preserved")
	}
	original, _ := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if string(original) != testCompose {
		t.Fatal("Portainer source modified")
	}
	if _, err = deployFixture(t, library, "existing", "existing"); err == nil {
		t.Fatal("imported project deployment allowed")
	}
}
func TestImportSourceChangeAndAuthorization(t *testing.T) {
	library, _, dir := importFixture(t)
	preview, _, err := library.importPreview(context.Background(), "existing")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "stack.env"), []byte("KEY=changed-fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = library.importStack(context.Background(), "existing", preview.Fingerprint); err == nil {
		t.Fatal("changed environment file was silently imported")
	}
	library.allowImport = false
	if _, _, err = library.importPreview(context.Background(), "existing"); err == nil {
		t.Fatal("source read without operator authorization")
	}
}

func TestEnvironmentImportCanBeExcluded(t *testing.T) {
	library, _, _ := importFixture(t)
	library.includeEnvironment = false
	preview, files, err := library.importPreview(context.Background(), "existing")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := files["stack.env"]; exists || len(preview.EnvironmentNames) != 0 {
		t.Fatal("environment file was included without authorization")
	}
}

func TestFailedDeploymentIsNotRecordedAsSuccess(t *testing.T) {
	library, _, runner := testLibrary(t)
	if _, err := library.create(context.Background(), stackInput{Name: "example", Compose: testCompose}); err != nil {
		t.Fatal(err)
	}
	runner.failUp = true
	if _, err := deployFixture(t, library, "example", "example"); err == nil {
		t.Fatal("failed deployment reported success")
	}
	record, err := library.load("example")
	if err != nil {
		t.Fatal(err)
	}
	if record.DeployedAt != "" {
		t.Fatal("failed deployment persisted success")
	}
}
func TestStackMutationsRequireSameOriginToken(t *testing.T) {
	library, source, _ := testLibrary(t)
	handler := newHandler(source, library)
	get := httptest.NewRequest("GET", "/api/session", nil)
	get.Host = "localhost:8787"
	sessionResponse := httptest.NewRecorder()
	handler.ServeHTTP(sessionResponse, get)
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(sessionResponse.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(stackInput{Name: "example", Compose: testCompose})
	for _, test := range []struct {
		origin, token, content string
		want                   int
	}{{"http://attacker.example", session.CSRFToken, "application/json", 403}, {"http://localhost:8787", "wrong", "application/json", 403}, {"http://localhost:8787", session.CSRFToken, "text/plain", 403}, {"http://localhost:8787", session.CSRFToken, "application/json", 200}} {
		req := httptest.NewRequest("POST", "/api/stacks/validate", strings.NewReader(string(body)))
		req.Host = "localhost:8787"
		req.Header.Set("Origin", test.origin)
		req.Header.Set("X-Stacklane-CSRF", test.token)
		req.Header.Set("Content-Type", test.content)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != test.want {
			t.Fatalf("got %d, want %d: %s", response.Code, test.want, response.Body.String())
		}
	}
}
