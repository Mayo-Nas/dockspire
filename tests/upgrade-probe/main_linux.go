//go:build linux

// This test-only probe never reads administrator bytes. It is not a runtime image component.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

type fileMetadata struct {
	uid, gid int64
	mode     uint32
	kind     string
	errno    int
}

func errorNumber(err error) int {
	if err == nil {
		return 0
	}
	var number syscall.Errno
	if errors.As(err, &number) {
		return int(number)
	}
	return -1
}

func metadata(path string) fileMetadata {
	result := fileMetadata{uid: -1, gid: -1, kind: "unknown"}
	info, err := os.Lstat(path)
	if err != nil {
		result.errno = errorNumber(err)
		return result
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		result.errno = -1
		return result
	}
	result.uid, result.gid, result.mode = int64(stat.Uid), int64(stat.Gid), uint32(info.Mode().Perm())
	switch {
	case info.Mode().IsRegular():
		result.kind = "regular"
	case info.IsDir():
		result.kind = "directory"
	case info.Mode()&os.ModeSymlink != 0:
		result.kind = "symlink"
	default:
		result.kind = "other"
	}
	return result
}

type securityContext struct {
	uids, gids                                [4]int64
	groups                                    []int64
	groupsComplete, statusComplete, truncated bool
	capEff, capBnd                            string
	noNewPrivs                                int
}

func unknownContext() securityContext {
	return securityContext{uids: [4]int64{-1, -1, -1, -1}, gids: [4]int64{-1, -1, -1, -1}, capEff: "unknown", capBnd: "unknown", noNewPrivs: -1}
}

func numericIDs(value string, count int) ([]int64, bool) {
	fields := strings.Fields(value)
	if count >= 0 && len(fields) != count {
		return nil, false
	}
	ids := make([]int64, len(fields))
	for i, field := range fields {
		number, err := strconv.ParseUint(field, 10, 32)
		if err != nil {
			return nil, false
		}
		ids[i] = int64(number)
	}
	return ids, true
}

// Only the caller's thread-status metadata is read; administrator bytes never enter this parser.
func securityState(reader io.Reader) securityContext {
	context := unknownContext()
	raw, err := io.ReadAll(io.LimitReader(reader, 65537))
	context.truncated = err != nil || len(raw) > 65536
	if len(raw) > 65536 {
		raw = raw[:65536]
	}
	uidOK, gidOK, groupsOK := false, false, false
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "Uid":
			if ids, ok := numericIDs(value, 4); ok {
				copy(context.uids[:], ids)
				uidOK = true
			}
		case "Gid":
			if ids, ok := numericIDs(value, 4); ok {
				copy(context.gids[:], ids)
				gidOK = true
			}
		case "Groups":
			if ids, ok := numericIDs(value, -1); ok {
				context.groups = ids
				groupsOK = true
			}
		case "CapEff", "CapBnd":
			if number, err := strconv.ParseUint(value, 16, 64); err == nil {
				if key == "CapEff" {
					context.capEff = fmt.Sprintf("%016x", number)
				} else {
					context.capBnd = fmt.Sprintf("%016x", number)
				}
			}
		case "NoNewPrivs":
			if value == "0" {
				context.noNewPrivs = 0
			} else if value == "1" {
				context.noNewPrivs = 1
			}
		}
	}
	context.groupsComplete = groupsOK && !context.truncated
	context.statusComplete = uidOK && gidOK && context.groupsComplete && context.capEff != "unknown" && context.capBnd != "unknown" && context.noNewPrivs >= 0
	return context
}

func groupMatch(context securityContext, gid int64) int {
	if gid < 0 || context.gids[3] < 0 || !context.groupsComplete {
		return -1
	}
	if context.gids[3] == gid {
		return 1
	}
	for _, group := range context.groups {
		if group == gid {
			return 1
		}
	}
	return 0
}

func groupOutput(context securityContext) (string, int, int) {
	if !context.groupsComplete {
		if context.truncated {
			return "unknown", -1, 1
		}
		return "unknown", -1, 0
	}
	count, limit := len(context.groups), len(context.groups)
	truncated := 0
	if limit > 128 {
		limit, truncated = 128, 1
	}
	values := make([]string, limit)
	for i, group := range context.groups[:limit] {
		values[i] = strconv.FormatInt(group, 10)
	}
	if limit == 0 {
		return "none", 0, 0
	}
	return strings.Join(values, ","), count, truncated
}

