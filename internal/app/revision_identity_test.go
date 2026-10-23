package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFingerprintDistinguishesPresenceAndRejectsStaleReview(t *testing.T) {
	a := map[string]string{"compose.yaml": testCompose}
	b := map[string]string{"compose.yaml": testCompose, ".env": ""}
	c := map[string]string{"compose.yaml": testCompose, ".env": "", "stack.env": ""}
	if filesFingerprint(a) == filesFingerprint(b) || filesFingerprint(b) == filesFingerprint(c) || legacyFilesFingerprint(a) != legacyFilesFingerprint(c) {
		t.Fatal("presence identity is ambiguous")
	}
	l, _, _ := testLibrary(t)
	if _, err := l.create(context.Background(), stackInput{Name: "example", Compose: testCompose}); err != nil {
		t.Fatal(err)
	}
	before, _ := l.definition("example")
	plan, err := l.review(context.Background(), "example", "")
	if err != nil {
		t.Fatal(err)
	}
	base, _ := l.directory("example")
	if err := os.Remove(filepath.Join(base, ".env")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.edit(context.Background(), "example", editInput{Compose: before.Compose, Revision: before.Fingerprint}, true); err == nil {
		t.Fatal("presence-only stale editor accepted")
	}
	if _, err := l.startJob(context.Background(), "example", plan.ID, "example"); err == nil {
		t.Fatal("presence-only stale operation accepted")
	}
}

func TestLegacyMissingEnvironmentCanBeSavedAsPresentEmpty(t *testing.T) {
	l, _, _ := testLibrary(t)
	if _, err := l.create(context.Background(), stackInput{Name: "example", Compose: testCompose}); err != nil {
		t.Fatal(err)
	}
	record, _ := l.load("example")
	base, _ := l.directory("example")
	for _, name := range []string{".env", "stack.env"} {
		if err := os.Remove(filepath.Join(base, name)); err != nil {
			t.Fatal(err)
		}
	}
	record.InitialFingerprintVersion = 0
	initial, _ := l.content(record)
	old := legacyFilesFingerprint(initial)
	record.AppliedRevision, record.DeployedAt = old, "2026-01-01T00:00:00Z"
	if err := l.writeRecord(record); err != nil {
		t.Fatal(err)
	}
	definition, _ := l.definition("example")
	saved, err := l.edit(context.Background(), "example", editInput{Compose: definition.Compose, Revision: definition.Fingerprint, ReplaceEnvironment: true}, true)
	if err != nil || saved.ConfigRevision == "" || saved.ConfigRevision == old {
		t.Fatal("empty environment was not stored as a new revision", err)
	}
	files, _ := l.content(saved)
	for _, name := range []string{".env", "stack.env"} {
		if _, ok := files[name]; !ok {
			t.Fatal("missing saved empty file", name)
		}
	}
	if l.appliedMatches(saved, files) {
		t.Fatal("new empty files incorrectly match absent applied files")
	}
	previous, err := l.comparisonBaseline(saved, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := previous[".env"]; present {
		t.Fatal("legacy applied baseline changed")
	}
}

func legacyRevisionLibrary(t *testing.T) (*stackLibrary, []string) {
	t.Helper()
	l, ids := revisionFixture(t, 24)
	record, _ := l.load("example")
	record.InitialFingerprintVersion = 0
	base, _ := l.directory("example")
	initial, _ := l.content(StackRecord{Name: "example"})
	old := []string{legacyFilesFingerprint(initial)}
	for _, id := range ids[1:] {
		files, err := l.content(StackRecord{Name: "example", ConfigRevision: id})
		if err != nil {
			t.Fatal(err)
		}
		legacy := legacyFilesFingerprint(files)
		if err := os.Rename(filepath.Join(base, "revisions", id), filepath.Join(base, "revisions", legacy)); err != nil {
			t.Fatal(err)
		}
		old = append(old, legacy)
	}
	record.ConfigRevision, record.AppliedRevision = old[24], old[1]
	record.Fingerprint = old[24]
	record.DeployedAt = "2026-01-01T00:00:00Z"
	job := StackJob{ID: randomID(), Name: "example", Kind: "recover", Status: "interrupted", Stage: "interrupted", Revision: old[2], PreviousAppliedRevision: old[1], StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	record.LastDeploymentAttempt = &DeploymentAttempt{OperationID: job.ID, Revision: job.Revision, StartedAt: job.StartedAt}
	if err := ensurePrivateDirectory(filepath.Join(l.root, "jobs")); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(filepath.Join(l.root, "jobs", job.ID+".json"), job); err != nil {
		t.Fatal(err)
	}
	if err := l.writeRecord(record); err != nil {
		t.Fatal(err)
	}
	return l, old
}

func TestLegacyRevisionHistoryRecoveryCleanupAndArchives(t *testing.T) {
	l, ids := legacyRevisionLibrary(t)
	record, _ := l.load("example")
	if !l.needsRecovery(record) {
		t.Fatal("legacy interrupted recovery was lost")
	}
	history, err := l.revisionHistory("example", "", 100)
	if err != nil || len(history.Revisions) != 25 {
		t.Fatal("legacy history unavailable", err)
	}
	selected, err := l.storedRevision(record)
	if err != nil || selected != ids[24] {
		t.Fatal("selected legacy ID changed", err)
	}
	cleanup, err := l.reviewRevisionCleanup(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range cleanup.Remove {
		if id == ids[0] || id == ids[1] || id == ids[2] || id == ids[24] {
			t.Fatal("legacy reference eligible for deletion")
		}
	}
	if _, err := l.cleanRevisions(context.Background(), "example", cleanup.Token, "example"); err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{1, 2} {
		var selection archiveSelection
		if err := l.selectArchiveFiles(context.Background(), "", &selection); err != nil {
			t.Fatal(err)
		}
		selection.manifest.Version = version
		archive := filepath.Join(t.TempDir(), "legacy.zip")
		if err := writeArchiveFiles(context.Background(), archive, selection); err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(t.TempDir(), "restored")
		if err := restoreLibrary(archive, root); err != nil {
			t.Fatal("archive version", version, err)
		}
		restored := newLibrary(root, "", false, l.source, l.runner)
		got, err := restored.load("example")
		if err != nil || got.ConfigRevision != ids[24] || got.AppliedRevision != ids[1] || got.LastDeploymentAttempt.Revision != ids[2] || !restored.needsRecovery(got) {
			t.Fatal("legacy archive changed persistent IDs", err)
		}
		preview, err := restored.revisionPreview("example", ids[1])
		if err != nil {
			t.Fatal(err)
		}
		draft, err := restored.restoreRevision(context.Background(), "example", ids[1], preview.RecordVersion, "example")
		if err != nil || draft.ConfigRevision != ids[1] || draft.LastDeploymentAttempt.Revision != ids[2] {
			t.Fatal("legacy draft restore changed recovery", err)
		}
		draft.LastDeploymentAttempt = nil
		draft.AppliedRevision = ids[1]
		if err := restored.writeRecord(draft); err != nil {
			t.Fatal(err)
		}
		directory, _ := restored.contentDirectory(draft)
		source := l.source.(*lifecycleFixture)
		source.mu.Lock()
		source.data.Containers = []Container{{ID: strings.Repeat("a", 64), Name: "example-web-1", Stack: "example", Service: "web", State: "running", ComposeDirectory: directory, ComposeFiles: filepath.Join(directory, "compose.yaml")}}
		source.mu.Unlock()
		plan, err := restored.buildPlan(context.Background(), "example", strings.Repeat("a", 64))
		if err != nil || plan.Kind != "service-update" || plan.Revision != ids[1] || plan.exactRevision == "" {
			t.Fatal("legacy applied service review was blocked", err)
		}
	}
}

func TestPendingLegacyTakeoverRetainsSourceAndRevisionIDs(t *testing.T) {
	l, _, _ := takeoverLibrary(t)
	plan, err := l.prepareTakeover(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	record, _ := l.load("example")
	record.InitialFingerprintVersion = 0
	source, _ := l.content(StackRecord{Name: "example"})
	plan.takeover.SourceFingerprint = legacyFilesFingerprint(source)
	plan.takeover.Revision = legacyFilesFingerprint(plan.takeoverFiles)
	record.ConfigRevision = plan.takeover.Revision
	record.Takeover = plan.takeover
	record.LastDeploymentAttempt = &DeploymentAttempt{OperationID: randomID(), Revision: plan.takeover.Revision, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := l.storeTakeoverRevision(record, plan.takeoverFiles); err != nil {
		t.Fatal(err)
	}
	if err := l.writeRecord(record); err != nil {
		t.Fatal(err)
	}
	recovered, err := l.prepareTakeover(context.Background(), "example")
	if err != nil || recovered.Revision != record.ConfigRevision || recovered.takeover.SourceFingerprint != record.Takeover.SourceFingerprint || recovered.exactRevision == "" {
		t.Fatal("legacy takeover recovery identity changed", err)
	}
}

func TestLegacyDefinitionUsesExactUnappliedState(t *testing.T) {
	l, ids := legacyRevisionLibrary(t)
	record, _ := l.load("example")
	record.AppliedRevision, record.LastDeploymentAttempt = record.ConfigRevision, nil
	if err := l.writeRecord(record); err != nil {
		t.Fatal(err)
	}
	definition, err := l.definition("example")
	if err != nil || definition.UnappliedChanges || definition.Fingerprint == ids[24] {
		t.Fatal("legacy alias was shown as unapplied or used as editor token", err)
	}
	preview, err := l.revisionPreview("example", ids[24])
	if err != nil || preview.Comparison.SourceOnly || len(preview.Comparison.Changes) != 0 {
		t.Fatal("identical legacy bytes were shown as source-only changes", err)
	}
}

func TestExactFingerprintPreservesRawBytesAndBoundaries(t *testing.T) {
	base := map[string]string{"compose.yaml": testCompose, ".env": "\xff"}
	other := map[string]string{"compose.yaml": testCompose, ".env": "\xfe"}
	if filesFingerprint(base) == filesFingerprint(other) {
		t.Fatal("invalid UTF-8 byte identities collided")
	}
	a := map[string]string{"compose.yaml": "a\x00.env\x00b", ".env": "c"}
	b := map[string]string{"compose.yaml": "a", ".env": "b\x00.env\x00c"}
	if filesFingerprint(a) == filesFingerprint(b) {
		t.Fatal("embedded delimiter boundaries collided")
	}
}
func TestCreateResponseTokenCanBeUsedForEdit(t *testing.T) {
	l, _, _ := testLibrary(t)
	record, err := l.create(context.Background(), stackInput{Name: "example", Compose: testCompose, Environment: "KEY=fixture-value\n"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.edit(context.Background(), "example", editInput{Compose: testCompose, Revision: record.Fingerprint}, true); err != nil {
		t.Fatal("create response was not an exact editor token", err)
	}
}
