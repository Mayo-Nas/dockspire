package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func comparedPlan(t *testing.T, l *stackLibrary) UpdatePlan {
	t.Helper()
	plan, err := l.review(context.Background(), "example", "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Comparison == nil {
		t.Fatal("missing comparison")
	}
	return plan
}

func comparisonChange(t *testing.T, c *RevisionComparison, scope, field, kind string) ConfigurationChange {
	t.Helper()
	for _, change := range c.Changes {
		if change.Scope == scope && change.Field == field && change.Kind == kind {
			return change
		}
	}
	t.Fatalf("missing %s/%s/%s in %#v", scope, field, kind, c.Changes)
	return ConfigurationChange{}
}

func editComparisonFixture(t *testing.T, l *stackLibrary, compose, env string) {
	t.Helper()
	current, err := l.definition("example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.edit(context.Background(), "example", editInput{Compose: compose, Revision: current.Fingerprint, ReplaceEnvironment: true, Environment: env}, true); err != nil {
		t.Fatal(err)
	}
}

func TestComparisonUsesLastSuccessfulBundleThroughEditsFailureAndRestart(t *testing.T) {
	l, source, runner := testLibrary(t)
	first := "# initial comment\nservices:\n  web:\n    image: fixture/web:v1\n    ports: [\"127.0.0.1:8080:80\"]\n    volumes: [\"data:/data\"]\n    environment: {KEEP: same, CHANGE: fixture-old-value, REMOVE: fixture-remove}\n  retired:\n    image: fixture/retired:v1\nvolumes:\n  data: {}\n"
	if _, err := l.create(context.Background(), stackInput{Name: "example", Compose: first, Environment: "KEEP=same\nCHANGE=fixture-old-file\nREMOVE=fixture-file-remove\n"}); err != nil {
		t.Fatal(err)
	}
	initial := comparedPlan(t, l)
	if initial.Comparison.Status != "initial" {
		t.Fatal(initial.Comparison.Status)
	}
	comparisonChange(t, initial.Comparison, "Service web", "Service", "added")
	if startReviewed(t, l, "").Status != "succeeded" {
		t.Fatal("initial deployment failed")
	}
	baseline, _ := l.load("example")
	second := "services:\n  web:\n    image: fixture/web:v2\n    ports: [\"127.0.0.1:8081:80\"]\n    volumes: [\"data:/new-data:ro\"]\n    environment: {KEEP: same, CHANGE: fixture-new-value, ADD: fixture-add}\n  worker:\n    image: fixture/worker:v1\nvolumes:\n  data: {}\n"
	editComparisonFixture(t, l, second, "KEEP=same\nCHANGE=fixture-new-file\nADD=fixture-file-add\n")
	plan := comparedPlan(t, l)
	c := plan.Comparison
	if c.Status != "available" || c.BaseRevision != baseline.AppliedRevision || c.Revision != plan.Revision {
		t.Fatal("wrong baseline")
	}
	image := comparisonChange(t, c, "Service web", "Image", "changed")
	if image.Before != "fixture/web:v1" || image.After != "fixture/web:v2" {
		t.Fatal(image)
	}
	comparisonChange(t, c, "Service web", "Ports", "changed")
	comparisonChange(t, c, "Service web", "Mounts", "changed")
	comparisonChange(t, c, "Service retired", "Service", "removed")
	comparisonChange(t, c, "Service worker", "Service", "added")
	for _, scope := range []string{"Service web", ".env", "stack.env"} {
		comparisonChange(t, c, scope, "Environment · CHANGE", "changed")
		comparisonChange(t, c, scope, "Environment · REMOVE", "removed")
		comparisonChange(t, c, scope, "Environment · ADD", "added")
		for _, change := range c.Changes {
			if change.Scope == scope && change.Field == "Environment · KEEP" {
				t.Fatal("unchanged variable listed")
			}
		}
	}
	if l.plans[plan.ID].Comparison != nil {
		t.Fatal("display data retained in review cache")
	}
	runner.failUp = true
	if startReviewed(t, l, "").Status != "failed" {
		t.Fatal("expected failed apply")
	}
	reloaded := newLibrary(l.root, "", false, source, runner)
	failedPlan := comparedPlan(t, reloaded)
	if failedPlan.Kind != "recover" || failedPlan.Comparison.BaseRevision != baseline.AppliedRevision {
		t.Fatal("failed attempt became the comparison baseline")
	}
	runner.failUp = false
	if startReviewed(t, reloaded, "").Status != "succeeded" {
		t.Fatal("recovery failed")
	}
	secondBaseline, _ := reloaded.load("example")
	third := strings.Replace(second, "fixture/web:v2", "fixture/web:v3", 1)
	editComparisonFixture(t, reloaded, third, "KEEP=same\nCHANGE=fixture-new-file\nADD=fixture-file-add\n")
	final := comparedPlan(t, reloaded).Comparison
	image = comparisonChange(t, final, "Service web", "Image", "changed")
	if final.BaseRevision != secondBaseline.AppliedRevision || image.Before != "fixture/web:v2" || image.After != "fixture/web:v3" {
		t.Fatal("revision-directory baseline was not used")
	}
}

