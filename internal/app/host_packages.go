package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var hostPackageNames = []string{"docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-ce-rootless-extras", "docker-compose-plugin"}
var debVersion = regexp.MustCompile(`^[0-9][A-Za-z0-9.+:~_-]{0,160}$`)

type HostPackage struct {
	Name      string `json:"name"`
	Installed string `json:"installed"`
	Candidate string `json:"candidate"`
	Update    bool   `json:"update"`
	Held      bool   `json:"held"`
	Trusted   bool   `json:"trusted"`
}
type hostCommands interface {
	Run(context.Context, string, ...string) ([]byte, error)
}
type systemHostCommands struct{}
type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 4<<20 {
		return 0, errors.New("Command output exceeded limit")
	}
	return b.Buffer.Write(p)
}
func (systemHostCommands) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	allowed := map[string]string{"dpkg-query": "/usr/bin/dpkg-query", "dpkg": "/usr/bin/dpkg", "apt-cache": "/usr/bin/apt-cache", "apt-mark": "/usr/bin/apt-mark", "apt-get": "/usr/bin/apt-get"}
	binary, ok := allowed[name]
	if !ok {
		return nil, errors.New("Unsupported host operation")
	}
	if name == "apt-get" && !allowedHostAPT(args) {
		return nil, errors.New(hostUpdatesDisabled)
	}
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "LANG=C", "LC_ALL=C", "DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=l"}
	var output boundedOutput
	command.Stdout = &output
	command.Stderr = io.Discard
	err := command.Run()
	return output.Bytes(), err
}

