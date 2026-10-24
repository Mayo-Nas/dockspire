package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func revisionFixture(t *testing.T, count int) (*stackLibrary, []string) {
	t.Helper()
	l, _ := coordinationLibrary(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	record, _ := l.load("example")
	record.CreatedAt = base.Format(time.RFC3339Nano)
	if err := l.writeRecord(record); err != nil {
		t.Fatal(err)
	}
	initial, _ := l.content(record)
	ids := []string{filesFingerprint(initial)}
	for i := 1; i <= count; i++ {
		current, _ := l.definition("example")
		compose := fmt.Sprintf("# version %d\nservices:\n  web:\n    image: fixture/web:v%d\n    environment: {TOKEN: fixture-private-%d}\n", i, i, i)
		record, err := l.edit(context.Background(), "example", editInput{Compose: compose, Revision: current.Fingerprint, ReplaceEnvironment: true, Environment: fmt.Sprintf("TOKEN=fixture-file-%d\n", i)}, true)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, record.Fingerprint)
		dir, _ := l.contentDirectory(record)
		stored := base.Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(dir, stored, stored); err != nil {
			t.Fatal(err)
		}
	}
	return l, ids
}

func TestRevisionHistoryPaginationRedactionAndRestoreDraft(t *testing.T) {
	l, ids := revisionFixture(t, 13)
	record, _ := l.load("example")
	record.AppliedRevision, record.DeployedAt = ids[2], "2026-01-02T00:00:00Z"
	record.LastDeploymentAttempt = &DeploymentAttempt{OperationID: randomID(), Revision: ids[3], StartedAt: "2026-01-03T00:00:00Z"}
	if err := l.writeRecord(record); err != nil {
		t.Fatal(err)
	}
	page, err := l.revisionHistory("example", "", 10)
	if err != nil || len(page.Revisions) != 10 || page.Revisions[0].Revision != ids[13] || !page.Revisions[0].Selected || page.NextCursor == "" {
		t.Fatal("wrong first page", page, err)
	}
	older, err := l.revisionHistory("example", page.NextCursor, 10)
	if err != nil || len(older.Revisions) != 4 || older.NextCursor != "" || !older.Revisions[3].Initial || !older.Revisions[1].Applied || !older.Revisions[0].Recovery {
		t.Fatal("wrong older page", older, err)
	}
	preview, err := l.revisionPreview("example", ids[1])
	if err != nil || !preview.CanRestore || preview.Comparison.BaseRevision != ids[13] || preview.Comparison.Revision != ids[1] {
		t.Fatal("wrong comparison", err)
	}
	encoded, _ := json.Marshal(preview)
	for _, secret := range []string{"fixture-private-", "fixture-file-"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("historical values leaked")
		}
	}
	beforeCalls := len(l.runner.(*fixtureRunner).calls)
	restored, err := l.restoreRevision(context.Background(), "example", ids[1], preview.RecordVersion, "example")
	if err != nil || restored.Fingerprint != ids[1] || restored.ConfigRevision != ids[1] || restored.AppliedRevision != record.AppliedRevision || restored.DeployedAt != record.DeployedAt || *restored.LastDeploymentAttempt != *record.LastDeploymentAttempt || restored.EditVersion != record.EditVersion+1 {
		t.Fatal("restore changed deployment state", restored, err)
	}
	files, _ := l.content(restored)
	if !strings.HasPrefix(files["compose.yaml"], "# version 1\n") || files[".env"] != "TOKEN=fixture-file-1\n" || files["stack.env"] != files[".env"] {
		t.Fatal("restore did not preserve exact bytes")
	}
	for _, call := range l.runner.(*fixtureRunner).calls[beforeCalls:] {
		if len(call) < 3 || call[2] != "config" {
			t.Fatal("draft restore attempted a container operation", call)
		}
	}
	if _, err := l.restoreRevision(context.Background(), "example", ids[2], preview.RecordVersion, "example"); err == nil {
		t.Fatal("stale preview was accepted")
	}
	preview, _ = l.revisionPreview("example", ids[0])
	initial, err := l.restoreRevision(context.Background(), "example", ids[0], preview.RecordVersion, "example")
	if err != nil || initial.ConfigRevision != "" || initial.Fingerprint != ids[0] {
		t.Fatal("initial bundle was not restored", err)
	}
	for _, input := range []struct {
		name, cursor string
		limit        int
	}{{"../escape", "", 10}, {"example", "../escape", 10}, {"example", strings.Repeat("a", 64), 10}, {"example", "", 0}, {"example", "", 101}} {
		if _, err := l.revisionHistory(input.name, input.cursor, input.limit); err == nil {
			t.Fatal("invalid revision page accepted")
		}
	}
	if _, err := l.revisionPreview("other", ids[1]); err == nil {
		t.Fatal("foreign revision was read")
	}
}

