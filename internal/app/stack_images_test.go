package app

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Changes the image behind one tag only when Compose applies, and can make the
// single after observation fail independently of the Compose result.
type imageTrackingFixture struct {
	*lifecycleFixture
	library                      *stackLibrary
	failApply, failRead, applied bool
	beforePersisted              bool
}

func (f *imageTrackingFixture) ProjectContainers(ctx context.Context, name string) ([]Container, error) {
	if f.applied && f.failRead {
		return nil, errors.New("fixture-private-diagnostic-must-not-be-saved")
	}
	return f.lifecycleFixture.ProjectContainers(ctx, name)
}

func (f *imageTrackingFixture) Run(_ context.Context, name, dir string, args ...string) ([]byte, error) {
	if args[0] != "up" {
		return nil, nil
	}
	record, _ := f.library.load(name)
	job, err := f.library.readJobFile(record.LastDeploymentAttempt.OperationID)
	f.beforePersisted = err == nil && job.Images != nil && len(job.Images.Services) > 0 && !job.Images.Services[0].AfterComplete
	f.mu.Lock()
	f.data.Containers = []Container{{ID: "new-web", Name: "example-web-1", Stack: name, Service: "web", Image: "fixture/web:latest", ImageID: "sha256:" + strings.Repeat("b", 64), State: "running", ComposeDirectory: dir, ComposeFiles: filepath.Join(dir, "compose.yaml")}}
	f.mu.Unlock()
	f.applied = true
	if f.failApply {
		return nil, errors.New("fixture partial apply")
	}
	return nil, nil
}

func TestOperationImagesSurviveFailureRestartAndBackup(t *testing.T) {
	for _, scenario := range []string{"success", "failed-apply", "unavailable-after"} {
		t.Run(scenario, func(t *testing.T) {
			l, source := coordinationLibrary(t)
			record, _ := l.load("example")
			record.DeployedAt = "2026-01-01T00:00:00Z"
			record.AppliedRevision, _ = l.contentFingerprint(record)
			if err := l.writeRecord(record); err != nil {
				t.Fatal(err)
			}
			dir, _ := l.contentDirectory(record)
			source.data.Containers = []Container{{ID: "old-web", Name: "example-web-1", Stack: "example", Service: "web", Image: "fixture/web:latest", ImageID: "sha256:" + strings.Repeat("a", 64), State: "running", ComposeDirectory: dir, ComposeFiles: filepath.Join(dir, "compose.yaml")}}
			fixture := &imageTrackingFixture{lifecycleFixture: source, library: l, failApply: scenario == "failed-apply", failRead: scenario == "unavailable-after"}
			l.source, l.runner = fixture, fixture
			job := startReviewed(t, l, "")
			if !fixture.beforePersisted || job.Images == nil || len(job.Images.Services) != 1 {
				t.Fatal("before image was not durable before apply")
			}
			row := job.Images.Services[0]
			if row.Before[0] != "sha256:"+strings.Repeat("a", 64) {
				t.Fatal("wrong before image")
			}
			if scenario == "unavailable-after" {
				if job.Status != "succeeded" || row.Status != "unavailable" || row.AfterComplete || job.Images.Warning == "" {
					t.Fatal("diagnostic read changed successful result", job)
				}
			} else if row.Status != "changed" || row.After[0] != "sha256:"+strings.Repeat("b", 64) {
				t.Fatal("same-tag image change was missed", row)
			}
			if scenario == "failed-apply" && job.Status != "failed" {
				t.Fatal("partial apply was reported as successful")
			}
			restarted := newLibrary(l.root, "", false, nil, nil)
			persisted, err := restarted.job(job.ID)
			if err != nil || !reflect.DeepEqual(persisted.Images, job.Images) {
				t.Fatal("image history lost on restart", err)
			}
			archive := filepath.Join(t.TempDir(), "library.zip")
			if err := l.writeArchive(archive, ""); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "restored")
			if err := restoreLibrary(archive, destination); err != nil {
				t.Fatal(err)
			}
			restored, err := newLibrary(destination, "", false, nil, nil).job(job.ID)
			if err != nil || !reflect.DeepEqual(restored.Images, job.Images) {
				t.Fatal("backup lost image history", err)
			}
		})
	}
}

func TestImageObservationScopesReplicasAndMissingIDs(t *testing.T) {
	l, _ := coordinationLibrary(t)
	record, _ := l.load("example")
	dir, _ := l.contentDirectory(record)
	container := func(service, id string) Container {
		return Container{Stack: "example", Service: service, ImageID: id, ComposeDirectory: dir, ComposeFiles: filepath.Join(dir, "compose.yaml")}
	}
	a, b := "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64)
	before := l.observeImages(record, "web", []Container{container("web", a), container("web", a), container("db", b)})
	after := l.observeImages(record, "web", []Container{container("web", a), container("db", a)})
	rows := operationImages([]ServiceSummary{{Name: "web"}}, before, after).Services
	if len(rows) != 1 || len(rows[0].Before) != 1 || rows[0].Status != "unchanged" {
		t.Fatal("replicas or unrelated service distorted image comparison", rows)
	}
	after = l.observeImages(record, "web", []Container{container("web", b), container("web", "fixture-sensitive-invalid-id")})
	if row := operationImages(nil, before, after).Services[0]; row.Status != "unavailable" || row.AfterComplete {
		t.Fatal("partial IDs were treated as complete")
	}
	oneOff := container("web", b)
	oneOff.ComposeOneOff = "True"
	foreign := container("web", b)
	foreign.Stack = "other"
	if len(l.observeImages(record, "", []Container{oneOff, foreign})) != 0 {
		t.Fatal("foreign/one-off images included")
	}
	unsafe := container("web", b)
	unsafe.ComposeDirectory = "/foreign"
	if l.observeImages(record, "", []Container{unsafe}) != nil {
		t.Fatal("unowned after observation trusted")
	}
	large := make([]Container, 201)
	for i := range large {
		large[i] = container("web", a)
	}
	if l.observeImages(record, "", large) != nil {
		t.Fatal("image observation exceeded its bound")
	}
	for _, example := range []struct {
		before, after map[string]observedServiceImages
		status        string
	}{
		{map[string]observedServiceImages{}, before, "added"}, {before, map[string]observedServiceImages{}, "removed"}, {before, nil, "unavailable"},
	} {
		if operationImages(nil, example.before, example.after).Services[0].Status != example.status {
			t.Fatal("incorrect absence/unknown result")
		}
	}
	// Older/interrupted jobs remain readable, and never invent an after image.
	job := StackJob{ID: randomID(), Name: "example", Status: "running", Images: operationImages(nil, before, nil)}
	if err := l.persistJob(job); err != nil {
		t.Fatal(err)
	}
	read, err := newLibrary(l.root, "", false, nil, nil).job(job.ID)
	if err != nil || read.Status != "interrupted" || read.Images.Services[0].AfterComplete {
		t.Fatal("interrupted result fabricated an observation")
	}
}