// Only index refreshes and the exact read-only simulation form are permitted.
func allowedHostAPT(args []string) bool {
	if len(args) == 3 && args[0] == "update" && args[1] == "-o" && args[2] == "APT::Update::Error-Mode=any" {
		return true
	}
	prefix := []string{"--simulate", "--assume-yes", "--no-remove", "--no-install-recommends", "--only-upgrade", "-o", "DPkg::Lock::Timeout=30", "install"}
	if len(args) <= len(prefix) {
		return false
	}
	for i, arg := range prefix {
		if args[i] != arg {
			return false
		}
	}
	for _, arg := range args[len(prefix):] {
		name, version, ok := strings.Cut(arg, "=")
		known := false
		for _, candidate := range hostPackageNames {
			known = known || name == candidate
		}
		if !ok || !known || !debVersion.MatchString(version) {
			return false
		}
	}
	return true
}
func supportedHost() (string, error) {
	raw, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "", errors.New("Host OS could not be identified")
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "ID=") {
			id := strings.Trim(strings.TrimPrefix(line, "ID="), "\"")
			if id == "ubuntu" || id == "debian" {
				return id, nil
			}
		}
	}
	return "", errors.New("Host updates currently support Ubuntu and Debian with official Docker CE packages")
}
func parsePackagePolicy(name, installed, policy string) HostPackage {
	result := HostPackage{Name: name, Installed: installed}
	lines := strings.Split(policy, "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "Candidate:" && debVersion.MatchString(fields[1]) {
			result.Candidate = fields[1]
		}
	}
	inCandidate, found, bad := false, false, false
	header := regexp.MustCompile(`^\s*(?:\*\*\*\s+)?([0-9][A-Za-z0-9.+:~_-]*)\s+[0-9]+\s*$`)
	for _, line := range lines {
		if match := header.FindStringSubmatch(line); match != nil {
			inCandidate = match[1] == result.Candidate
			continue
		}
		fields := strings.Fields(line)
		if !inCandidate || len(fields) < 3 || fields[len(fields)-1] != "Packages" {
			continue
		}
		uri, err := url.Parse(fields[1])
		if err != nil || uri.Scheme != "https" || uri.Host != "download.docker.com" || uri.User != nil || !(strings.HasPrefix(uri.Path, "/linux/ubuntu") || strings.HasPrefix(uri.Path, "/linux/debian")) {
			bad = true
		} else {
			found = true
		}
	}
	result.Trusted = found && !bad
	return result
}
func inspectHostPackages(ctx context.Context, commands hostCommands) ([]HostPackage, error) {
	heldRaw, err := commands.Run(ctx, "apt-mark", "showhold")
	if err != nil {
		return nil, errors.New("Cannot inspect package holds")
	}
	held := map[string]bool{}
	for _, name := range strings.Fields(string(heldRaw)) {
		held[name] = true
	}
	result := []HostPackage{}
	for _, name := range hostPackageNames {
		raw, err := commands.Run(ctx, "dpkg-query", "-W", "-f=${db:Status-Status}|${Version}", name)
		installed := ""
		if err == nil {
			status, version, ok := strings.Cut(strings.TrimSpace(string(raw)), "|")
			if ok && status == "installed" && debVersion.MatchString(version) {
				installed = version
			}
		}
		policy, err := commands.Run(ctx, "apt-cache", "policy", name)
		if err != nil {
			return nil, errors.New("Cannot inspect Docker package candidates")
		}
		item := parsePackagePolicy(name, installed, string(policy))
		item.Held = held[name]
		if installed != "" && item.Candidate != "" {
			_, compareErr := commands.Run(ctx, "dpkg", "--compare-versions", item.Candidate, "gt", installed)
			item.Update = compareErr == nil
		}
		result = append(result, item)
	}
	return result, nil
}
func packageChanges(packages []HostPackage, target string) ([]HostPackage, error) {
	if target != "engine" && target != "compose" {
		return nil, errors.New("Choose Docker Engine or Docker Compose")
	}
	changes := []HostPackage{}
	engineInstalled := false
	for _, item := range packages {
		if item.Name == "docker-ce" && item.Installed != "" {
			engineInstalled = true
		}
		if (target == "compose") != (item.Name == "docker-compose-plugin") || item.Installed == "" || !item.Update {
			continue
		}
		if item.Held {
			return nil, fmt.Errorf("%s is held; this helper will not override package holds", item.Name)
		}
		if !item.Trusted {
			return nil, fmt.Errorf("%s candidate is not exclusively from Docker's official HTTPS repository", item.Name)
		}
		changes = append(changes, item)
	}
	if !engineInstalled {
		return nil, errors.New("Updates require an existing official Docker CE installation; other installation methods are view-only")
	}
	if len(changes) == 0 {
		return nil, errors.New("No eligible updates are available for this selection")
	}
	return changes, nil
}
func aptSimulationArgs(changes []HostPackage) []string {
	args := []string{"--simulate", "--assume-yes", "--no-remove", "--no-install-recommends", "--only-upgrade", "-o", "DPkg::Lock::Timeout=30", "install"}
	for _, item := range changes {
		args = append(args, item.Name+"="+item.Candidate)
	}
	return args
}
func validateSimulation(raw []byte, changes []HostPackage) error {
	selected := map[string]string{}
	seen := map[string]bool{}
	for _, item := range changes {
		selected[item.Name] = item.Candidate
	}
	inst := regexp.MustCompile(`^Inst (\S+)(?: \[[^\]]+\])? \((\S+)`)
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "Remv ") || strings.HasPrefix(line, "Purg ") {
			return errors.New("Package simulation would remove packages; update blocked")
		}
		if strings.HasPrefix(line, "Inst ") {
			match := inst.FindStringSubmatch(line)
			if match == nil || selected[match[1]] == "" || selected[match[1]] != match[2] {
				return errors.New("Package simulation includes unreviewed packages or versions; update blocked")
			}
			seen[match[1]] = true
		}
		if strings.HasPrefix(line, "Conf ") {
			fields := strings.Fields(line)
			if len(fields) < 2 || selected[fields[1]] == "" {
				return errors.New("Package simulation would configure an unreviewed package; update blocked")
			}
		}
	}
	if len(seen) != len(selected) {
		return errors.New("Package simulation differs from the reviewed changes; refresh and review again")
	}
	return nil
}
func simulateHostUpdate(ctx context.Context, commands hostCommands, changes []HostPackage) error {
	raw, err := commands.Run(ctx, "apt-get", aptSimulationArgs(changes)...)
	if err != nil {
		return errors.New("APT could not simulate this update. Check package-manager locks or dependency problems on the host; no packages were changed")
	}
	return validateSimulation(raw, changes)
}
