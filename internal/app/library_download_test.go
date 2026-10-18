package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func archiveRequest(t *testing.T, handler http.Handler, path, body string) *http.Request {
	t.Helper()
	session := httptest.NewRecorder()
	handler.ServeHTTP(session, httptest.NewRequest("GET", "http://localhost/api/session", nil))
	var auth map[string]any
	if err := json.Unmarshal(session.Body.Bytes(), &auth); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "http://localhost"+path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://localhost")
	request.Header.Set("X-Stacklane-CSRF", auth["csrfToken"].(string))
	return request
}

func assertNoDownloadFiles(t *testing.T, l *stackLibrary) {
	t.Helper()
	entries, err := os.ReadDir(l.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".download-") {
			t.Fatal("private download files were left behind")
		}
	}
	if len(l.archiveSlot) != 0 {
		t.Fatal("archive slot was not released")
	}
}

func TestOnlineArchiveRoundTripAndSelectedExport(t *testing.T) {
	l, source := coordinationLibrary(t)
	current, _ := l.definition("example")
	input := editInput{Compose: strings.Replace(current.Compose, "nginx:alpine", "nginx:stable", 1), Revision: current.Fingerprint, ReplaceEnvironment: true, Environment: "TOKEN=fixture-export-value\n"}
	if _, err := l.edit(context.Background(), "example", input, true); err != nil {
		t.Fatal(err)
	}
	if startReviewed(t, l, "").Status != "succeeded" {
		t.Fatal("fixture apply failed")
	}
	recordBefore, _ := l.load("example")
	handler := newHandler(source, l)
	runnerCalls := len(l.runner.(*fixtureRunner).calls)
	for _, project := range []string{"", "example"} {
		path := "/api/library/backup"
		if project != "" {
			path = "/api/stacks/example/export"
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, archiveRequest(t, handler, path, `{"includeSecrets":true}`))
		if response.Code != 200 || response.Header().Get("Content-Type") != "application/zip" {
			t.Fatal(response.Code, response.Body.String())
		}
		if response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Header().Get("Content-Disposition"), "attachment;") {
			t.Fatal("unsafe download headers")
		}
		if response.Header().Get("Content-Length") != strconv.Itoa(response.Body.Len()) {
			t.Fatal("incorrect download length")
		}
		archive := filepath.Join(t.TempDir(), "download.zip")
		if err := os.WriteFile(archive, response.Body.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		files := archiveContents(t, archive)
		if project == "" {
			if _, ok := files["stacks/other/record.json"]; !ok {
				t.Fatal("library omitted another stack")
			}
			if _, ok := files["stacks/example/revisions/"+recordBefore.ConfigRevision+"/compose.yaml"]; !ok {
				t.Fatal("library omitted immutable revision")
			}
			if !strings.Contains(string(files["stacks/example/compose.yaml"]), "nginx:alpine") {
				t.Fatal("initial configuration was lost")
			}
			restored := filepath.Join(t.TempDir(), "restored")
			if err := restoreLibrary(archive, restored); err != nil {
				t.Fatal(err)
			}
			copy := newLibrary(restored, "", false, nil, nil)
			saved, err := copy.load("example")
			if err != nil || recordStamp(saved) != recordStamp(recordBefore) {
				t.Fatal("snapshot metadata did not restore exactly", err)
			}
		} else {
			if !strings.Contains(string(files["compose.yaml"]), "nginx:stable") || string(files["stack.env"]) != input.Environment {
				t.Fatal("export did not preserve selected source bytes")
			}
			if len(files) != 5 || files["stacklane-record.json"] == nil {
				t.Fatal("export contains unrelated data")
			}
			var manifest libraryManifest
			if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil || manifest.Kind != "compose-export" || manifest.Project != "example" {
				t.Fatal("incorrect export manifest")
			}
		}
		assertNoDownloadFiles(t, l)
	}
	if len(l.runner.(*fixtureRunner).calls) != runnerCalls {
		t.Fatal("archive contacted Compose")
	}
	after, _ := l.load("example")
	if recordStamp(after) != recordStamp(recordBefore) {
		t.Fatal("archive changed the live stack")
	}
}