func TestRevisionCleanupProtectsReferencesAndPreservesArchiveRestore(t *testing.T) {
	l, ids := revisionFixture(t, 30)
	record, _ := l.load("example")
	record.AppliedRevision = ids[1]
	record.LastDeploymentAttempt = &DeploymentAttempt{OperationID: randomID(), Revision: ids[2], StartedAt: "2026-01-02T00:00:00Z"}
	if err := l.writeRecord(record); err != nil {
		t.Fatal(err)
	}
	for _, job := range []StackJob{
		{ID: record.LastDeploymentAttempt.OperationID, Name: "example", Kind: "update", Status: "failed", StartedAt: "2026-01-02T00:00:00Z", Revision: ids[2], PreviousAppliedRevision: ids[1]},
		{ID: randomID(), Name: "example", Kind: "update", Status: "succeeded", StartedAt: "2026-01-03T00:00:00Z", Revision: ids[3], PreviousAppliedRevision: ids[4]},
	} {
		if err := l.persistJob(job); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := l.reviewRevisionCleanup(context.Background(), "example")
	if err != nil || len(plan.Remove) != 6 || plan.Retained != 25 || plan.Eligible != 6 {
		t.Fatal("wrong cleanup selection", plan, err)
	}
	if _, err := l.cleanRevisions(context.Background(), "example", plan.Token, "wrong"); err == nil {
		t.Fatal("cleanup accepted wrong confirmation")
	}
	result, err := l.cleanRevisions(context.Background(), "example", plan.Token, "example")
	if err != nil || result.Removed != 6 {
		t.Fatal("cleanup failed", result, err)
	}
	for _, i := range []int{0, 1, 2, 3, 4, 11, 30} {
		if _, _, err := l.verifiedRevision("example", ids[i]); err != nil {
			t.Fatal("protected/recent configuration removed", i, err)
		}
	}
	for i := 5; i <= 10; i++ {
		if _, _, err := l.verifiedRevision("example", ids[i]); err == nil {
			t.Fatal("eligible configuration remained", i)
		}
	}
	if _, err := l.cleanRevisions(context.Background(), "example", plan.Token, "example"); err == nil {
		t.Fatal("old cleanup review replayed")
	}
	archive := filepath.Join(t.TempDir(), "library.zip")
	if err := l.writeArchive(archive, ""); err != nil {
		t.Fatal("retention broke backup", err)
	}
	destination := filepath.Join(t.TempDir(), "restored")
	if err := restoreLibrary(archive, destination); err != nil {
		t.Fatal("retention broke portable restore", err)
	}
	copy := newLibrary(destination, "", false, nil, nil)
	if _, _, err := copy.verifiedRevision("example", ids[1]); err != nil {
		t.Fatal("applied baseline lost through backup", err)
	}
}

func TestRevisionCleanupRejectsChangesDamageAndCancellation(t *testing.T) {
	for _, scenario := range []string{"new-save", "new-job", "damaged-job", "missing-reference", "damaged-candidate", "unexpected-file", "active", "imported", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			l, ids := revisionFixture(t, 23)
			plan, err := l.reviewRevisionCleanup(context.Background(), "example")
			if err != nil || len(plan.Remove) == 0 {
				t.Fatal(err)
			}
			ctx := context.Background()
			record, _ := l.load("example")
			switch scenario {
			case "new-save":
				record.EditVersion++
				l.writeRecord(record)
			case "new-job":
				l.persistJob(StackJob{ID: randomID(), Name: "example", Kind: "update", Status: "succeeded", StartedAt: "2026-01-02T00:00:00Z", Revision: ids[1]})
			case "damaged-job":
				os.MkdirAll(filepath.Join(l.root, "jobs"), 0700)
				os.WriteFile(filepath.Join(l.root, "jobs", randomID()+".json"), []byte("{"), 0600)
			case "missing-reference":
				record.AppliedRevision = strings.Repeat("a", 64)
				l.writeRecord(record)
			case "damaged-candidate":
				os.WriteFile(filepath.Join(l.root, "stacks", "example", "revisions", ids[1], "compose.yaml"), []byte("damaged"), 0600)
			case "unexpected-file":
				os.WriteFile(filepath.Join(l.root, "stacks", "example", "revisions", ids[1], "unknown.txt"), []byte("preserve me"), 0600)
			case "active":
				l.active["example"] = randomID()
			case "imported":
				record.Origin = "portainer"
				l.writeRecord(record)
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			if _, err := l.cleanRevisions(ctx, "example", plan.Token, "example"); err == nil {
				t.Fatal("unsafe/stale cleanup accepted")
			}
			for _, id := range ids[1:] {
				if _, err := os.Stat(filepath.Join(l.root, "stacks", "example", "revisions", id)); err != nil {
					t.Fatal("failed review removed a bundle", err)
				}
			}
		})
	}
}

