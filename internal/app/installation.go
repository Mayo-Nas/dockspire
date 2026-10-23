package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

type DependencyCheck struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Version string `json:"version,omitempty"`
	Message string `json:"message"`
}
type InstallationStatus struct {
	CheckedAt string            `json:"checkedAt"`
	Mode      string            `json:"mode"`
	Ready     bool              `json:"ready"`
	Checks    []DependencyCheck `json:"checks"`
}

var dependencyVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9._-]+)?$`)

func installationCheck(ctx context.Context, source inventorySource, runner ComposeRunner) InstallationStatus {
	result := InstallationStatus{CheckedAt: time.Now().UTC().Format(time.RFC3339), Mode: "live", Checks: []DependencyCheck{}}
	engine := DependencyCheck{Name: "Docker Engine", State: "unavailable", Message: "Cannot reach Docker. It may be missing, stopped, or inaccessible to this application."}
	if docker, ok := source.(*dockerSource); ok {
		var version struct{ Version, ApiVersion, MinAPIVersion string }
		if docker.get(ctx, "/version", &version) == nil {
			engine.Version = version.Version
			if _, err := negotiateAPI(version.ApiVersion, version.MinAPIVersion); err != nil {
				engine.State, engine.Message = "incompatible", err.Error()
			} else {
				engine.State, engine.Message = "ready", "Docker is reachable and its API is supported"
			}
		}
	} else if _, demo := source.(demoSource); demo {
		result.Mode = "demo"
		engine.State, engine.Message = "demo", "Demo mode does not inspect the host installation"
	}
	compose := DependencyCheck{Name: "Docker Compose", State: "unavailable", Message: "Compose checks are unavailable for this installation"}
	if command, ok := runner.(commandCompose); ok {
		compose = checkComposeBinary(ctx, command.binary)
	}
	if _, demo := runner.(demoCompose); demo {
		compose.State, compose.Message = "demo", "Demo mode does not execute Docker Compose"
	}
	result.Checks = append(result.Checks, engine, compose)
	result.Ready = engine.State == "ready" && compose.State == "ready"
	return result
}
func checkComposeBinary(ctx context.Context, binary string) DependencyCheck {
	result := DependencyCheck{Name: "Docker Compose", State: "unavailable", Message: "Compose could not run; check executable permissions and platform compatibility"}
	info, err := os.Stat(binary)
	if errors.Is(err, os.ErrNotExist) {
		result.State, result.Message = "missing", "Docker Compose executable is missing. Install the modern Compose plugin and configure its path."
		return result
	}
	if err != nil || !info.Mode().IsRegular() {
		return result
	}
	raw, err := probeCompose(ctx, binary, "version", "--short")
	result = composeVersionResult(raw, err)
	if result.State != "ready" {
		return result
	}
	for _, probe := range []struct {
		command string
		flags   []string
	}{
		{"up", []string{"--wait", "--wait-timeout"}},
		{"pull", []string{"--policy"}},
		{"config", []string{"--format"}},
	} {
		help, err := probeCompose(ctx, binary, probe.command, "--help")
		if err != nil || !composeHelpSupports(help, probe.flags...) || (probe.command == "config" && !strings.Contains(strings.ToLower(string(help)), "json")) {
			result.State, result.Message = "incompatible", "Compose lacks required up --wait/--wait-timeout, pull --policy, or config --format json support; install a current Compose plugin"
			return result
		}
	}
	result.Message = "Compose supports the deployment and JSON validation options required by Dockspire"
	return result
}
func composeVersionResult(raw []byte, err error) DependencyCheck {
	result := DependencyCheck{Name: "Docker Compose", State: "unavailable", Message: "Compose could not run; check executable permissions and platform compatibility"}
	version := strings.TrimSpace(string(raw))
	if err != nil || len(raw) > 128 || !dependencyVersion.MatchString(version) {
		return result
	}
	if strings.HasPrefix(strings.TrimPrefix(version, "v"), "1.") {
		result.State, result.Version, result.Message = "incompatible", version, "Legacy Compose v1 is unsupported; install the modern Docker Compose plugin"
		return result
	}
	result.State, result.Version, result.Message = "ready", strings.TrimPrefix(version, "v"), "Compose executable is available"
	return result
}
func registerInstallationRoutes(mux *http.ServeMux, library *stackLibrary, source inventorySource) {
	mux.HandleFunc("GET /api/system/installation", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		var runner ComposeRunner
		if library != nil {
			runner = library.runner
		}
		writeJSON(w, 200, installationCheck(ctx, source, runner))
	})
}
