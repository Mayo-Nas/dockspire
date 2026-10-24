package app

import (
	"archive/zip"
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestArchiveCopyFallbackNeverOverwrites(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "completed.zip")
	destination := filepath.Join(parent, "published.zip")
	if err := os.WriteFile(source, []byte("fixture-complete-archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyNewArchive(source, destination); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("fixture-replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyNewArchive(source, destination); err == nil {
		t.Fatal("copy fallback replaced an existing archive")
	}
	raw, err := os.ReadFile(destination)
	if err != nil || string(raw) != "fixture-complete-archive" {
		t.Fatal("copy fallback damaged a previous archive", err)
	}
}

func TestOfflineArchiveCLI(t *testing.T) {
	if os.Getenv("DOCKSPIRE_TEST_ARCHIVE_CLI") == "1" {
		for i, arg := range os.Args {
			if arg == "--" {
				os.Args = append([]string{os.Args[0]}, os.Args[i+1:]...)
				break
			}
		}
		flag.CommandLine = flag.NewFlagSet("fixture-dockspire", flag.ExitOnError)
		Run()
		return
	}
	l, _ := coordinationLibrary(t)
	for i := 0; i < 25; i++ {
		historyJob(t, l, "example", i)
	}
	parent := t.TempDir()
	archive := filepath.Join(parent, "library.zip")
	restored := filepath.Join(parent, "restored")
	run := func(wantSuccess bool, args ...string) {
		t.Helper()
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, append([]string{"-test.run=^TestOfflineArchiveCLI$", "--", "--docker-socket", filepath.Join(parent, "missing.sock"), "--compose-binary", filepath.Join(parent, "missing-compose")}, args...)...)
		command.Env = append(os.Environ(), "DOCKSPIRE_TEST_ARCHIVE_CLI=1")
		output, err := command.CombinedOutput()
		if (err == nil) != wantSuccess || ctx.Err() != nil {
			t.Fatalf("offline command failed: %v %s", err, output)
		}
		if strings.Contains(string(output), "Installation check") || strings.Contains(string(output), "fixture-not-a-real-secret") {
			t.Fatal("offline command contacted Docker or printed configuration")
		}
	}
	run(true, "--data-dir", l.root, "--backup", archive)
	run(true, "--data-dir", restored, "--restore", archive)
	run(false, "--data-dir", restored, "--restore", archive)
	run(true, "--data-dir", l.root, "--export-stack", "example", "--backup", filepath.Join(parent, "compose.zip"))
	run(true, "--data-dir", l.root, "--prune-history", "--history-keep", "10")
	reloaded := newLibrary(l.root, "", false, nil, nil)
	history, err := reloaded.historyDetails("example")
	if err != nil || len(history.Jobs) != 10 {
		t.Fatal("CLI retention failed", len(history.Jobs), err)
	}
	run(false, "--data-dir", l.root, "--export-stack", "example")
	run(false, "--history-keep", "0", "--data-dir", l.root, "--backup", filepath.Join(parent, "invalid.zip"))
}

func archiveContents(t *testing.T, path string) map[string][]byte {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	files := map[string][]byte{}
	for _, file := range reader.File {
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(stream)
		stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[file.Name] = raw
	}
	return files
}

func TestLibraryBackupRestoreAndPortableExport(t *testing.T) {
	parent := t.TempDir()
	l, _ := coordinationLibrary(t)
	draft, _ := l.definition("example")
	if _, err := l.edit(context.Background(), "example", editInput{Compose: strings.Replace(draft.Compose, "nginx:alpine", "nginx:stable", 1), Revision: draft.Fingerprint, ReplaceEnvironment: true, Environment: "TOKEN=fixture-private-value\n"}, true); err != nil {
		t.Fatal(err)
	}
	job := startReviewed(t, l, "")
	if job.Status != "succeeded" {
		t.Fatal(job.Message)
	}
	// Opt-in imports retain their origin and stay view-only after restoration.
	other, _ := l.load("other")
	other.Origin = "portainer"
	other.SourceDirectory = "/data/compose/fixture"
	if err := l.writeRecord(other); err != nil {
		t.Fatal(err)
	}
	callsBeforeArchive := len(l.runner.(*fixtureRunner).calls)
	archive := filepath.Join(parent, "library.zip")
	if err := l.writeArchive(archive, ""); err != nil {
		t.Fatal(err)
	}
	files := archiveContents(t, archive)
	var manifest libraryManifest
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil || manifest.Kind != "dockspire-library" || manifest.Version != 2 {
		t.Fatal("backup did not use the current Dockspire archive contract")
	}
	if !strings.Contains(string(files["stacks/example/record.json"]), job.ID) {
		t.Fatal("backup omitted deployment attempt")
	}
	if _, ok := files["jobs/"+job.ID+".json"]; !ok {
		t.Fatal("backup omitted operations")
	}
	if err := l.writeArchive(archive, ""); err == nil {
		t.Fatal("backup overwrote existing archive")
	}
	if err := l.writeArchive(filepath.Join(l.root, "unsafe.zip"), ""); err == nil {
		t.Fatal("backup allowed output inside library")
	}
	destination := filepath.Join(parent, "restored")
	if err := restoreLibrary(archive, destination); err != nil {
		t.Fatal(err)
	}
	restored := newLibrary(destination, "", false, &failingSource{}, nil)
	before, _ := l.definition("example")
	after, err := restored.definition("example")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("restored configuration/attempt differs", err)
	}
	for name, body := range files {
		if name == "manifest.json" {
			continue
		}
		actual, err := os.ReadFile(filepath.Join(destination, filepath.FromSlash(name)))
		if err != nil || string(actual) != string(body) {
			t.Fatal("restore changed file bytes", name, err)
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(filepath.Join(destination, filepath.FromSlash(name)))
			if err != nil || info.Mode().Perm()&0077 != 0 {
				t.Fatal("restored file permissions were not private", name, err)
			}
		}
	}
	if _, err := restored.review(context.Background(), "other", ""); err == nil {
		t.Fatal("restore adopted an imported stack")
	}
	if err := restoreLibrary(archive, destination); err == nil {
		t.Fatal("restore replaced existing data")
	}
	export := filepath.Join(parent, "compose.zip")
	if err := l.writeArchive(export, "example"); err != nil {
		t.Fatal(err)
	}
	exported := archiveContents(t, export)
	selected, _ := l.content(before.StackRecord)
	for _, name := range []string{"compose.yaml", ".env", "stack.env"} {
		if string(exported[name]) != selected[name] {
			t.Fatal("portable export changed selected source", name)
		}
	}
	if !strings.Contains(string(exported[".env"]), "fixture-private-value") || len(exported["dockspire-record.json"]) == 0 {
		t.Fatal("export was masked or omitted metadata")
	}
	if err := restoreLibrary(export, filepath.Join(parent, "wrong-kind")); err == nil {
		t.Fatal("Compose export accepted as a complete backup")
	}
	if len(l.runner.(*fixtureRunner).calls) != callsBeforeArchive {
		t.Fatal("archive unexpectedly invoked Compose")
	}
}