func TestRevisionRestoreRechecksPublicationAfterValidation(t *testing.T) {
	for _, scenario := range []string{"generation", "active", "cleanup"} {
		t.Run(scenario, func(t *testing.T) {
			l, ids := revisionFixture(t, 23)
			preview, _ := l.revisionPreview("example", ids[1])
			blocker := &validationBlocker{entered: make(chan struct{}), release: make(chan struct{})}
			l.runner = blocker
			done := make(chan error, 1)
			go func() {
				_, err := l.restoreRevision(context.Background(), "example", ids[1], preview.RecordVersion, "example")
				done <- err
			}()
			select {
			case <-blocker.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("validation did not start")
			}
			switch scenario {
			case "generation":
				l.mu.Lock()
				record, _ := l.load("example")
				record.EditVersion++
				l.writeRecord(record)
				l.mu.Unlock()
			case "active":
				l.jobsMu.Lock()
				l.active["example"] = randomID()
				l.jobsMu.Unlock()
			case "cleanup":
				plan, err := l.reviewRevisionCleanup(context.Background(), "example")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := l.cleanRevisions(context.Background(), "example", plan.Token, "example"); err != nil {
					t.Fatal(err)
				}
			}
			close(blocker.release)
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("restore overwrote concurrent change")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("restore stuck")
			}
			record, _ := l.load("example")
			if record.ConfigRevision != ids[23] {
				t.Fatal("selected configuration changed")
			}
		})
	}
}

func TestRevisionAPISecurityAndUnsafeStorage(t *testing.T) {
	l, ids := revisionFixture(t, 1)
	handler := newHandler(l.source, l)
	for _, path := range []string{"/api/stacks/example/revisions", "/api/stacks/example/revisions/" + ids[0], "/api/stacks/example/revisions/cleanup"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "http://localhost"+path, nil))
		if response.Code != 200 || strings.Contains(response.Body.String(), "fixture-private-") || strings.Contains(response.Body.String(), "fixture-file-") {
			t.Fatal("unsafe revision response", response.Code)
		}
	}
	preview, _ := l.revisionPreview("example", ids[0])
	body := fmt.Sprintf(`{"recordVersion":%q,"confirmation":"example"}`, preview.RecordVersion)
	path := "/api/stacks/example/revisions/" + ids[0] + "/restore"
	for _, mode := range []string{"csrf", "origin", "confirmation", "valid"} {
		request := archiveRequest(t, handler, path, body)
		expected := 403
		switch mode {
		case "csrf":
			request.Header.Del("X-Dockspire-CSRF")
		case "origin":
			request.Header.Set("Origin", "http://foreign.invalid")
		case "confirmation":
			request = archiveRequest(t, handler, path, strings.Replace(body, `"example"`, `"wrong"`, 1))
			expected = 400
		case "valid":
			expected = 200
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != expected {
			t.Fatal(mode, response.Code, response.Body.String())
		}
	}
	record, _ := l.load("example")
	record.Origin = "portainer"
	l.writeRecord(record)
	preview, _ = l.revisionPreview("example", ids[1])
	if preview.CanRestore {
		t.Fatal("imported stack can restore")
	}
	if _, err := l.restoreRevision(context.Background(), "example", ids[1], preview.RecordVersion, "example"); err == nil {
		t.Fatal("imported stack restored")
	}
	if runtime.GOOS == "windows" {
		return
	} // Linux is the deployment target; symlink creation requires privilege on Windows.
	root := l.root
	l.root = filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(root, l.root); err != nil {
		t.Fatal(err)
	}
	if _, err := l.revisionPreview("example", ids[0]); err == nil {
		t.Fatal("linked library read")
	}
}