func TestComparisonDetectsSecretOnlyChangesWithoutLeakingInReviewAPI(t *testing.T) {
	l, source, _ := testLibrary(t)
	old := "services:\n  web:\n    image: fixture/web:v1\n    environment: {TOKEN: fixture-old-token, INHERITED: null}\n    command: [fixture-old-command]\n    labels: {anything: fixture-old-label}\n"
	if _, err := l.create(context.Background(), stackInput{Name: "example", Compose: old, Environment: "PASSWORD=fixture-old-password\n"}); err != nil {
		t.Fatal(err)
	}
	startReviewed(t, l, "")
	next := "# fixture-secret-comment\nservices:\n  web:\n    image: ${IMAGE:-fixture-image-default}\n    ports: [\"${PORT:-fixture-port-default}:80\"]\n    volumes: [\"${PATH:-fixture-path-default}:/data\"]\n    environment: {TOKEN: fixture-new-token, EMPTY: null}\n    command: [fixture-new-command]\n    labels: {anything: fixture-new-label}\n"
	editComparisonFixture(t, l, next, "PASSWORD=fixture-new-password\n")
	handler := newHandler(source, l)
	session := httptest.NewRecorder()
	handler.ServeHTTP(session, httptest.NewRequest("GET", "http://localhost/api/session", nil))
	var auth map[string]any
	_ = json.Unmarshal(session.Body.Bytes(), &auth)
	request := httptest.NewRequest("POST", "http://localhost/api/stacks/example/review", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://localhost")
	request.Header.Set("X-Stacklane-CSRF", auth["csrfToken"].(string))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, secret := range []string{"fixture-old-token", "fixture-new-token", "fixture-old-password", "fixture-new-password", "fixture-old-command", "fixture-new-command", "fixture-old-label", "fixture-new-label", "fixture-secret-comment", "fixture-image-default", "fixture-port-default", "fixture-path-default"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("review exposed %s", secret)
		}
	}
	var plan UpdatePlan
	if err := json.Unmarshal(response.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	comparisonChange(t, plan.Comparison, "Service web", "Environment · TOKEN", "changed")
	comparisonChange(t, plan.Comparison, "Service web", "Environment · INHERITED", "removed")
	comparisonChange(t, plan.Comparison, "Service web", "Environment · EMPTY", "added")
	comparisonChange(t, plan.Comparison, ".env", "Environment · PASSWORD", "changed")
	comparisonChange(t, plan.Comparison, "Service web", "Command", "changed")
	comparisonChange(t, plan.Comparison, "Service web", "Labels", "changed")
}

func TestComparisonNoChangesSourceOnlyAndEnvironmentRepresentation(t *testing.T) {
	l, _, _ := testLibrary(t)
	raw := "services:\n  web:\n    image: fixture/web:v1\n    environment: {KEY: fixture-value, INHERITED: null}\n"
	if _, err := l.create(context.Background(), stackInput{Name: "example", Compose: raw, Environment: "KEY=fixture-value\n"}); err != nil {
		t.Fatal(err)
	}
	startReviewed(t, l, "")
	c := comparedPlan(t, l).Comparison
	if c.Status != "available" || c.SourceOnly || len(c.Changes) != 0 {
		t.Fatal("unchanged definition produced changes")
	}
	changed := "# new comment\nservices:\n  web:\n    environment: [INHERITED, KEY=fixture-value]\n    image: fixture/web:v1\n"
	editComparisonFixture(t, l, changed, "# file comment\nKEY=fixture-value\n")
	c = comparedPlan(t, l).Comparison
	if !c.SourceOnly || len(c.Changes) != 0 {
		t.Fatalf("presentation change produced settings changes: %#v", c)
	}
}

func TestComparisonUnavailableBaselineDoesNotInventAdditions(t *testing.T) {
	for _, state := range []string{"legacy", "missing", "damaged", "invalid-hash"} {
		t.Run(state, func(t *testing.T) {
			l, _, _ := testLibrary(t)
			savedDraft(t, l)
			startReviewed(t, l, "")
			current, _ := l.definition("example")
			editComparisonFixture(t, l, strings.Replace(current.Compose, "nginx:alpine", "nginx:stable", 1), "KEY=fixture-file-value\n")
			record, _ := l.load("example")
			switch state {
			case "legacy":
				record.AppliedRevision, record.LastDeploymentAttempt = "", nil
			case "missing":
				record.AppliedRevision = strings.Repeat("a", 64)
			case "invalid-hash":
				record.AppliedRevision = "../outside"
			case "damaged":
				base, _ := l.directory("example")
				if err := os.WriteFile(filepath.Join(base, "compose.yaml"), []byte("fixture-damaged-body"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := l.writeRecord(record); err != nil {
				t.Fatal(err)
			}
			c := comparedPlan(t, l).Comparison
			if c.Status != "unavailable" || c.Warning == "" || len(c.Changes) != 0 {
				t.Fatal("missing baseline was presented as an empty deployment", c)
			}
		})
	}
}

func TestComparisonRejectsLinkedBaselineAndBoundsOutput(t *testing.T) {
	l, _, _ := testLibrary(t)
	savedDraft(t, l)
	record, _ := l.load("example")
	outside := t.TempDir()
	files := map[string]string{"compose.yaml": "services:\n  web:\n    image: fixture/external\n", ".env": "", "stack.env": ""}
	for name, raw := range files {
		if err := os.WriteFile(filepath.Join(outside, name), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	record.AppliedRevision = filesFingerprint(files)
	base, _ := l.directory("example")
	if err := os.MkdirAll(filepath.Join(base, "revisions"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "revisions", record.AppliedRevision)); err == nil {
		current, _ := l.content(record)
		if _, err := l.comparisonBaseline(record, current); err == nil {
			t.Fatal("followed linked baseline")
		}
	}
	c := &RevisionComparison{Changes: []ConfigurationChange{}}
	a, b := map[string]any{}, map[string]any{}
	for i := 0; i < 300; i++ {
		b["VARIABLE_"+strings.Repeat("A", i)] = "fixture-secret-value"
	}
	c.compareEnvironment(".env", a, b)
	if !c.Truncated || len(c.Changes) != maxComparisonChanges {
		t.Fatal("comparison was not bounded")
	}
	encoded, _ := json.Marshal(c)
	if strings.Contains(string(encoded), "fixture-secret-value") || len(encoded) > 150000 {
		t.Fatal("unbounded or unsafe comparison")
	}
}

func TestComparisonLongSyntaxAndSafeValues(t *testing.T) {
	ports := []any{map[string]any{"target": 80, "published": "8080", "host_ip": "127.0.0.1", "protocol": "tcp", "name": "fixture-hidden-port-name"}}
	value := comparisonValue("ports", ports)
	if !strings.Contains(value, "published=8080") || strings.Contains(value, "fixture-hidden-port-name") {
		t.Fatal(value)
	}
	mounts := []any{map[string]any{"type": "volume", "source": "data", "target": "/data", "read_only": true, "volume": map[string]any{"secret": "fixture-hidden-option"}}}
	value = comparisonValue("volumes", mounts)
	if !strings.Contains(value, "source=data") || strings.Contains(value, "fixture-hidden-option") {
		t.Fatal(value)
	}
	for _, image := range []string{"https://user:fixture-password@example.test/image", "user:fixture-password@example.test/image", "${IMAGE:-fixture-default}"} {
		if comparisonValue("image", image) != "[details hidden]" {
			t.Fatal("unsafe image display")
		}
	}
	if comparisonValue("ports", []any{"127.0.0.1:8080:80/tcp", "[::1]:8081:80", 8082}) != "127.0.0.1:8080:80/tcp\n[::1]:8081:80\n8082" {
		t.Fatal("normal ports were hidden")
	}
}
