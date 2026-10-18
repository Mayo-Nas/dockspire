package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Re-execute the test binary as a fake Compose producer without a shell or Docker.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "--project-name" && os.Args[2] == "output-fixture" {
		mode := os.Args[len(os.Args)-1]
		if mode == "small" {
			_, _ = io.WriteString(os.Stdout, "{}")
		} else {
			chunk := strings.Repeat("x", 32<<10)
			for i := 0; i <= maxComposeOutput/len(chunk); i++ {
				_, _ = io.WriteString(os.Stdout, chunk)
			}
			if mode == "overflow" {
				time.Sleep(30 * time.Second)
			}
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestComposeOutputLimitAndDiscard(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runner := commandCompose{binary: binary, socket: filepath.Join(t.TempDir(), "unused.sock")}
	for _, tc := range []struct {
		command, mode string
		overflow      bool
		want          string
	}{
		{"config", "small", false, "{}"},
		{"config", "overflow", true, ""},
		{"pull", "discard", false, ""},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			output, err := runner.Run(ctx, "output-fixture", t.TempDir(), tc.command, tc.mode)
			if ctx.Err() != nil {
				t.Fatal("producer was not cancelled promptly", ctx.Err())
			}
			if tc.overflow {
				if err == nil || !strings.Contains(err.Error(), "8 MiB") || len(output) != 0 {
					t.Fatal("missing safe overflow result", err)
				}
			} else if err != nil || string(output) != tc.want {
				t.Fatal("unexpected output result", err)
			}
		})
	}
}

func TestEditValidatesExactEnvironmentBundle(t *testing.T) {
	for _, save := range []bool{false, true} {
		for _, replace := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{false: "validate", true: "save"}[save], map[bool]string{false: "preserve", true: "replace"}[replace]}, "-"), func(t *testing.T) {
				l, _ := coordinationLibrary(t)
				base, _ := l.directory("example")
				for name, value := range map[string]string{".env": "GLOBAL=fixture-one\n", "stack.env": "ASSOCIATED=fixture-two\n"} {
					if err := os.WriteFile(filepath.Join(base, name), []byte(value), 0600); err != nil {
						t.Fatal(err)
					}
				}
				original, err := l.definition("example")
				if err != nil {
					t.Fatal(err)
				}
				before, _ := l.load("example")
				files, _ := l.content(before)
				files["compose.yaml"] = strings.Replace(files["compose.yaml"], "nginx:alpine", "nginx:stable", 1)
				names := []string{"GLOBAL", "ASSOCIATED"}
				if replace {
					files[".env"], files["stack.env"] = "REPLACED=fixture-three\n", "REPLACED=fixture-three\n"
					names = []string{"REPLACED"}
				}
				l.runner = revisionBundleRunner{t, map[string]string{".env": files[".env"], "stack.env": files["stack.env"]}}
				result, err := l.edit(context.Background(), "example", editInput{Compose: strings.Replace(original.Compose, "nginx:alpine", "nginx:stable", 1), Revision: original.Fingerprint, ReplaceEnvironment: replace, Environment: "REPLACED=fixture-three\n"}, save)
				if err != nil || !reflect.DeepEqual(result.EnvironmentNames, names) {
					t.Fatal("wrong environment validation", result.EnvironmentNames, err)
				}
				stored, _ := l.load("example")
				actual, _ := l.content(stored)
				if save {
					if actual[".env"] != files[".env"] || actual["stack.env"] != files["stack.env"] || !strings.Contains(actual["compose.yaml"], "nginx:stable") {
						t.Fatal("saved bytes differ from validated bytes")
					}
				} else if stored.ConfigRevision != before.ConfigRevision || actual["compose.yaml"] == files["compose.yaml"] {
					t.Fatal("validation modified saved configuration")
				}
			})
		}
	}
}

func TestEnvironmentNamesMaximumInput(t *testing.T) {
	raw := strings.Repeat("A=x\n", maxDefinition/4)
	names, err := environmentNames(raw)
	if err != nil || !reflect.DeepEqual(names, []string{"A"}) {
		t.Fatal(names, err)
	}
	if _, err := environmentNames(raw + "\n"); err == nil {
		t.Fatal("oversize environment accepted")
	}
}

func BenchmarkEnvironmentNamesMaximumInput(b *testing.B) {
	raw := strings.Repeat("A=x\n", maxDefinition/4)
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for b.Loop() {
		if _, err := environmentNames(raw); err != nil {
			b.Fatal(err)
		}
	}
}