func TestArchiveRestoresInterruptedAttemptWithoutReplay(t *testing.T) {
	l, _, runner := testLibrary(t)
	if _, err := l.create(context.Background(), stackInput{Name: "example", Compose: testCompose}); err != nil {
		t.Fatal(err)
	}
	runner.failUp = true
	job := startReviewed(t, l, "")
	if job.Status != "failed" {
		t.Fatal(job.Status)
	}
	job.Status = "running"
	job.Stage = "applying"
	job.FinishedAt = ""
	if err := l.persistJob(job); err != nil {
		t.Fatal(err)
	}
	calls := len(runner.calls)
	parent := t.TempDir()
	archive := filepath.Join(parent, "interrupted.zip")
	destination := filepath.Join(parent, "restored")
	if err := l.writeArchive(archive, ""); err != nil {
		t.Fatal(err)
	}
	if err := restoreLibrary(archive, destination); err != nil {
		t.Fatal(err)
	}
	restored := newLibrary(destination, "", false, &failingSource{}, nil)
	definition, err := restored.definition("example")
	if err != nil || !definition.RecoveryRequired || definition.LastDeploymentAttempt == nil || definition.LastDeploymentAttempt.OperationID != job.ID || definition.AppliedRevision != "" {
		t.Fatal("restore lost recovery intent", err)
	}
	interrupted, err := restored.job(job.ID)
	if err != nil || interrupted.Status != "interrupted" || len(runner.calls) != calls {
		t.Fatal("restore replayed or lost unfinished work", err)
	}
}

