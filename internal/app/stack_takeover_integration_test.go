package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Explicit opt-in only. Never point this test at an existing project or source.
// DOCKSPIRE_TEST_IMAGE must be a locally cached immutable Dockspire image ID.
func TestTakeoverDockerIntegration(t *testing.T) {
	if os.Getenv("DOCKSPIRE_TAKEOVER_TEST") != "1" {
		t.Skip("explicit disposable Docker opt-in required")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Fatal("fixture requires Linux amd64")
	}
	image := os.Getenv("DOCKSPIRE_TEST_IMAGE")
	if !strings.HasPrefix(image, "sha256:") || !revisionName.MatchString(strings.TrimPrefix(image, "sha256:")) {
		t.Fatal("an explicit cached immutable image ID is required")
	}
	name := "dockspire-takeover-fixture-" + randomID()[:12]
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "source")
	sourceDir := filepath.Join(sourceRoot, "7")
	bindDir := filepath.Join(root, "bind")
	for _, dir := range []string{sourceDir, bindDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	docker := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/nonexistent", "DOCKER_CONFIG=/nonexistent", "DOCKER_HOST=unix:///var/run/docker.sock"}
		cmd.Stderr = io.Discard
		return cmd.Output()
	}
	run := func(args ...string) string {
		raw, err := docker(args...)
		if err != nil {
			t.Fatalf("Disposable Docker fixture %s failed: %v", args[0], err)
		}
		return strings.TrimSpace(string(raw))
	}
	if run("image", "inspect", "--format", "{{.Id}}", image) != image {
		t.Fatal("cached image identity mismatch")
	}
	for _, args := range [][]string{{"ps", "-aq", "--filter", "label=com.docker.compose.project=" + name}, {"volume", "ls", "-q", "--filter", "label=com.docker.compose.project=" + name}, {"network", "ls", "-q", "--filter", "label=com.docker.compose.project=" + name}, {"ps", "-aq", "--filter", "label=io.dockspire.takeover.fixture=" + name}} {
		if run(args...) != "" {
			t.Fatal("fixture resources already exist")
		}
	}
	source := newDockerSource("/var/run/docker.sock")
	volumeNames := []string{name + "_data", name + "_worker_data"}
	networkName := name + "_default"
	for _, volume := range volumeNames {
		if _, err := docker("volume", "inspect", volume); err == nil {
			t.Fatal("fixture volume name collision")
		}
	}
	if _, err := docker("network", "inspect", networkName); err == nil {
		t.Fatal("fixture network name collision")
	}
	// Only remove exact newly owned resources. Any unexpected project member
	// blocks cleanup rather than widening the deletion scope.
	t.Cleanup(func() {
		items, err := source.ProjectContainers(context.Background(), name)
		if err != nil {
			t.Error("Cannot inspect fixture cleanup scope")
			return
		}
		for _, c := range items {
			if (c.Service != "web" && c.Service != "worker") || c.Name != name+"-"+c.Service+"-1" || (c.ComposeFiles != "/data/compose/7/docker-compose.yml" && !strings.HasPrefix(c.ComposeDirectory, root+"/")) {
				t.Error("Unexpected fixture member; cleanup blocked")
				return
			}
		}
		for _, c := range items {
			if _, err := docker("rm", "-f", c.ID); err != nil {
				t.Error("Cannot remove owned fixture container")
				return
			}
		}
		controls, err := docker("ps", "-aq", "--no-trunc", "--filter", "label=io.dockspire.takeover.fixture="+name)
		if err != nil {
			t.Error("Cannot inspect fixture control container")
			return
		}
		for _, id := range strings.Fields(string(controls)) {
			if !revisionName.MatchString(id) {
				t.Error("Invalid control identity")
				return
			}
			if _, err := docker("rm", "-f", id); err != nil {
				t.Error("Cannot remove owned control container")
				return
			}
		}
		for _, volume := range volumeNames {
			label, err := docker("volume", "inspect", "--format", `{{index .Labels "com.docker.compose.project"}}`, volume)
			if err != nil {
				continue
			}
			if strings.TrimSpace(string(label)) != name {
				t.Error("Unowned fixture volume; removal blocked")
				continue
			}
			if _, err := docker("volume", "rm", volume); err != nil {
				t.Error("Cannot remove owned fixture volume")
			}
		}
		label, err := docker("network", "inspect", "--format", `{{index .Labels "com.docker.compose.project"}}`, networkName)
		if err == nil {
			if strings.TrimSpace(string(label)) != name {
				t.Error("Unowned fixture network; removal blocked")
				return
			}
			if _, err := docker("network", "rm", networkName); err != nil {
				t.Error("Cannot remove owned fixture network")
			}
		}
	})
	// Extract only the shipped executable, never credential/configuration files.
	copyID := run("create", "--name", name+"-compose", "--label", "io.dockspire.takeover.fixture="+name, "--entrypoint", "/docker-compose", image, "version")
	if !revisionName.MatchString(copyID) {
		t.Fatal("invalid owned executable-copy container ID")
	}
	composeBinary := filepath.Join(root, "docker-compose")
	run("cp", copyID+":/docker-compose", composeBinary)
	if err := os.Chmod(composeBinary, 0700); err != nil {
		t.Fatal(err)
	}
	run("rm", copyID)
	marker := []byte("fictional retained fixture data\n")
	markerPath := filepath.Join(bindDir, "marker")
	if err := os.WriteFile(markerPath, marker, 0600); err != nil {
		t.Fatal(err)
	}
	definition := "services:\n"
	for _, service := range []string{"web", "worker"} {
		volume := "data"
		if service == "worker" {
			volume = "worker_data"
		}
		definition += fmt.Sprintf("  %s:\n    image: %s\n    command: [--demo, --https, --setup, --data-dir, /appdata]\n    environment:\n      FIXTURE_VALUE: 'fictional$$value'\n    labels:\n      'io.dockspire.fixture$key': 'fictional$$label'\n    read_only: true\n    cap_drop: [ALL]\n    security_opt: [no-new-privileges:true]\n    mem_limit: 128m\n    pids_limit: 64\n    volumes:\n      - %s:/appdata\n      - type: bind\n        source: %q\n        target: /sentinels\n        read_only: true\n        bind:\n          create_host_path: false\n", service, image, volume, bindDir)
	}
	definition += "volumes:\n  data: {}\n  worker_data: {}\n"
	for file, body := range map[string]string{"docker-compose.yml": definition, ".env": ""} {
		if err := os.WriteFile(filepath.Join(sourceDir, file), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal("Cannot establish fixture supplemental groups")
	}
	seedArgs := []string{"run", "--rm", "--pull", "never", "--name", name + "-seed", "--label", "io.dockspire.takeover.fixture=" + name, "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--env", "HOME=/nonexistent", "--env", "DOCKER_CONFIG=/nonexistent", "--env", "DOCKER_HOST=unix:///var/run/docker.sock", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--memory", "128m", "--pids-limit", "64", "--tmpfs", "/tmp:rw,noexec,nosuid,size=32m"}
	for _, group := range groups {
		seedArgs = append(seedArgs, "--group-add", fmt.Sprint(group))
	}
	seedArgs = append(seedArgs, "-v", sourceRoot+":/data/compose:ro", "-v", "/var/run/docker.sock:/var/run/docker.sock:ro", "--entrypoint", "/docker-compose", image, "--project-name", name, "--project-directory", "/data/compose/7", "--env-file", "/data/compose/7/.env", "-f", "/data/compose/7/docker-compose.yml", "up", "--detach", "--force-recreate", "--no-build", "--pull", "never", "--wait", "--wait-timeout", "60")
	seed := func() { run(seedArgs...) }
	seed()
	before, err := source.ProjectContainers(context.Background(), name)
	if err != nil || len(before) != 2 {
		t.Fatal("Synthetic source did not create exactly two services", err)
	}
	checkLiteral := func(c Container) {
		if run("inspect", "--format", `{{range .Config.Env}}{{if eq . "FIXTURE_VALUE=fictional$value"}}matched{{end}}{{end}}`, c.ID) != "matched" {
			t.Fatal("Synthetic runtime environment literal changed")
		}
		if run("inspect", "--format", `{{if eq (index .Config.Labels "io.dockspire.fixture$key") "fictional$label"}}matched{{end}}`, c.ID) != "matched" {
			t.Fatal("Synthetic runtime label key/value literal changed")
		}
	}
	for _, c := range before {
		checkLiteral(c)
	}
	networkID := run("network", "inspect", "--format", "{{.Id}}", networkName)
	volumeCreated := run("volume", "inspect", "--format", "{{.CreatedAt}}", volumeNames[0])
	run("cp", markerPath, name+"-web-1:/appdata/retained-marker")
	// Environment excluded deliberately: imports may have no saved .env.
	library := newLibrary(filepath.Join(root, "library"), sourceRoot, true, source, commandCompose{binary: composeBinary, socket: "/var/run/docker.sock"})
	preview, _, err := library.importPreview(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = library.importStack(context.Background(), name, preview.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(library.root, "stacks", name, ".env")); !os.IsNotExist(err) {
		t.Fatal("excluded environment was unexpectedly copied")
	}
	plan, err := library.reviewAction(context.Background(), name, "", "takeover")
	if err != nil {
		t.Fatal("Real Compose hash/config review failed:", err)
	}
	job, err := library.startJob(context.Background(), name, plan.ID, name, true)
	if err != nil {
		t.Fatal(err)
	}
	wait := func(job StackJob) {
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			current, err := library.job(job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.Status != "running" && !library.isBusy(name) {
				if current.Status != "succeeded" {
					t.Fatal(current.Message)
				}
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("Synthetic takeover job timed out")
	}
	wait(job)
	after, err := source.ProjectContainers(context.Background(), name)
	if err != nil || len(after) != 2 {
		t.Fatal("Takeover service count changed", err)
	}
	record, err := library.load(name)
	if err != nil || !record.managed() {
		t.Fatal("Takeover did not persist verified management", err)
	}
	original := map[string]Container{}
	for _, c := range before {
		original[c.Service] = c
	}
	for _, c := range after {
		checkLiteral(c)
		old := original[c.Service]
		if c.ID == old.ID || c.Name != old.Name || c.ImageID != old.ImageID || !library.ownsContainer(record, c) {
			t.Fatal("Takeover did not preserve names/images and establish new ownership")
		}
	}
	// Real Compose must preserve the literal dollar across private snapshot YAML.
	dir, _ := library.contentDirectory(record)
	canonical, err := library.runCompose(context.Background(), name, dir, "config", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var resolved struct {
		Services map[string]struct{ Environment map[string]string }
	}
	if json.Unmarshal(canonical, &resolved) != nil || resolved.Services["web"].Environment["FIXTURE_VALUE"] != "fictional$$value" {
		t.Fatal("Literal dollar changed during snapshot roundtrip")
	}
	redeploy, err := library.review(context.Background(), name, "")
	if err != nil {
		t.Fatal(err)
	}
	job, err = library.startJob(context.Background(), name, redeploy.ID, name)
	if err != nil {
		t.Fatal(err)
	}
	wait(job)
	rechecked, err := source.ProjectContainers(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rechecked {
		checkLiteral(c)
	}
	if run("network", "inspect", "--format", "{{.Id}}", networkName) != networkID || run("volume", "inspect", "--format", "{{.CreatedAt}}", volumeNames[0]) != volumeCreated {
		t.Fatal("Named resources were replaced")
	}
	markerCopy := filepath.Join(root, "marker-copy")
	run("cp", name+"-web-1:/appdata/retained-marker", markerCopy)
	for _, file := range []string{markerPath, markerCopy} {
		raw, err := os.ReadFile(file)
		if err != nil || string(raw) != string(marker) {
			t.Fatal("Persistent marker changed", err)
		}
	}
	// A synthetic second writer must be rejected, even with the same project.
	seed()
	if _, err := library.review(context.Background(), name, ""); err == nil {
		t.Fatal("A replacement from the former manager was silently authorized")
	}
	t.Log("Synthetic takeover/redeploy passed: real service hashes, literal dollars, names, image IDs, named volume/bind markers, network identity and foreign-writer rejection")
}
