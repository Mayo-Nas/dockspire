package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"
)

const maxDefinition = 512 << 10

// Bound expanded configuration separately from the smaller saved input files.
const maxComposeOutput = 8 << 20

var projectName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
var portainerFile = regexp.MustCompile(`^/data/compose/([0-9]+)/docker-compose\.ya?ml$`)

type StackRecord struct {
	Takeover                  *TakeoverState     `json:"takeover,omitempty"`
	Name                      string             `json:"name"`
	Origin                    string             `json:"origin"`
	CreatedAt                 string             `json:"createdAt"`
	DeployedAt                string             `json:"deployedAt,omitempty"`
	SourceDirectory           string             `json:"sourceDirectory,omitempty"`
	Fingerprint               string             `json:"fingerprint"`
	EnvironmentNames          []string           `json:"environmentNames"`
	Warnings                  []string           `json:"warnings"`
	Services                  []ServiceSummary   `json:"services"`
	InitialFingerprintVersion int                `json:"initialFingerprintVersion,omitempty"`
	ConfigRevision            string             `json:"configRevision,omitempty"`
	AppliedRevision           string             `json:"appliedRevision,omitempty"`
	UpdatedAt                 string             `json:"updatedAt,omitempty"`
	LastDeploymentAttempt     *DeploymentAttempt `json:"lastDeploymentAttempt,omitempty"`
	Unavailable               bool               `json:"unavailable,omitempty"`
	EditVersion               uint64             `json:"editVersion,omitempty"`
}

// An attempt is recorded before Compose may change containers. It is not proof of success.
type DeploymentAttempt struct {
	OperationID string `json:"operationId"`
	Revision    string `json:"revision"`
	StartedAt   string `json:"startedAt"`
}
type ServiceSummary struct {
	Name  string `json:"name"`
	Image string `json:"image"`
}
type StackDefinition struct {
	StackRecord
	Compose          string `json:"compose"`
	RecoveryRequired bool   `json:"recoveryRequired,omitempty"`
	UnappliedChanges bool   `json:"unappliedChanges,omitempty"`
}
type stackInput struct {
	Name        string `json:"name"`
	Compose     string `json:"compose"`
	Environment string `json:"environment"`
}
type ComposeRunner interface {
	Run(context.Context, string, string, ...string) ([]byte, error)
}
type commandCompose struct{ binary, socket string }
type demoCompose struct{}

func (demoCompose) Run(_ context.Context, _ string, _ string, args ...string) ([]byte, error) {
	if len(args) > 0 && args[0] == "config" {
		return nil, nil
	}
	return nil, errors.New("Demo mode does not deploy containers")
}
func (c commandCompose) Run(ctx context.Context, name, directory string, args ...string) ([]byte, error) {
	environmentFile := filepath.Join(directory, ".env")
	if _, err := os.Lstat(environmentFile); err != nil {
		if os.IsNotExist(err) {
			environmentFile = os.DevNull
		} else {
			return nil, errors.New("Cannot inspect the saved environment file")
		}
	}
	commandArgs := []string{"--project-name", name, "--project-directory", directory, "--env-file", environmentFile, "-f", filepath.Join(directory, "compose.yaml")}
	commandArgs = append(commandArgs, args...)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.binary, commandArgs...)
	cmd.Dir = directory
	cmd.Env = []string{"DOCKER_HOST=unix://" + c.socket, "DOCKER_CONFIG=/nonexistent", "HOME=/nonexistent", "PATH=/usr/bin:/bin", "COMPOSE_DISABLE_ENV_FILE=1"}
	stdout := composeOutput{cancel: cancel}
	cmd.Stdout = io.Discard
	if len(args) > 0 && args[0] == "config" {
		cmd.Stdout = &stdout
	}
	// Compose diagnostics can contain interpolated secrets; do not return them to browsers or logs.
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if stdout.overflow {
		return nil, errors.New("Expanded Compose configuration exceeds the 8 MiB limit; reduce repeated or interpolated values")
	}
	if err != nil {
		return nil, errors.New("Compose could not complete this operation. Check the definition, environment values, image availability, and port conflicts")
	}
	return stdout.buffer.Bytes(), nil
}