// opener returns only Close, so this measurement cannot consume administrator contents.
func inspect(out io.Writer, root, label string, opener func(string) (io.Closer, error)) {
	parent := metadata(root)
	admin := fileMetadata{uid: -1, gid: -1, kind: "unknown", errno: -1}
	if parent.kind == "directory" {
		admin = metadata(filepath.Join(root, "admin.json"))
	}
	for _, target := range []struct {
		name  string
		value fileMetadata
	}{{"data", parent}, {"admin", admin}} {
		v := target.value
		fmt.Fprintf(out, "probe label=%s target=%s type=%s uid=%d gid=%d mode=%04o stat_errno=%d\n", label, target.name, v.kind, v.uid, v.gid, v.mode, v.errno)
	}
	context := unknownContext()
	if status, err := os.Open("/proc/thread-self/status"); err == nil {
		context = securityState(status)
		status.Close()
	}
	openErrno := -1
	if parent.kind == "directory" && admin.kind == "regular" {
		file, err := opener(filepath.Join(root, "admin.json"))
		openErrno = errorNumber(err)
		if err == nil && file != nil {
			file.Close()
		}
	}
	groups, count, truncated := groupOutput(context)
	complete := 0
	if context.groupsComplete {
		complete = 1
	}
	fmt.Fprintf(out, "probe label=%s uid_real=%d uid_effective=%d uid_saved=%d uid_fs=%d gid_real=%d gid_effective=%d gid_saved=%d gid_fs=%d groups=%s groups_count=%d groups_complete=%d groups_truncated=%d parent_group_match=%d admin_group_match=%d\n", label, context.uids[0], context.uids[1], context.uids[2], context.uids[3], context.gids[0], context.gids[1], context.gids[2], context.gids[3], groups, count, complete, truncated, groupMatch(context, parent.gid), groupMatch(context, admin.gid))
	complete = 0
	if context.statusComplete {
		complete = 1
	}
	fmt.Fprintf(out, "probe label=%s cap_eff=%s cap_bnd=%s no_new_privs=%d status_complete=%d open_errno=%d\n", label, context.capEff, context.capBnd, context.noNewPrivs, complete, openErrno)
}

func validAppBinary(binary string) bool {
	if len(binary) < 2 || len(binary) > 129 || binary[0] != '/' || binary == "/upgrade-probe" {
		return false
	}
	for index, character := range binary[1:] {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			continue
		}
		if index > 0 && (character == '.' || character == '_' || character == '-') {
			continue
		}
		return false
	}
	return true
}

func validAppArgs(args []string) bool {
	http := []string{"--demo", "--setup", "--listen", ":8787", "--data-dir", "/appdata", "--allow-http"}
	https := []string{"--demo", "--setup", "--listen", ":9443", "--data-dir", "/appdata", "--https", "--tls-hosts", "127.0.0.1"}
	joined := strings.Join(args, "\x00")
	return joined == strings.Join(http, "\x00") || joined == strings.Join(https, "\x00")
}

func run(args []string, out io.Writer, measure func(string), execute func(string, []string, []string) error) int {
	if len(args) == 2 && args[0] == "--inspect-only" && (args[1] == "old" || args[1] == "candidate") {
		measure(args[1])
		return 0
	}
	if len(args) >= 5 && args[0] == "--exec" && args[1] == "rollback" && validAppBinary(args[2]) && args[3] == "--" && validAppArgs(args[4:]) {
		measure("rollback")
		err := execute(args[2], append([]string{args[2]}, args[4:]...), os.Environ())
		fmt.Fprintf(out, "probe exec_errno=%d\n", errorNumber(err))
		return 1
	}
	fmt.Fprintln(out, "probe invocation invalid")
	return 2
}

func main() {
	// Keep status measurement, Open and Exec on the same OS thread; never change credentials.
	runtime.LockOSThread()
	os.Exit(run(os.Args[1:], os.Stdout, func(label string) {
		inspect(os.Stdout, "/appdata", label, func(path string) (io.Closer, error) { return os.Open(path) })
	}, syscall.Exec))
}