func writeTestArchive(t *testing.T, path string, files map[string][]byte, alter func(*libraryManifest), extra string, symlink bool) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	writer := zip.NewWriter(file)
	manifest := libraryManifest{Version: 1, Kind: "dockspire-library", Files: map[string]string{}}
	for name, raw := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(raw); err != nil {
			t.Fatal(err)
		}
		manifest.Files[name] = fingerprint(string(raw))
	}
	if extra != "" {
		header := &zip.FileHeader{Name: extra}
		if symlink {
			header.SetMode(os.ModeSymlink | 0777)
		}
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		entry.Write([]byte("fixture"))
	}
	if alter != nil {
		alter(&manifest)
	}
	raw, _ := json.Marshal(manifest)
	entry, err := writer.Create("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	entry.Write(raw)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreRejectsUnsafeCorruptAndIncompleteArchives(t *testing.T) {
	for _, scenario := range []string{"traversal", "absolute", "windows", "symlink", "duplicate", "checksum", "missing", "version", "oversized", "missing-selected", "damaged-revision"} {
		t.Run(scenario, func(t *testing.T) {
			parent := t.TempDir()
			path := filepath.Join(parent, "archive.zip")
			destination := filepath.Join(parent, "destination")
			files := map[string][]byte{}
			var alter func(*libraryManifest)
			extra := ""
			symlink := false
			switch scenario {
			case "traversal":
				files["../escape"] = []byte("fixture")
			case "absolute":
				files["/escape"] = []byte("fixture")
			case "windows":
				files[`stacks\example\record.json`] = []byte("fixture")
			case "symlink":
				extra = "stacks/example/compose.yaml"
				symlink = true
			case "duplicate":
				files["jobs/"+strings.Repeat("a", 32)+".json"] = []byte("fixture")
				extra = "jobs/" + strings.Repeat("a", 32) + ".json"
			case "checksum":
				files["jobs/"+strings.Repeat("a", 32)+".json"] = []byte("fixture")
				alter = func(m *libraryManifest) {
					for name := range m.Files {
						m.Files[name] = strings.Repeat("0", 64)
					}
				}
			case "missing":
				alter = func(m *libraryManifest) { m.Files["stacks/example/compose.yaml"] = strings.Repeat("0", 64) }
			case "version":
				alter = func(m *libraryManifest) { m.Version = 999 }
			case "oversized":
				files["stacks/example/compose.yaml"] = []byte(strings.Repeat("a", maxDefinition+1))
			case "missing-selected", "damaged-revision":
				record := StackRecord{Name: "example", Origin: "new", ConfigRevision: strings.Repeat("a", 64)}
				raw, _ := json.Marshal(record)
				files["stacks/example/record.json"] = raw
				files["stacks/example/compose.yaml"] = []byte(testCompose)
				if scenario == "damaged-revision" {
					files["stacks/example/revisions/"+record.ConfigRevision+"/compose.yaml"] = []byte(testCompose)
				}
			}
			writeTestArchive(t, path, files, alter, extra, symlink)
			if err := restoreLibrary(path, destination); err == nil {
				t.Fatal("invalid archive restored", scenario)
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("failed restore published a destination")
			}
			entries, _ := os.ReadDir(parent)
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".dockspire-restore-") {
					t.Fatal("failed restore retained staging files")
				}
			}
		})
	}
}

func TestArchiveDoesNotFollowLinksOrHideMissingRecoveryRevisions(t *testing.T) {
	l, _ := coordinationLibrary(t)
	record, _ := l.load("example")
	record.LastDeploymentAttempt = &DeploymentAttempt{OperationID: randomID(), Revision: strings.Repeat("a", 64), StartedAt: "2026-10-16T12:00:00Z"}
	if err := l.writeRecord(record); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "backup.zip")
	if err := l.writeArchive(path, ""); err == nil {
		t.Fatal("backup hid a missing recovery revision")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("failed backup published an archive")
	}
}