// The exec stdout copier is the only writer; Run reads the result after Wait.
// Cancel immediately on overflow, rather than waiting for a producer to close stdout.
type composeOutput struct {
	buffer   bytes.Buffer
	cancel   context.CancelFunc
	overflow bool
}

func (w *composeOutput) Write(p []byte) (int, error) {
	if len(p) > maxComposeOutput-w.buffer.Len() {
		w.overflow = true
		w.cancel()
		return 0, errors.New("Compose output limit exceeded")
	}
	return w.buffer.Write(p)
}

type stackLibrary struct {
	root, portainerRoot  string
	allowImport          bool
	includeEnvironment   bool
	source               inventorySource
	runner               ComposeRunner
	mu                   sync.Mutex
	jobsMu               sync.Mutex
	jobs                 map[string]StackJob
	plans                map[string]UpdatePlan
	active               map[string]string
	composeSlot          chan struct{}
	archiveSlot          chan struct{}
	historyIndex         map[string][]historyRef
	historyStamp         time.Time
	historyDamaged       bool
	historyKeep          int
	historyCleanupFailed map[string]bool
	host                 *hostHelperClient
	takeoverHostname     func() (string, error)
}

func newLibrary(root, portainerRoot string, allow bool, source inventorySource, runner ComposeRunner) *stackLibrary {
	if absolute, err := filepath.Abs(root); err == nil {
		root = absolute
	}
	return &stackLibrary{root: root, portainerRoot: portainerRoot, allowImport: allow, source: source, runner: runner, jobs: map[string]StackJob{}, plans: map[string]UpdatePlan{}, active: map[string]string{}, composeSlot: make(chan struct{}, 1), archiveSlot: make(chan struct{}, 1), historyKeep: 200, historyCleanupFailed: map[string]bool{}}
}
func (l *stackLibrary) directory(name string) (string, error) {
	if !projectName.MatchString(name) {
		return "", errors.New("Use 1–63 lowercase letters, digits, underscores, or dashes; start with a letter or digit")
	}
	return filepath.Join(l.root, "stacks", name), nil
}
func readLimited(filename string) ([]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxDefinition {
		return nil, errors.New("Definition must be a regular file smaller than 512 KiB")
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, maxDefinition+1))
}
func (l *stackLibrary) load(name string) (StackRecord, error) {
	var record StackRecord
	dir, err := l.directory(name)
	if err != nil {
		return record, err
	}
	data, err := readLimited(filepath.Join(dir, "record.json"))
	if err != nil {
		return record, errors.New("Saved stack not found")
	}
	if json.Unmarshal(data, &record) != nil || record.Name != name {
		return record, errors.New("Saved stack metadata is invalid")
	}
	if !validTakeover(record.Takeover) || (record.Takeover != nil && (record.Origin != "portainer" || record.LastDeploymentAttempt == nil)) {
		return StackRecord{}, errors.New("Saved takeover metadata is invalid")
	}
	if record.Unavailable || (record.InitialFingerprintVersion != 0 && record.InitialFingerprintVersion != 2) || (record.ConfigRevision != "" && !revisionName.MatchString(record.ConfigRevision)) {
		return StackRecord{}, errors.New("Saved stack metadata is invalid")
	}
	if attempt := record.LastDeploymentAttempt; attempt != nil {
		if !validOperationID(attempt.OperationID) || !revisionName.MatchString(attempt.Revision) {
			return StackRecord{}, errors.New("Saved deployment attempt is invalid")
		}
		if _, err := time.Parse(time.RFC3339Nano, attempt.StartedAt); err != nil {
			return StackRecord{}, errors.New("Saved deployment attempt is invalid")
		}
	}
	return record, nil
}
func (l *stackLibrary) list() ([]StackRecord, error) {
	result := []StackRecord{}
	entries, err := os.ReadDir(filepath.Join(l.root, "stacks"))
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return nil, errors.New("Cannot read stack library")
	}
	for _, entry := range entries {
		if !entry.IsDir() || !projectName.MatchString(entry.Name()) {
			continue
		}
		record, err := l.load(entry.Name())
		if err != nil {
			record = StackRecord{Name: entry.Name(), Unavailable: true, Warnings: []string{"Saved stack metadata is unavailable. Restore this stack from a backup before changing it."}}
		}
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}
func (l *stackLibrary) definition(name string) (StackDefinition, error) {
	record, err := l.load(name)
	if err != nil {
		return StackDefinition{}, err
	}
	dir, err := l.contentDirectory(record)
	if err != nil {
		return StackDefinition{}, err
	}
	raw, err := readLimited(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		return StackDefinition{}, errors.New("Cannot read saved definition")
	}
	_, redacted, _, err := inspectCompose(string(raw), false)
	if err != nil {
		return StackDefinition{}, err
	}
	record.Fingerprint, err = l.contentFingerprint(record)
	if err != nil {
		return StackDefinition{}, err
	}
	files, err := l.content(record)
	if err != nil {
		return StackDefinition{}, err
	}
	return StackDefinition{StackRecord: record, Compose: redacted, RecoveryRequired: l.needsRecovery(record), UnappliedChanges: record.DeployedAt != "" && !l.appliedMatches(record, files)}, nil
}
func fingerprint(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
func environmentNames(raw string) ([]string, error) {
	if len(raw) > maxDefinition {
		return nil, errors.New("Environment file is too large")
	}
	seen := map[string]bool{}
	names := []string{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, _, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !comparisonEnvName.MatchString(key) {
			return nil, errors.New("Use one NAME=value environment entry per line")
		}
		if !seen[key] {
			seen[key] = true
			names = append(names, key)
		}
	}
	sort.Strings(names)
	return names, nil
}
func inspectCompose(raw string, newStack bool) ([]ServiceSummary, string, []string, error) {
	if len(raw) == 0 || len(raw) > maxDefinition {
		return nil, "", nil, errors.New("Paste a Compose definition smaller than 512 KiB")
	}
	var model map[string]any
	decoder := yaml.NewDecoder(strings.NewReader(raw))
	if err := decoder.Decode(&model); err != nil {
		location := regexp.MustCompile(`line [0-9]+`).FindString(err.Error())
		if location != "" {
			location = " near " + location
		}
		return nil, "", nil, fmt.Errorf("Compose YAML is invalid%s; check indentation, quoting, and duplicate keys", location)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, "", nil, errors.New("Use one Compose document")
	}
	services, ok := model["services"].(map[string]any)
	if !ok || len(services) == 0 {
		return nil, "", nil, errors.New("Define at least one service under services:")
	}
	warnings := []string{}
	summary := []ServiceSummary{}
	if _, ok := model["include"]; ok {
		if newStack {
			return nil, "", nil, errors.New("Compose include files need file-upload support; use a self-contained definition for now")
		}
		warnings = append(warnings, "Includes need review before takeover")
	}
	for name, value := range services {
		if !projectName.MatchString(name) {
			return nil, "", nil, errors.New("Service names must use lowercase letters, digits, underscores, or dashes, starting with a letter or digit (63 characters maximum)")
		}
		service, ok := value.(map[string]any)
		if !ok {
			return nil, "", nil, errors.New("Each service must contain a mapping")
		}
		image, _ := service["image"].(string)
		summary = append(summary, ServiceSummary{Name: name, Image: image})
		if newStack {
			if image == "" {
				return nil, "", nil, errors.New("Each new service needs an image; building images is not supported yet")
			}
			for _, key := range []string{"build", "extends", "configs", "secrets", "develop"} {
				if _, present := service[key]; present {
					return nil, "", nil, fmt.Errorf("New stacks do not support %s files or operations yet", key)
				}
			}
			if files, present := service["env_file"]; present {
				if !supportedEnvFiles(files) {
					return nil, "", nil, errors.New("Use .env or stack.env for service env_file; other attachments are not supported yet")
				}
			}
			if volumes, ok := service["volumes"].([]any); ok {
				for _, v := range volumes {
					source := ""
					bindMount := false
					switch volume := v.(type) {
					case string:
						if strings.Contains(volume, ":") {
							source = strings.SplitN(volume, ":", 2)[0]
							bindMount = strings.HasPrefix(source, ".") || strings.Contains(source, "/")
						}
					case map[string]any:
						if volume["type"] == "bind" {
							source, _ = volume["source"].(string)
							bindMount = true
						}
					}
					if bindMount && !path.IsAbs(source) {
						return nil, "", nil, errors.New("Use named volumes or absolute Linux host paths for bind mounts")
					}
				}
			}
		}
		if _, ok := service["volumes"]; ok && !newStack {
			warnings = append(warnings, "Review host paths and persistent storage before takeover")
		}
	}
	if newStack {
		for _, key := range []string{"configs", "secrets"} {
			if _, present := model[key]; present {
				return nil, "", nil, fmt.Errorf("New stacks do not support external %s attachments yet", key)
			}
		}
	}
	sort.Slice(summary, func(i, j int) bool { return summary[i].Name < summary[j].Name })
	redactCompose(model)
	redacted, err := composeSource(raw, model)
	if err != nil {
		return nil, "", nil, errors.New("Cannot prepare definition preview")
	}
	return summary, redacted, warnings, nil
}
func supportedEnvFiles(value any) bool {
	switch v := value.(type) {
	case string:
		return v == ".env" || v == "stack.env"
	case []any:
		for _, item := range v {
			if !supportedEnvFiles(item) {
				return false
			}
		}
		return true
	case map[string]any:
		return supportedEnvFiles(v["path"])
	}
	return false
}
func redactCompose(value any) {
	switch current := value.(type) {
	case map[string]any:
		for key, item := range current {
			if strings.EqualFold(key, "environment") {
				switch env := item.(type) {
				case map[string]any:
					for name := range env {
						env[name] = "[hidden]"
					}
				case []any:
					for i, entry := range env {
						if text, ok := entry.(string); ok {
							name, _, _ := strings.Cut(text, "=")
							env[i] = name + "=[hidden]"
						}
					}
				}
				continue
			}
			lower := strings.ToLower(key)
			if strings.Contains(lower, "password") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "private_key") {
				if _, scalar := item.(string); scalar {
					current[key] = "[hidden]"
					continue
				}
			}
			redactCompose(item)
		}
	case []any:
		for _, item := range current {
			redactCompose(item)
		}
	}
}
func (l *stackLibrary) validate(ctx context.Context, input stackInput) (StackRecord, error) {
	return l.validateBundle(ctx, input.Name, map[string]string{"compose.yaml": input.Compose, ".env": input.Environment, "stack.env": input.Environment})
}

