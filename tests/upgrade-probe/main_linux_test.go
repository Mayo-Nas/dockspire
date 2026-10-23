//go:build linux

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

type closeCounter struct{ calls *int }

func (c closeCounter) Close() error { *c.calls = *c.calls + 1; return nil }

func TestInspectOnlyOpensClosesAndOmitsPrivateValues(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "admin.json")
	if err := os.WriteFile(path, []byte("PRIVATE_ADMIN_BYTES_CANARY"), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	closed, opened := 0, 0
	inspect(&output, root, "old", func(name string) (io.Closer, error) {
		if name != path {
			t.Fatal("unexpected open target")
		}
		opened++
		return closeCounter{&closed}, nil
	})
	text := output.String()
	if opened != 1 || closed != 1 || !strings.Contains(text, "target=admin type=regular") || !strings.Contains(text, "mode=0600 stat_errno=0") || !strings.Contains(text, "open_errno=0") {
		t.Fatal("metadata/open-close measurement missing")
	}
	if strings.Contains(text, root) || strings.Contains(text, "CANARY") || len(strings.Split(strings.TrimSpace(text), "\n")) != 4 {
		t.Fatal("private or unexpected output")
	}
}

func TestOpenErrnoOmitsErrorPathsAndContents(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "admin.json"), []byte("PRIVATE_CANARY"), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	inspect(&output, root, "candidate", func(string) (io.Closer, error) {
		return nil, &os.PathError{Op: "open", Path: "PRIVATE_ERROR_PATH_CANARY", Err: syscall.EACCES}
	})
	if !strings.Contains(output.String(), "open_errno=13") || strings.Contains(output.String(), "CANARY") || strings.Contains(output.String(), root) {
		t.Fatal("errno disclosure or missing number")
	}
	if errorNumber(nil) != 0 || errorNumber(syscall.ENOENT) != 2 {
		t.Fatal("errno conversion")
	}
}

func TestNonregularAdministratorIsNotOpened(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("not-opened", filepath.Join(root, "admin.json")); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	inspect(&output, root, "old", func(string) (io.Closer, error) { t.Fatal("symlink was opened"); return nil, nil })
	if !strings.Contains(output.String(), "type=symlink") || !strings.Contains(output.String(), "open_errno=-1") {
		t.Fatal("unsafe target measurement")
	}
}

func TestSymlinkParentIsNotTraversed(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "admin.json"), []byte("PRIVATE_CANARY"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	inspect(&output, link, "old", func(string) (io.Closer, error) { t.Fatal("linked parent was opened"); return nil, nil })
	if !strings.Contains(output.String(), "target=data type=symlink") || !strings.Contains(output.String(), "target=admin type=unknown uid=-1 gid=-1") || !strings.Contains(output.String(), "open_errno=-1") {
		t.Fatal("parent precondition ignored")
	}
}

func TestSecurityStateUsesFilesystemIDsAndFiltersInput(t *testing.T) {
	context := securityState(strings.NewReader("Name:\tPRIVATE_CANARY\nUid:\t1 2 3 4\nGid:\t5 6 7 8\nGroups:\t9 10\nCapEff:\t0000000000000000\nCapBnd:\t00000000ffffffff\nNoNewPrivs:\t1\n"))
	if context.uids != [4]int64{1, 2, 3, 4} || context.gids != [4]int64{5, 6, 7, 8} || !context.statusComplete || context.capEff != "0000000000000000" || context.capBnd != "00000000ffffffff" || context.noNewPrivs != 1 {
		t.Fatal("filtered thread credentials")
	}
	if groupMatch(context, 8) != 1 || groupMatch(context, 9) != 1 || groupMatch(context, 6) != 0 || groupMatch(context, -1) != -1 {
		t.Fatal("filesystem/supplementary group matching")
	}
	context = securityState(strings.NewReader("Uid:\tPRIVATE_CANARY\nGid:\t5 6 7 8\nGroups:\tPRIVATE_CANARY\nCapEff:\tPRIVATE_CANARY\nNoNewPrivs:\t3\n"))
	if context.statusComplete || context.groupsComplete || context.capEff != "unknown" || context.noNewPrivs != -1 || groupMatch(context, 8) != -1 {
		t.Fatal("malformed/unknown credentials accepted")
	}
}

func TestGroupOutputIsBoundedWithCompleteMembership(t *testing.T) {
	var ids []string
	for i := 1000; i < 1200; i++ {
		ids = append(ids, strconv.Itoa(i))
	}
	context := securityState(strings.NewReader("Uid:\t0 0 0 0\nGid:\t0 0 0 0\nGroups:\t" + strings.Join(ids, " ") + "\nCapEff:\t0\nCapBnd:\t0\nNoNewPrivs:\t1\n"))
	output, count, truncated := groupOutput(context)
	if !context.groupsComplete || count != 200 || truncated != 1 || len(strings.Split(output, ",")) != 128 || groupMatch(context, 1199) != 1 {
		t.Fatal("bounded output lost complete membership")
	}
}

func TestSecurityStateRejectsTruncatedInput(t *testing.T) {
	context := securityState(strings.NewReader("Uid:\t0 0 0 0\nGid:\t0 0 0 0\nGroups:\t0\n" + strings.Repeat("x", 70000) + "\nCapEff:\t1234\nNoNewPrivs:\t1\n"))
	output, count, truncated := groupOutput(context)
	if context.statusComplete || context.groupsComplete || output != "unknown" || count != -1 || truncated != 1 || groupMatch(context, 0) != -1 {
		t.Fatal("status bound became false certainty")
	}
}

func TestExecPreservesArgumentsAndMeasuresFirst(t *testing.T) {
	app := []string{"--demo", "--setup", "--listen", ":8787", "--data-dir", "/appdata", "--allow-http"}
	args := append([]string{"--exec", "rollback", "--"}, app...)
	measured := false
	var output bytes.Buffer
	status := run(args, &output, func(label string) { measured = label == "rollback" }, func(binary string, argv, _ []string) error {
		if !measured || binary != "/stacklane" || !reflect.DeepEqual(argv, append([]string{"/stacklane"}, app...)) {
			t.Fatal("execution context/arguments changed")
		}
		return syscall.ENOENT
	})
	if status != 1 || output.String() != "probe exec_errno=2\n" {
		t.Fatal("exec failure not sanitized")
	}
}

func TestInvocationIsClosedAndDoesNotEchoInput(t *testing.T) {
	for _, args := range [][]string{{"--inspect-only", "PRIVATE_CANARY"}, {"--exec", "rollback", "--", "--data-dir", "/other"}} {
		var output bytes.Buffer
		status := run(args, &output, func(string) { t.Fatal("invalid measurement invoked") }, func(string, []string, []string) error { t.Fatal("invalid exec invoked"); return nil })
		if status != 2 || output.String() != "probe invocation invalid\n" {
			t.Fatal("invalid invocation disclosed")
		}
	}
	var output bytes.Buffer
	measured := false
	status := run([]string{"--inspect-only", "candidate"}, &output, func(label string) { measured = label == "candidate" }, func(string, []string, []string) error { t.Fatal("inspection executed application"); return nil })
	if status != 0 || !measured || output.Len() != 0 {
		t.Fatal("inspect-only mode")
	}
}