func TestOnlineArchiveSnapshotDoesNotMixLaterEditsOrJobs(t *testing.T) {
	l, _ := coordinationLibrary(t)
	before, _ := l.load("example")
	oldJob := StackJob{ID: randomID(), Name: "example", Kind: "deploy", Status: "failed", Stage: "failed", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := l.persistJob(oldJob); err != nil {
		t.Fatal(err)
	}
	selection, dir, err := l.snapshotArchive(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	// Publication and history locks must be free before compression/download.
	done := make(chan error, 1)
	go func() {
		definition, err := l.definition("example")
		if err == nil {
			_, err = l.edit(context.Background(), "example", editInput{Compose: strings.Replace(definition.Compose, "nginx:alpine", "nginx:stable", 1), Revision: definition.Fingerprint}, true)
		}
		if err == nil {
			oldJob.Message = "new history after snapshot"
			err = l.persistJob(oldJob)
		}
		if err == nil {
			err = l.persistJob(StackJob{ID: randomID(), Name: "example", Status: "failed"})
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("snapshot retained publication locks")
	}
	archive := filepath.Join(dir, "snapshot.zip")
	if err := writeArchiveFiles(context.Background(), archive, selection); err != nil {
		t.Fatal(err)
	}
	files := archiveContents(t, archive)
	var saved StackRecord
	if err := json.Unmarshal(files["stacks/example/record.json"], &saved); err != nil || recordStamp(saved) != recordStamp(before) {
		t.Fatal("snapshot mixed later metadata")
	}
	if strings.Contains(string(files["jobs/"+oldJob.ID+".json"]), "new history after snapshot") {
		t.Fatal("snapshot mixed a later job write")
	}
	jobs := 0
	for name := range files {
		if strings.HasPrefix(name, "jobs/") {
			jobs++
		}
	}
	if jobs != 1 {
		t.Fatal("snapshot included later job creation")
	}
	if !strings.Contains(string(files["stacks/example/compose.yaml"]), "nginx:alpine") {
		t.Fatal("snapshot lost initial source")
	}
}

func TestOnlineArchiveSlotActiveOperationsCancellationAndCleanup(t *testing.T) {
	l, _ := coordinationLibrary(t)
	l.active["example"] = "reserved-before-checks"
	if _, err := l.prepareArchiveDownload(context.Background(), ""); !errors.Is(err, errArchiveActive) {
		t.Fatal("active operation was archived", err)
	}
	delete(l.active, "example")
	assertNoDownloadFiles(t, l)
	download, err := l.prepareArchiveDownload(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.prepareArchiveDownload(context.Background(), ""); !errors.Is(err, errArchiveBusy) {
		t.Fatal("concurrent archive was allowed")
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{download.directory, download.file.Name()} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm()&0077 != 0 {
				t.Fatal("temporary secrets are not private", err)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(download.directory, "files")); !os.IsNotExist(err) {
		t.Fatal("uncompressed copies retained during download")
	}
	// Holding a completed archive must not reserve the container-operation gate.
	if startReviewed(t, l, "").Status != "succeeded" {
		t.Fatal("archive blocked a later operation")
	}
	download.close()
	download.close()
	assertNoDownloadFiles(t, l)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.prepareArchiveDownload(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled request was prepared")
	}
	assertNoDownloadFiles(t, l)
	if _, err := l.prepareArchiveDownload(context.Background(), "missing"); err == nil {
		t.Fatal("unknown stack was exported")
	}
	assertNoDownloadFiles(t, l)
}

type failedArchiveWriter struct{ header http.Header }

func (w *failedArchiveWriter) Header() http.Header       { return w.header }
func (w *failedArchiveWriter) WriteHeader(int)           {}
func (w *failedArchiveWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestOnlineArchiveHTTPGuardsFailuresAndNoRestoreEndpoint(t *testing.T) {
	l, source := coordinationLibrary(t)
	handler := newHandler(source, l)
	for _, path := range []string{"/api/library/backup", "/api/stacks/example/export"} {
		for _, variation := range []string{"origin", "csrf", "type", "ack", "unknown", "multiple", "large", "method"} {
			body := `{"includeSecrets":true}`
			switch variation {
			case "ack":
				body = `{}`
			case "unknown":
				body = `{"includeSecrets":true,"path":"/outside"}`
			case "multiple":
				body += `{}`
			case "large":
				body += strings.Repeat(" ", 1024)
			}
			r := archiveRequest(t, handler, path, body)
			switch variation {
			case "origin":
				r.Header.Set("Origin", "http://attacker.invalid")
			case "csrf":
				r.Header.Del("X-Stacklane-CSRF")
			case "type":
				r.Header.Set("Content-Type", "text/plain")
			case "method":
				r.Method = "GET"
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, r)
			if response.Code == 200 || response.Header().Get("Content-Type") == "application/zip" {
				t.Fatalf("unsafe %s request accepted", variation)
			}
			assertNoDownloadFiles(t, l)
		}
		handler.ServeHTTP(&failedArchiveWriter{header: http.Header{}}, archiveRequest(t, handler, path, `{"includeSecrets":true}`))
		assertNoDownloadFiles(t, l)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, archiveRequest(t, handler, "/api/library/restore", `{"includeSecrets":true}`))
	if response.Code != 404 {
		t.Fatal("online restore was exposed")
	}
	base, _ := l.directory("example")
	if err := os.WriteFile(filepath.Join(base, "record.json"), []byte("{fixture-private-corruption"), 0600); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, archiveRequest(t, handler, "/api/library/backup", `{"includeSecrets":true}`))
	if response.Code != 500 || strings.Contains(response.Body.String(), "fixture-private-corruption") || strings.Contains(response.Body.String(), l.root) {
		t.Fatal("unsafe corruption error")
	}
	assertNoDownloadFiles(t, l)
}

func TestOnlineArchiveEmptyLibraryAndLinkedExport(t *testing.T) {
	l := newLibrary(filepath.Join(t.TempDir(), "new-data"), "", false, nil, nil)
	download, err := l.prepareArchiveDownload(context.Background(), "")
	if err != nil {
		t.Fatal("empty library backup failed", err)
	}
	download.close()
	assertNoDownloadFiles(t, l)
	l, _ = coordinationLibrary(t)
	outside := t.TempDir()
	record, _ := l.load("example")
	files, _ := l.content(record)
	record.ConfigRevision = filesFingerprint(files)
	base, _ := l.directory("example")
	if err := os.MkdirAll(filepath.Join(base, "revisions"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, raw := range files {
		if err := os.WriteFile(filepath.Join(outside, name), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(base, "revisions", record.ConfigRevision)); err != nil {
		t.Skip("directory symlinks unavailable")
	}
	if err := l.writeRecord(record); err != nil {
		t.Fatal(err)
	}
	if _, err := l.prepareArchiveDownload(context.Background(), "example"); err == nil {
		t.Fatal("export followed linked revision")
	}
	assertNoDownloadFiles(t, l)
}