// Validate the exact saved bytes, including distinct associated environment files.
func (l *stackLibrary) validateBundle(ctx context.Context, name string, files map[string]string) (StackRecord, error) {
	if _, err := l.directory(name); err != nil {
		return StackRecord{}, err
	}
	services, _, warnings, err := inspectCompose(files["compose.yaml"], true)
	if err != nil {
		return StackRecord{}, err
	}
	names, err := environmentNames(files[".env"])
	if err != nil {
		return StackRecord{}, err
	}
	associated, err := environmentNames(files["stack.env"])
	if err != nil {
		return StackRecord{}, err
	}
	seen := map[string]bool{}
	for _, key := range names {
		seen[key] = true
	}
	for _, key := range associated {
		if !seen[key] {
			names = append(names, key)
			seen[key] = true
		}
	}
	if err = os.MkdirAll(filepath.Join(l.root, "tmp"), 0700); err != nil {
		return StackRecord{}, errors.New("Cannot prepare validation storage")
	}
	dir, err := os.MkdirTemp(filepath.Join(l.root, "tmp"), "validate-")
	if err != nil {
		return StackRecord{}, errors.New("Cannot create validation directory")
	}
	defer os.RemoveAll(dir)
	for name, body := range files {
		if name != "compose.yaml" && name != ".env" && name != "stack.env" {
			return StackRecord{}, errors.New("Unsupported configuration file")
		}
		if err = os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			return StackRecord{}, errors.New("Cannot store validation files")
		}
	}
	if l.runner == nil {
		return StackRecord{}, errors.New("Compose validation is unavailable on this host")
	}
	canonical, runErr := l.runCompose(ctx, name, dir, "config", "--format", "json")
	if runErr != nil {
		return StackRecord{}, runErr
	}
	if len(canonical) > 0 {
		var resolved struct {
			Services map[string]struct {
				Volumes []struct{ Type, Source string }
			}
		}
		if json.Unmarshal(canonical, &resolved) != nil {
			return StackRecord{}, errors.New("Compose returned an invalid validation response")
		}
		for _, service := range resolved.Services {
			for _, mount := range service.Volumes {
				source := filepath.ToSlash(mount.Source)
				root := filepath.ToSlash(l.root)
				if mount.Type == "bind" && (source == root || strings.HasPrefix(source, strings.TrimSuffix(root, "/")+"/")) {
					return StackRecord{}, errors.New("A bind mount resolves inside application storage. Use an absolute path on the Docker host")
				}
			}
		}
	}
	return StackRecord{InitialFingerprintVersion: 2, Name: name, Origin: "new", CreatedAt: time.Now().UTC().Format(time.RFC3339), Fingerprint: filesFingerprint(files), EnvironmentNames: names, Warnings: warnings, Services: services}, nil
}
func (l *stackLibrary) create(ctx context.Context, input stackInput) (StackRecord, error) {
	dir, err := l.directory(input.Name)
	if err != nil {
		return StackRecord{}, err
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		return StackRecord{}, errors.New("A saved stack already uses this name")
	}
	containers, err := l.source.ProjectContainers(ctx, input.Name)
	if err != nil {
		return StackRecord{}, err
	}
	if len(containers) > 0 {
		return StackRecord{}, errors.New("A Docker project already uses this name; import it instead")
	}
	record, err := l.validate(ctx, input)
	if err != nil {
		return StackRecord{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return StackRecord{}, err
	}
	if err = l.saveNew(record, input.Compose, map[string]string{".env": input.Environment, "stack.env": input.Environment}); err != nil {
		return StackRecord{}, err
	}
	return record, nil
}
func (l *stackLibrary) saveNew(record StackRecord, compose string, files map[string]string) error {
	dir, err := l.directory(record.Name)
	if err != nil {
		return err
	}
	if err = ensurePrivateDirectory(filepath.Dir(dir)); err != nil {
		return errors.New("Cannot create stack library")
	}
	if _, err = os.Lstat(dir); !os.IsNotExist(err) {
		return errors.New("Stack already exists or storage is unavailable")
	}
	temp, err := os.MkdirTemp(filepath.Dir(dir), ".stack-")
	if err != nil {
		return errors.New("Cannot stage stack files")
	}
	defer os.RemoveAll(temp)
	bundle := map[string]string{"compose.yaml": compose}
	for name, body := range files {
		if name != ".env" && name != "stack.env" {
			return errors.New("Unsupported imported filename")
		}
		bundle[name] = body
	}
	record.InitialFingerprintVersion = 2
	metadata, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return errors.New("Cannot prepare stack metadata")
	}
	bundle["record.json"] = string(metadata)
	for name, body := range bundle {
		if err = writePrivateFile(filepath.Join(temp, name), []byte(body)); err != nil {
			return errors.New("Cannot save stack files")
		}
	}
	if err = syncDirectory(temp); err != nil {
		return errors.New("Cannot sync staged stack files")
	}
	if err = os.Rename(temp, dir); err != nil {
		return errors.New("Cannot publish stack files")
	}
	if err = syncDirectory(filepath.Dir(dir)); err != nil {
		return errors.New("Stack files were published, but durable storage could not be confirmed. Reopen before retrying")
	}
	return nil
}
func (l *stackLibrary) importPreview(ctx context.Context, name string) (StackDefinition, map[string]string, error) {
	if !l.allowImport || l.portainerRoot == "" {
		return StackDefinition{}, nil, errors.New("Portainer source access has not been enabled by the operator")
	}
	if _, err := l.directory(name); err != nil {
		return StackDefinition{}, nil, err
	}
	containers, err := l.source.ProjectContainers(ctx, name)
	if err != nil {
		return StackDefinition{}, nil, err
	}
	var sourceID, sourceFile, originalDirectory string
	for _, container := range containers {
		if container.Stack != name {
			continue
		}
		match := portainerFile.FindStringSubmatch(container.ComposeFiles)
		if len(match) == 2 {
			if sourceID != "" && (sourceID != match[1] || sourceFile != path.Base(match[0])) {
				return StackDefinition{}, nil, errors.New("Project contains conflicting source locations")
			}
			sourceID = match[1]
			sourceFile = path.Base(match[0])
			originalDirectory = container.ComposeDirectory
		}
	}
	if sourceID == "" {
		return StackDefinition{}, nil, errors.New("No supported Portainer Compose source was found for this project")
	}
	dir := filepath.Join(l.portainerRoot, sourceID)
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return StackDefinition{}, nil, errors.New("Portainer source directory is unavailable")
	}
	raw, err := readLimited(filepath.Join(dir, sourceFile))
	if err != nil {
		return StackDefinition{}, nil, errors.New("Cannot read the Portainer Compose definition")
	}
	services, redacted, warnings, err := inspectCompose(string(raw), false)
	if err != nil {
		return StackDefinition{}, nil, err
	}
	files := map[string]string{}
	names := []string{}
	environmentFiles := []string{}
	if l.includeEnvironment {
		environmentFiles = []string{".env", "stack.env"}
	} else {
		warnings = append(warnings, "Associated environment files were excluded by the operator")
	}
	for _, file := range environmentFiles {
		body, readErr := readLimited(filepath.Join(dir, file))
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return StackDefinition{}, nil, errors.New("Cannot safely read a related environment file")
		}
		files[file] = string(body)
		keys, parseErr := environmentNames(string(body))
		if parseErr != nil {
			warnings = append(warnings, "Environment file syntax needs review")
		}
		names = append(names, keys...)
	}
	warnings = append(warnings, "Import copies configuration only. Portainer remains responsible until a separate takeover review is explicitly confirmed and completes successfully")
	warnings = append(warnings, "Only this Compose file and authorized .env / stack.env files are copied. Other referenced files need review")
	if len(files) == 0 {
		warnings = append(warnings, "No environment files were copied. Values stored only in Portainer's database are not imported")
	}
	files["compose.yaml"] = string(raw)
	record := StackRecord{InitialFingerprintVersion: 2, Name: name, Origin: "portainer", CreatedAt: time.Now().UTC().Format(time.RFC3339), SourceDirectory: originalDirectory, Fingerprint: filesFingerprint(files), EnvironmentNames: names, Warnings: warnings, Services: services}
	files["compose.yaml"] = string(raw)
	return StackDefinition{StackRecord: record, Compose: redacted}, files, nil
}
func (l *stackLibrary) importStack(ctx context.Context, name, expectedFingerprint string) (StackRecord, error) {
	preview, files, err := l.importPreview(ctx, name)
	if err != nil {
		return StackRecord{}, err
	}
	if expectedFingerprint == "" || preview.Fingerprint != expectedFingerprint {
		return StackRecord{}, errors.New("The source changed; preview it again before importing")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return StackRecord{}, err
	}
	raw := files["compose.yaml"]
	delete(files, "compose.yaml")
	if err = l.saveNew(preview.StackRecord, raw, files); err != nil {
		return StackRecord{}, err
	}
	return preview.StackRecord, nil
}
