package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func BenchmarkWarmHistoryPage(b *testing.B) {
	for _, count := range []int{10, 100, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			l := newLibrary(b.TempDir(), "", false, nil, nil)
			dir := filepath.Join(l.root, "jobs")
			if err := os.Mkdir(dir, 0700); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < count; i++ {
				name := "other"
				if i == 0 {
					name = "example"
				}
				job := StackJob{ID: fmt.Sprintf("%032x", i+1), Name: name, Kind: "restart", Status: "succeeded", StartedAt: "2026-10-16T12:00:00Z"}
				raw, _ := json.Marshal(job)
				if err := os.WriteFile(filepath.Join(dir, job.ID+".json"), raw, 0600); err != nil {
					b.Fatal(err)
				}
			}
			if _, err := l.historyPage("example", "", 20); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				page, err := l.historyPage("example", "", 20)
				if err != nil || len(page.Jobs) != 1 {
					b.Fatal(err)
				}
			}
		})
	}
}

func historyJob(t *testing.T, l *stackLibrary, name string, i int) StackJob {
	t.Helper()
	job := StackJob{ID: fmt.Sprintf("%032x", i+1), Name: name, Kind: "restart", Status: "succeeded", Stage: "complete", StartedAt: time.Date(2026, 10, 1, 0, 0, i, 0, time.UTC).Format(time.RFC3339Nano)}
	if err := l.persistJob(job); err != nil {
		t.Fatal(err)
	}
	return job
}

