package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestComposeRequiredCapabilities(t *testing.T) {
	for _, tc := range []struct {
		help  string
		flags []string
		want  bool
	}{
		{"--wait --wait-timeout int", []string{"--wait", "--wait-timeout"}, true},
		{"--wait-timeout int", []string{"--wait"}, false},
		{"--policy string", []string{"--policy"}, true},
		{"--format string (json|yaml)", []string{"--format", "json"}, false},
		{"--format string (json|yaml)", []string{"--format"}, true},
		{"--formatting string", []string{"--format"}, false},
	} {
		if got := composeHelpSupports([]byte(tc.help), tc.flags...); got != tc.want {
			t.Fatalf("help %q got %v", tc.help, got)
		}
	}
}
func TestComposeProbeBoundsAndDeadline(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	raw, err := probeCompose(ctx, binary, "probe-fixture", "overflow")
	if err == nil || !strings.Contains(err.Error(), "output exceeds") || len(raw) != 0 || ctx.Err() != nil {
		t.Fatalf("overflow cap not independently enforced: %v", err)
	}
	cancel()
	ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	raw, err = probeCompose(ctx, binary, "probe-fixture", "sleep")
	if err == nil || len(raw) != 0 || ctx.Err() != context.DeadlineExceeded {
		t.Fatal("probe deadline not enforced", err)
	}
}

func TestInstallationRejectsVersionValidMissingCapabilities(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"capable", "missing"} {
		target := filepath.Join(t.TempDir(), "compose-probe-"+mode+filepath.Ext(binary))
		if err := os.WriteFile(target, raw, 0700); err != nil {
			t.Fatal(err)
		}
		got := checkComposeBinary(context.Background(), target)
		expected := "ready"
		if mode == "missing" {
			expected = "incompatible"
		}
		if got.State != expected {
			t.Fatalf("version-valid %s executable readiness %s", mode, got.State)
		}
	}
}
