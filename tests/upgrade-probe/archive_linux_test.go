//go:build linux

package main

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func checkpointHelpers(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile("../test-upgrade.sh")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(source), "# Preserve the stopped source archive;")
	end := strings.Index(string(source), "# End checkpoint archive helpers.")
	if start < 0 || end <= start {
		t.Fatal("checkpoint helpers unavailable")
	}
	return string(source[start:end])
}

func syntheticCheckpoint(t *testing.T, name string, kind byte) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := tar.NewWriter(&out)
	if err := writer.WriteHeader(&tar.Header{Name: "./", Typeflag: tar.TypeDir, Mode: 0700, Uid: 1234, Gid: 2345}); err != nil {
		t.Fatal(err)
	}
	header := &tar.Header{Name: name, Typeflag: kind, Mode: 0600, Uid: 1234, Gid: 2345}
	if kind == tar.TypeReg {
		header.Size = 7
	} else {
		header.Linkname = "../outside"
	}
	if err := writer.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if kind == tar.TypeReg {
		if _, err := writer.Write([]byte("fixture")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func runCheckpoint(t *testing.T, archive []byte) (string, error) {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"bin", "view"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "source.tar"), archive, 0600); err != nil {
		t.Fatal(err)
	}
	mock := `#!/usr/bin/env bash
set -eu
if [[ "$1" == inspect && "$3" == *Labels* ]]; then printf '%s\n' fixture-owner
elif [[ "$1" == inspect && "$3" == '{{.Image}}' ]]; then printf '%s\n' fixture-image
elif [[ "$1" == cp && "$2" == fixture:/appdata/. && "$3" == - ]]; then cat "$FIXTURE_ROOT/source.tar"
elif [[ "$1" == cp && "$2" == - && "$3" == fixture:/appdata ]]; then cat > "$FIXTURE_ROOT/restored.tar"
else exit 80
fi
`
	if err := os.WriteFile(filepath.Join(root, "bin", "docker"), []byte(mock), 0700); err != nil {
		t.Fatal(err)
	}
	script := "set -euo pipefail\n" + checkpointHelpers(t) + "\nname=fixture; owner=fixture-owner; running_image=fixture-image; container_created=true\ncheckpoint=\"$FIXTURE_ROOT/view\"; checkpoint_archive=\"$FIXTURE_ROOT/checkpoint.tar\"\nsave_checkpoint_archive\nrestore_checkpoint_archive\n"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", "--noprofile", "--norc", "-c", script)
	command.Env = append(os.Environ(), "BASH_ENV=", "ENV=", "FIXTURE_ROOT="+root, "PATH="+filepath.Join(root, "bin")+":"+os.Getenv("PATH"))
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	command.WaitDelay = time.Second
	return root, command.Run()
}

func TestCheckpointPreservesOriginalNumericOwnershipAndModes(t *testing.T) {
	original := syntheticCheckpoint(t, "./admin.json", tar.TypeReg)
	root, err := runCheckpoint(t, original)
	if err != nil {
		t.Fatal("checkpoint helper failed")
	}
	restored, err := os.ReadFile(filepath.Join(root, "restored.tar"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, restored) {
		t.Fatal("restore did not use original archive bytes")
	}
	info, err := os.Stat(filepath.Join(root, "checkpoint.tar"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("checkpoint archive not private")
	}
	reader := tar.NewReader(bytes.NewReader(restored))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == "./admin.json" && (header.Uid != 1234 || header.Gid != 2345 || header.Mode != 0600) {
			t.Fatal("numeric ownership or private mode changed")
		}
	}
}

func TestCheckpointRejectsUnsafeArchiveMembers(t *testing.T) {
	for _, item := range []struct {
		name string
		kind byte
	}{
		{"../outside", tar.TypeReg}, {"/outside", tar.TypeReg}, {"./bad\nname", tar.TypeReg},
		{"./admin.json", tar.TypeSymlink}, {"./admin.json", tar.TypeLink}, {"./appdata/admin.json", tar.TypeReg},
	} {
		t.Run(strings.ReplaceAll(item.name, "/", "_"), func(t *testing.T) {
			root, err := runCheckpoint(t, syntheticCheckpoint(t, item.name, item.kind))
			if err == nil {
				t.Fatal("unsafe archive accepted")
			}
			if _, err := os.Stat(filepath.Join(root, "restored.tar")); !os.IsNotExist(err) {
				t.Fatal("unsafe archive reached restore")
			}
			if _, err := os.Stat(filepath.Join(root, "outside")); !os.IsNotExist(err) {
				t.Fatal("archive escaped view")
			}
		})
	}
}