type revisionBundleRunner struct {
	t     *testing.T
	files map[string]string
}

func (r revisionBundleRunner) Run(_ context.Context, _ string, directory string, args ...string) ([]byte, error) {
	r.t.Helper()
	if len(args) == 0 || args[0] != "config" {
		r.t.Error("restore attempted a container operation")
	}
	for name, expected := range r.files {
		raw, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || string(raw) != expected {
			r.t.Errorf("validation did not use exact %s bytes", name)
		}
	}
	return nil, nil
}

func TestRevisionRestoreValidatesDistinctEnvironmentFiles(t *testing.T) {
	l, _ := coordinationLibrary(t)
	record, _ := l.load("example")
	base, _ := l.directory("example")
	if err := os.WriteFile(filepath.Join(base, "stack.env"), []byte("ASSOCIATED=fixture-old-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	initial, _ := l.content(record)
	id := filesFingerprint(initial)
	current, _ := l.definition("example")
	if _, err := l.edit(context.Background(), "example", editInput{Compose: strings.Replace(current.Compose, "nginx:alpine", "nginx:stable", 1), Revision: current.Fingerprint, ReplaceEnvironment: true, Environment: "NEW=fixture-new\n"}, true); err != nil {
		t.Fatal(err)
	}
	l.runner = revisionBundleRunner{t, initial}
	preview, _ := l.revisionPreview("example", id)
	restored, err := l.restoreRevision(context.Background(), "example", id, preview.RecordVersion, "example")
	if err != nil || len(restored.EnvironmentNames) != 1 || restored.EnvironmentNames[0] != "ASSOCIATED" {
		t.Fatal("associated environment was not validated/restored", restored.EnvironmentNames, err)
	}
}

func TestRevisionCleanupBatchAndHTTPReviewProtection(t *testing.T) {
	l, _ := revisionFixture(t, 121)
	handler := newHandler(l.source, l)
	plan, err := l.reviewRevisionCleanup(context.Background(), "example")
	if err != nil || len(plan.Remove) != revisionCleanupBatch || plan.Eligible != 101 {
		t.Fatal("wrong batch limit", len(plan.Remove), plan.Eligible, err)
	}
	body, _ := json.Marshal(map[string]string{"token": plan.Token, "confirmation": "example"})
	path := "/api/stacks/example/revisions/cleanup"
	for _, mode := range []string{"csrf", "origin", "valid"} {
		request := archiveRequest(t, handler, path, string(body))
		expected := 403
		if mode == "csrf" {
			request.Header.Del("X-Dockspire-CSRF")
		} else if mode == "origin" {
			request.Header.Set("Origin", "http://foreign.invalid")
		} else {
			expected = 200
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != expected {
			t.Fatal(mode, response.Code, response.Body.String())
		}
	}
	remaining, err := l.reviewRevisionCleanup(context.Background(), "example")
	if err != nil || len(remaining.Remove) != 1 || remaining.Eligible != 1 {
		t.Fatal("remaining cleanup candidates were lost", remaining, err)
	}
	base, _ := l.directory("example")
	entries, _ := os.ReadDir(base)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".revision-cleanup-") {
			t.Fatal("private cleanup files were left behind")
		}
	}
}
