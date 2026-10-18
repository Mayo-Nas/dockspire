package app

import (
	"context"
	"encoding/json"
	"errors"
	"go.yaml.in/yaml/v3"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const takeoverCompose = "services:\n  web:\n    image: fixture/app:stable\n    environment:\n      VALUE: 'fictional$$literal'\n    volumes:\n      - data:/data\nvolumes:\n  data: {}\n"

type takeoverFixtureSource struct {
	fixtureSource
	details map[string]TakeoverContainer
}

func (s *takeoverFixtureSource) TakeoverContainer(_ context.Context, id string) (TakeoverContainer, error) {
	c, ok := s.details[id]
	if !ok {
		return c, errors.New("fixture unavailable")
	}
	return c, nil
}
func (s *takeoverFixtureSource) ControlContainer(_ context.Context, id, action string) error {
	for i, c := range s.data.Containers {
		if c.ID == id {
			if action == "stop" {
				s.data.Containers[i].State = "exited"
			} else {
				s.data.Containers[i].State = "running"
			}
			return nil
		}
	}
	return errors.New("fixture missing")
}
func (s *takeoverFixtureSource) ContainerState(_ context.Context, id string) (string, error) {
	for _, c := range s.data.Containers {
		if c.ID == id {
			return c.State, nil
		}
	}
	return "", errors.New("fixture missing")
}

type takeoverFixtureRunner struct {
	library    *stackLibrary
	source     *takeoverFixtureSource
	canonical  []byte
	hash       string
	base       Container
	metadata   TakeoverContainer
	failAfter  bool
	afterUp    func() error
	ups, pulls int
}

func (r *takeoverFixtureRunner) Run(_ context.Context, name, dir string, args ...string) ([]byte, error) {
	if args[0] == "config" {
		if len(args) > 1 && args[1] == "--hash" {
			return []byte("web " + r.hash + "\n"), nil
		}
		return r.canonical, nil
	}
	if args[0] == "pull" {
		r.pulls++
		return nil, nil
	}
	if args[0] != "up" {
		return nil, errors.New("unexpected fixture operation")
	}
	r.ups++
	record, err := r.library.load(name)
	if err != nil || record.LastDeploymentAttempt == nil {
		return nil, errors.New("missing durable apply intent")
	}
	item := r.base
	item.ID = strings.Repeat("e", 64)
	item.Image = r.metadata.Image
	item.ComposeDirectory = dir
	item.ComposeFiles = filepath.Join(dir, "compose.yaml")
	r.source.data.Containers = []Container{item}
	metadata := r.metadata
	metadata.ID = item.ID
	r.source.details[item.ID] = metadata
	if r.afterUp != nil {
		if err := r.afterUp(); err != nil {
			return nil, err
		}
	}
	if r.failAfter {
		return nil, errors.New("fictional failure after recreation")
	}
	return nil, nil
}

func takeoverLibrary(t *testing.T) (*stackLibrary, *takeoverFixtureSource, *takeoverFixtureRunner) {
	t.Helper()
	id := strings.Repeat("a", 64)
	image := "sha256:" + strings.Repeat("b", 64)
	item := Container{ID: id, Name: "example_web_1", Service: "web", Stack: "example", Image: "fixture/app:stable", ImageID: image, State: "running", ComposeFiles: "/data/compose/7/docker-compose.yml", ComposeDirectory: "/data/compose/7"}
	metadata := TakeoverContainer{ID: id, Name: item.Name, Service: "web", Image: image, ConfigHash: strings.Repeat("c", 64), Mounts: []TakeoverMount{{Type: "volume", Source: "example_data", Target: "/data"}}, Networks: map[string]string{"example_default": strings.Repeat("d", 64)}, Ports: "{}", Hostname: "fictional-service"}
	source := &takeoverFixtureSource{fixtureSource: fixtureSource{data: Inventory{Containers: []Container{item}}}, details: map[string]TakeoverContainer{id: metadata}}
	canonical := []byte(`{"name":"example","services":{"web":{"image":"fixture/app:stable","environment":{"VALUE":"fictional$$literal"},"volumes":[{"type":"volume","source":"data","target":"/data","volume":{}}],"networks":{"default":null}}},"volumes":{"data":{"name":"example_data"}},"networks":{"default":{"name":"example_default"}}}`)
	runner := &takeoverFixtureRunner{source: source, canonical: canonical, hash: metadata.ConfigHash, base: item, metadata: metadata}
	l := newLibrary(t.TempDir(), t.TempDir(), true, source, runner)
	runner.library = l
	l.takeoverHostname = func() (string, error) { return "fictional-manager", nil }
	dir := filepath.Join(l.portainerRoot, "7")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(takeoverCompose), 0600); err != nil {
		t.Fatal(err)
	}
	preview, _, err := l.importPreview(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = l.importStack(context.Background(), "example", preview.Fingerprint); err != nil {
		t.Fatal(err)
	}
	return l, source, runner
}

func runFixtureTakeover(t *testing.T, l *stackLibrary) StackJob {
	t.Helper()
	plan, err := l.reviewAction(context.Background(), "example", "", "takeover")
	if err != nil {
		t.Fatal(err)
	}
	job, err := l.startJob(context.Background(), "example", plan.ID, "example", true)
	if err != nil {
		t.Fatal(err)
	}
	return waitJob(t, l, job)
}

func TestTakeoverExplicitConfirmationAndManagedWorkflows(t *testing.T) {
	l, source, runner := takeoverLibrary(t)
	ctx := context.Background()
	before, _ := l.load("example")
	plan, err := l.reviewAction(ctx, "example", "", "takeover")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(plan)
	if strings.Contains(string(raw), "fictional$") || strings.Contains(string(raw), "sourceFile") || l.plans[plan.ID].takeover != nil || l.plans[plan.ID].takeoverFiles != nil {
		t.Fatal("review exposed or cached private snapshot")
	}
	for _, ack := range [][]bool{nil, {false}} {
		if _, err = l.startJob(ctx, "example", plan.ID, "example", ack...); err == nil {
			t.Fatal("missing writer acknowledgement accepted")
		}
	}
	if _, err = l.startJob(ctx, "example", plan.ID, "wrong", true); err == nil {
		t.Fatal("wrong project confirmation accepted")
	}
	after, _ := l.load("example")
	if recordStamp(before) != recordStamp(after) || runner.ups != 0 {
		t.Fatal("review/failed confirmation changed ownership")
	}
	job, err := l.startJob(ctx, "example", plan.ID, "example", true)
	if err != nil {
		t.Fatal(err)
	}
	if result := waitJob(t, l, job); result.Status != "succeeded" {
		t.Fatal(result.Message)
	}
	record, _ := l.load("example")
	if !record.managed() || record.Origin != "portainer" || record.Takeover.State != "managed" || source.data.Containers[0].Name != "example_web_1" {
		t.Fatal("verified takeover did not preserve identity/provenance")
	}
	original, _ := l.content(StackRecord{Name: "example"})
	if original["compose.yaml"] != takeoverCompose {
		t.Fatal("original import overwritten")
	}
	files, _ := l.content(record)
	if !strings.Contains(files["compose.yaml"], "fictional$$literal") || !strings.Contains(files["compose.yaml"], "external: true") {
		t.Fatal("snapshot lost dollars or named resources")
	}
	for _, value := range []any{record, []StackRecord{record}, StackDefinition{StackRecord: record, Compose: "redacted"}} {
		encoded, _ := json.Marshal(publicStackValue(value))
		if strings.Contains(string(encoded), "configHash") || strings.Contains(string(encoded), "sourceFingerprint") || strings.Contains(string(encoded), `"takeover":`) {
			t.Fatal("public record leaked takeover internals")
		}
		if !strings.Contains(string(encoded), `"managed":true`) {
			t.Fatal("public authority state absent")
		}
	}
	if _, err = deployFixture(t, l, "example", "example"); err != nil {
		t.Fatal("ordinary pinned redeploy failed:", err)
	}
	if runner.pulls != 0 {
		t.Fatal("local image IDs were pulled")
	}
	lifecycle, err := l.reviewAction(ctx, "example", "", "restart")
	if err != nil {
		t.Fatal(err)
	}
	control, err := l.startJob(ctx, "example", lifecycle.ID, "example")
	if err != nil {
		t.Fatal(err)
	}
	if waitJob(t, l, control).Status != "succeeded" {
		t.Fatal("managed lifecycle failed")
	}
	definition, _ := l.definition("example")
	edited, err := l.edit(ctx, "example", editInput{Revision: definition.Fingerprint, Compose: strings.Replace(definition.Compose, runner.metadata.Image, "fixture/app:next", 1)}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !edited.managed() {
		t.Fatal("editing lost managed provenance")
	}
	if _, err = deployFixture(t, l, "example", "example"); err != nil {
		t.Fatal(err)
	}
	if runner.pulls != 1 {
		t.Fatal("explicit tag edit did not resume registry pulls")
	}
	preview, err := l.revisionPreview("example", record.Takeover.Revision)
	if err != nil || !preview.CanRestore {
		t.Fatal("managed revision restore unavailable", err)
	}
	restored, err := l.restoreRevision(ctx, "example", record.Takeover.Revision, preview.RecordVersion, "example")
	if err != nil || !restored.managed() {
		t.Fatal("restoring draft lost authority", err)
	}
	cleanup, err := l.reviewRevisionCleanup(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	for _, rev := range cleanup.Remove {
		if rev == record.Takeover.Revision {
			t.Fatal("takeover recovery revision eligible for cleanup")
		}
	}
}

func TestTakeoverInterruptedRecoveryAndForeignWriters(t *testing.T) {
	for _, mode := range []string{"all-recreated", "temporarily-missing", "foreign-replacement"} {
		t.Run(mode, func(t *testing.T) {
			l, source, runner := takeoverLibrary(t)
			runner.failAfter = true
			if result := runFixtureTakeover(t, l); result.Status != "failed" {
				t.Fatal("partial failure reported success")
			}
			pending, _ := l.load("example")
			if pending.managed() || !l.needsRecovery(pending) {
				t.Fatal("partial takeover enabled authority")
			}
			if _, err := l.edit(context.Background(), "example", editInput{}, true); err == nil {
				t.Fatal("pending takeover enabled editing")
			}
			if mode == "temporarily-missing" {
				source.data.Containers = nil
			}
			if mode == "foreign-replacement" {
				item := runner.base
				item.ID = strings.Repeat("f", 64)
				source.data.Containers = []Container{item}
				c := runner.metadata
				c.ID = item.ID
				source.details[item.ID] = c
			}
			reloaded := newLibrary(l.root, l.portainerRoot, true, source, runner)
			reloaded.takeoverHostname = l.takeoverHostname
			runner.library = reloaded
			runner.failAfter = false
			if mode == "foreign-replacement" {
				if _, err := reloaded.reviewAction(context.Background(), "example", "", "takeover"); err == nil {
					t.Fatal("replacement Portainer ID authorized")
				}
				return
			}
			review, err := reloaded.reviewAction(context.Background(), "example", "", "takeover")
			if err != nil || len(review.Targets) != 1 || review.Targets[0] != "example_web_1" {
				t.Fatal("recovery confirmation omitted a captured service", err)
			}
			if result := runFixtureTakeover(t, reloaded); result.Status != "succeeded" {
				t.Fatal("pending recovery after lost source labels failed:", result.Message)
			}
		})
	}
}

func TestTakeoverReviewRejectsChangedOrUnsafeState(t *testing.T) {
	for _, mode := range []string{"hash", "mount", "network", "self", "large", "stale"} {
		t.Run(mode, func(t *testing.T) {
			l, source, runner := takeoverLibrary(t)
			ctx := context.Background()
			var plan UpdatePlan
			var err error
			if mode == "stale" {
				plan, err = l.reviewAction(ctx, "example", "", "takeover")
				if err != nil {
					t.Fatal(err)
				}
			}
			id := source.data.Containers[0].ID
			c := source.details[id]
			switch mode {
			case "hash":
				runner.hash = strings.Repeat("0", 64)
			case "mount":
				c.Mounts = []TakeoverMount{{Type: "volume", Source: "other_data", Target: "/data"}}
			case "network":
				c.Networks = map[string]string{"other_default": strings.Repeat("d", 64)}
			case "self":
				c.Hostname = "fictional-manager"
			case "large":
				c.Ports = `{"x":"` + strings.Repeat("x", maxDefinition) + `"}`
			case "stale":
				source.data.Containers[0].State = "exited"
			}
			source.details[id] = c
			if mode == "stale" {
				_, err = l.startJob(ctx, "example", plan.ID, "example", true)
			} else {
				_, err = l.reviewAction(ctx, "example", "", "takeover")
			}
			if err == nil || runner.ups != 0 {
				t.Fatal("unsafe takeover accepted", mode)
			}
			record, loadErr := l.load("example")
			if loadErr != nil || record.Takeover != nil {
				t.Fatal("failed review damaged original metadata")
			}
		})
	}
	l, _, _ := takeoverLibrary(t)
	record, _ := l.load("example")
	record.Warnings = []string{strings.Repeat("x", maxDefinition)}
	if l.writeRecord(record) == nil {
		t.Fatal("oversized persisted record accepted")
	}
	if _, err := l.load("example"); err != nil {
		t.Fatal("size failure corrupted existing record")
	}
}

func TestTakeoverUnsupportedDependenciesAndResourceDeclarations(t *testing.T) {
	for _, field := range []string{"command: ['${FIXTURE_PASS}']", "labels: {example: '$FIXTURE_PASS'}", "hostname: '${FIXTURE_HOST}'"} {
		raw := "services:\n web:\n  image: fixture/app\n  " + field + "\n"
		if takeoverShape(raw) == nil {
			t.Fatal("outside-environment interpolation accepted")
		}
	}
	if takeoverShape("services:\n web:\n  image: fixture/app\njobs: {}\n") == nil {
		t.Fatal("top-level jobs accepted")
	}
	for _, key := range []string{"build", "pull_policy", "scale", "deploy", "profiles", "depends_on", "models", "provider", "pre_start", "post_start", "pre_stop", "uts", "label_file", "credential_spec"} {
		raw := "services:\n  web:\n    image: fixture/app\n    " + key + ": {}\n"
		if key == "pull_policy" {
			continue
		} // First handoff overrides pulling with --pull never.
		if takeoverShape(raw) == nil {
			t.Fatal("unsupported dependency accepted", key)
		}
	}
	for _, raw := range []string{"services:\n web:\n  image: fixture/app\n  environment: [INHERITED]\n", "services:\n web:\n  image: fixture/app\n  volumes: [./data:/data]\n", "services:\n web:\n  image: fixture/app\nmodels: {}\n"} {
		if takeoverShape(raw) == nil {
			t.Fatal("unsafe dependency accepted")
		}
	}
	_, _, runner := takeoverLibrary(t)
	for _, kind := range []string{"volumes", "networks"} {
		var model map[string]any
		json.Unmarshal(runner.canonical, &model)
		model[kind].(map[string]any)["unused"] = map[string]any{"name": "unreviewed"}
		raw, _ := json.Marshal(model)
		if _, err := takeoverSnapshot(raw, []TakeoverContainer{runner.metadata}); err == nil {
			t.Fatal("unused resource accepted", kind)
		}
	}
	var model map[string]any
	json.Unmarshal(runner.canonical, &model)
	s := model["services"].(map[string]any)["web"].(map[string]any)
	s["volumes"] = []any{map[string]any{"type": "bind", "source": "/fixture/data", "target": "/data", "bind": map[string]any{"create_host_path": true}}}
	delete(model, "volumes")
	c := runner.metadata
	c.Mounts = []TakeoverMount{{Type: "bind", Source: "/fixture/data", Target: "/data"}}
	raw, _ := json.Marshal(model)
	files, err := takeoverSnapshot(raw, []TakeoverContainer{c})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(files["compose.yaml"], "create_host_path: false") {
		t.Fatal("missing bind source could be auto-created")
	}
}

func TestTakeoverRequiresVerifiedOwnershipAndDurableCompletion(t *testing.T) {
	for _, mode := range []string{"foreign-labels", "record-write-failure"} {
		t.Run(mode, func(t *testing.T) {
			l, source, runner := takeoverLibrary(t)
			recordPath := filepath.Join(l.root, "stacks", "example", "record.json")
			runner.afterUp = func() error {
				if mode == "foreign-labels" {
					source.data.Containers[0].ComposeDirectory = runner.base.ComposeDirectory
					source.data.Containers[0].ComposeFiles = runner.base.ComposeFiles
					return nil
				}
				if err := os.Rename(recordPath, recordPath+".fixture-backup"); err != nil {
					return err
				}
				return os.Mkdir(recordPath, 0700)
			}
			result := runFixtureTakeover(t, l)
			if result.Status != "failed" {
				t.Fatal("unverified or undurable takeover reported success")
			}
			if mode == "record-write-failure" {
				if err := os.Remove(recordPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(recordPath+".fixture-backup", recordPath); err != nil {
					t.Fatal(err)
				}
			}
			record, err := l.load("example")
			if err != nil || record.managed() || !l.needsRecovery(record) {
				t.Fatal("failed handoff lost pending recovery", err)
			}
		})
	}
}

func TestTakeoverDockerMetadataAdapter(t *testing.T) {
	for _, mode := range []string{"default", "alias", "ipam", "driver", "links", "propagation"} {
		t.Run(mode, func(t *testing.T) {
			endpoint := map[string]any{"NetworkID": strings.Repeat("d", 64), "Aliases": []string{"example_web_1", "web", monitorTestID[:12]}}
			mount := map[string]any{"Type": "volume", "Name": "example_data", "Source": "/var/lib/docker/volumes/example_data/_data", "Destination": "/data", "RW": true, "Mode": "rw"}
			switch mode {
			case "alias":
				endpoint["Aliases"] = []string{"custom-name"}
			case "ipam":
				endpoint["IPAMConfig"] = map[string]any{"IPv4Address": "192.0.2.7"}
			case "driver":
				endpoint["DriverOpts"] = map[string]string{"fictional": "option"}
			case "links":
				endpoint["Links"] = []string{"custom:link"}
			case "propagation":
				mount["Propagation"] = "shared"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("metadata adapter mutated Docker")
				}
				if r.URL.Path == "/version" {
					writeJSON(w, 200, map[string]string{"ApiVersion": "1.55", "MinAPIVersion": "1.40"})
					return
				}
				if r.URL.Path != "/v1.55/containers/"+monitorTestID+"/json" {
					http.NotFound(w, r)
					return
				}
				writeJSON(w, 200, map[string]any{"Id": monitorTestID, "Name": "/example_web_1", "Image": "sha256:" + strings.Repeat("b", 64), "Config": map[string]any{"Hostname": "fictional", "Env": []string{"TOKEN=fictional-private"}, "Labels": map[string]string{"com.docker.compose.service": "web", "com.docker.compose.config-hash": strings.Repeat("c", 64)}}, "Mounts": []any{mount}, "NetworkSettings": map[string]any{"Networks": map[string]any{"example_default": endpoint}, "Ports": map[string]any{}}})
			}))
			defer server.Close()
			source := &dockerSource{client: server.Client(), base: server.URL}
			c, err := source.TakeoverContainer(context.Background(), monitorTestID)
			if mode != "default" {
				if err == nil {
					t.Fatal("unsafe live endpoint accepted", mode)
				}
				return
			}
			if err != nil || c.Mounts[0].Source != "example_data" {
				t.Fatal("default metadata rejected", err)
			}
			raw, _ := json.Marshal(c)
			if strings.Contains(string(raw), "fictional-private") {
				t.Fatal("unselected credential material exposed")
			}
		})
	}
}

func TestTakeoverSnapshotComposeRenderEscapes(t *testing.T) {
	_, _, runner := takeoverLibrary(t)
	var model map[string]any
	json.Unmarshal(runner.canonical, &model)
	service := model["services"].(map[string]any)["web"].(map[string]any)
	service["command"] = []any{"fictional$$command"}
	service["labels"] = map[string]any{"fixture$$key": "fictional$$label"}
	raw, _ := json.Marshal(model)
	files, err := takeoverSnapshot(raw, []TakeoverContainer{runner.metadata})
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if yaml.Unmarshal([]byte(files["compose.yaml"]), &saved) != nil {
		t.Fatal("snapshot YAML invalid")
	}
	s := saved["services"].(map[string]any)["web"].(map[string]any)
	if s["environment"].(map[string]any)["VALUE"] != "fictional$$literal" || s["command"].([]any)[0] != "fictional$$command" || s["labels"].(map[string]any)["fixture$key"] != "fictional$$label" {
		t.Fatal("CLI escape inversion or YAML value escaping changed literal semantics")
	}
}