func TestHistoryPagesStayScopedAndSurviveNewOperations(t *testing.T) {
	l, _ := coordinationLibrary(t)
	for i := 0; i < 25; i++ {
		historyJob(t, l, "example", i)
	}
	for i := 100; i < 180; i++ {
		historyJob(t, l, "other", i)
	}
	first, err := l.historyPage("example", "", 10)
	if err != nil || len(first.Jobs) != 10 || first.NextCursor == "" {
		t.Fatal(err)
	}
	// A cold index has already been built. Damage an unrelated file in place:
	// the requested page should not reopen unrelated operation files.
	path := filepath.Join(l.root, "jobs", fmt.Sprintf("%032x.json", 101))
	if err := os.WriteFile(path, []byte("{fixture-damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := l.historyPage("example", first.NextCursor, 10)
	if err != nil || len(second.Jobs) != 10 || len(second.Warnings) != 0 {
		t.Fatal("page rescanned unrelated records", err)
	}
	newer := historyJob(t, l, "example", 300)
	third, err := l.historyPage("example", second.NextCursor, 10)
	if err != nil || len(third.Jobs) != 5 || third.NextCursor != "" {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, page := range []StackHistory{first, second, third} {
		for _, job := range page.Jobs {
			if job.Name != "example" || seen[job.ID] {
				t.Fatal("foreign/duplicate history")
			}
			seen[job.ID] = true
		}
	}
	if seen[newer.ID] || len(seen) != 25 {
		t.Fatal("new insert shifted an existing cursor")
	}
	if _, err := l.historyPage("other", first.NextCursor, 10); err == nil {
		t.Fatal("cross-stack cursor accepted")
	}
	for _, query := range []string{"?details=1&limit=0", "?details=1&limit=101", "?details=1&cursor=../invalid", "?details=1&limit=bad"} {
		response := httptest.NewRecorder()
		newHandler(l.source, l).ServeHTTP(response, httptest.NewRequest("GET", "http://localhost:8787/api/stacks/example/operations"+query, nil))
		if response.Code == 200 {
			t.Fatal("invalid page accepted", query)
		}
	}
}

func TestHistoryPageBoundsReadsEvenWhenRecordsBecomeDamaged(t *testing.T) {
	l, _ := coordinationLibrary(t)
	for i := 0; i < 25; i++ {
		historyJob(t, l, "example", i)
	}
	page, err := l.historyPage("example", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range page.Jobs {
		if err := os.WriteFile(filepath.Join(l.root, "jobs", job.ID+".json"), []byte("{fixture-invalid"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	broken, err := l.historyPage("example", "", 10)
	if err != nil || len(broken.Jobs) != 0 || broken.NextCursor == "" || len(broken.Warnings) == 0 {
		t.Fatal("damaged page read past its bound", len(broken.Jobs), err)
	}
	next, err := l.historyPage("example", broken.NextCursor, 10)
	if err != nil || len(next.Jobs) != 10 {
		t.Fatal("damaged page blocked older readable history", err)
	}
}

func TestHistoryRetentionProtectsAttemptsAndSuccessfulBaseline(t *testing.T) {
	l, _ := coordinationLibrary(t)
	l.historyKeep = 3
	record, _ := l.load("example")
	baseline := historyJob(t, l, "example", 0)
	baseline.Kind = "deploy"
	baseline.Revision = record.Fingerprint
	if err := l.persistJob(baseline); err != nil {
		t.Fatal(err)
	}
	attempt := historyJob(t, l, "example", 1)
	attempt.Kind = "update"
	attempt.Status = "failed"
	if err := l.persistJob(attempt); err != nil {
		t.Fatal(err)
	}
	record.LastDeploymentAttempt = &DeploymentAttempt{OperationID: attempt.ID, Revision: record.Fingerprint, StartedAt: attempt.StartedAt}
	if err := l.writeRecord(record); err != nil {
		t.Fatal(err)
	}
	active := historyJob(t, l, "example", 2)
	for i := 3; i < 12; i++ {
		historyJob(t, l, "example", i)
	}
	foreign := historyJob(t, l, "other", 100)
	broken := filepath.Join(l.root, "jobs", randomID()+".json")
	if err := os.WriteFile(broken, []byte("{fixture-invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	l.jobsMu.Lock()
	l.active["example"] = active.ID
	l.pruneHistoryLocked("example")
	l.jobsMu.Unlock()
	for _, id := range []string{baseline.ID, attempt.ID, active.ID, foreign.ID} {
		if _, err := l.job(id); err != nil {
			t.Fatal("protected record removed", id)
		}
	}
	if _, err := os.Stat(broken); err != nil {
		t.Fatal("damaged history removed")
	}
	history, err := l.historyPage("example", "", 100)
	if err != nil || len(history.Jobs) != 6 || len(history.Warnings) != 1 {
		t.Fatal("retention did not bound unprotected history", len(history.Jobs), err)
	}
	l.jobsMu.Lock()
	delete(l.active, "example")
	l.jobsMu.Unlock()
	reloaded := newLibrary(l.root, "", false, l.source, l.runner)
	reloaded.historyKeep = 3
	reloaded.jobsMu.Lock()
	reloaded.pruneHistoryLocked("example")
	reloaded.jobsMu.Unlock()
	history, err = reloaded.historyPage("example", "", 100)
	if err != nil || len(history.Jobs) != 5 {
		t.Fatal("restart lost protected references", len(history.Jobs), err)
	}
}

func TestHistoryRetentionRunsAfterTerminalResultAndPreservesRevisions(t *testing.T) {
	l, _ := coordinationLibrary(t)
	l.historyKeep = 2
	for i := 0; i < 9; i++ {
		historyJob(t, l, "example", i)
	}
	definition, _ := l.definition("example")
	if _, err := l.edit(context.Background(), "example", editInput{Compose: strings.Replace(definition.Compose, "alpine", "stable", 1), Revision: definition.Fingerprint}, true); err != nil {
		t.Fatal(err)
	}
	job := startReviewed(t, l, "")
	if job.Status != "succeeded" {
		t.Fatal(job.Message)
	}
	history, err := l.historyPage("example", "", 100)
	if err != nil || len(history.Jobs) != 2 {
		t.Fatal("terminal result did not retain history", len(history.Jobs), err)
	}
	record, _ := l.load("example")
	if _, err := l.content(record); err != nil {
		t.Fatal("retention touched configuration", err)
	}
}
