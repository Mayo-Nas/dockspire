package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (l *stackLibrary) buildTakeoverPlan(ctx context.Context, name string) (UpdatePlan, error) {
	var plan UpdatePlan
	_, err := l.withCompose(ctx, func() ([]byte, error) { var err error; plan, err = l.prepareTakeover(ctx, name); return nil, err })
	return plan, err
}

// Called only while holding the Compose slot; config is read-only and its
// resolved values remain private transient data, never API review fields.
func (l *stackLibrary) prepareTakeover(ctx context.Context, name string) (UpdatePlan, error) {
	plan := UpdatePlan{Name: name, Kind: "takeover", Targets: []string{}}
	record, err := l.load(name)
	if err != nil {
		return plan, err
	}
	if record.Origin != "portainer" || record.managed() {
		return plan, errors.New("Takeover requires an imported Portainer configuration that is not already managed")
	}
	inspector, ok := l.source.(takeoverInspector)
	if !ok {
		return plan, errors.New("Takeover resource inspection is unavailable on this connection")
	}
	initial := record
	initial.ConfigRevision = ""
	files, err := l.safeRevisionContent(initial)
	if err != nil {
		return plan, err
	}
	sourceFingerprint := ""
	if record.Takeover == nil {
		preview, _, readErr := l.importPreview(ctx, name)
		if readErr != nil {
			return plan, readErr
		}
		sourceFingerprint = preview.Fingerprint
	} else {
		source, readErr := l.takeoverSourceBundle(record.Takeover.SourceFile)
		if readErr != nil {
			return plan, readErr
		}
		sourceFingerprint = filesFingerprint(source)
		if sourceFingerprint != record.Takeover.SourceFingerprint {
			return plan, errors.New("The authorized Portainer source changed during takeover; inspect both managers before recovery")
		}
	}
	if sourceFingerprint != filesFingerprint(files) {
		return plan, errors.New("The Portainer source or authorized environment inputs changed; keep the running workload unchanged and review a fresh copy")
	}
	if err = takeoverShape(files["compose.yaml"]); err != nil {
		return plan, err
	}
	items, err := l.source.ProjectContainers(ctx, name)
	if err != nil {
		return plan, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Service < items[j].Service })
	if len(items) > 100 {
		return plan, errors.New("Takeover supports at most 100 services")
	}
	hostname := l.takeoverHostname
	if hostname == nil {
		hostname = os.Hostname
	}
	selfHost, err := hostname()
	if err != nil || selfHost == "" {
		return plan, errors.New("Cannot establish Dockspire's host identity; takeover is blocked")
	}
	captureBytes := 0
	live := []TakeoverContainer{}
	identities := []string{}
	seen := map[string]bool{}
	for _, item := range items {
		if item.Stack != name || strings.EqualFold(item.ComposeOneOff, "true") || !projectName.MatchString(item.Service) || seen[item.Service] {
			return plan, errors.New("Takeover requires one container per service, with no one-offs, replicas or mixed projects")
		}
		if item.State != "running" && item.State != "exited" && item.State != "created" {
			return plan, errors.New("A container is transitioning; inspect it before takeover")
		}
		seen[item.Service] = true
		c, err := inspector.TakeoverContainer(ctx, item.ID)
		if err != nil {
			return plan, err
		}
		if c.Hostname == selfHost || (len(selfHost) >= 12 && strings.HasPrefix(c.ID, selfHost)) {
			return plan, errors.New("Dockspire cannot take over the project containing its own running manager; use a separate manager instance")
		}
		if c.ID != item.ID || c.Service != item.Service || c.Name != item.Name || c.Image != item.ImageID {
			return plan, errors.New("Container identity changed during resource inspection")
		}
		live = append(live, c)
		plan.Targets = append(plan.Targets, c.Name)
		raw, _ := json.Marshal(c)
		captureBytes += len(raw)
		if captureBytes > maxDefinition/2 {
			return plan, errors.New("Takeover resource metadata exceeds the safe capture limit")
		}
		identities = append(identities, containerIdentity(item)+fingerprint(string(raw)))
	}
	if record.Takeover != nil {
		if record.Takeover.State != "pending" || record.ConfigRevision != record.Takeover.Revision {
			return plan, errors.New("Takeover recovery metadata is inconsistent; restore from a verified backup")
		}
		plan.takeover = record.Takeover
		plan.takeoverFiles, err = l.safeRevisionContent(record)
		if err != nil {
			return plan, err
		}
		if filesFingerprint(plan.takeoverFiles) != record.Takeover.Revision {
			return plan, errors.New("The selected takeover revision is damaged")
		}
		if err = l.checkTakeoverInventory(record, items, live, false); err != nil {
			return plan, err
		}
	} else {
		if len(live) == 0 || len(live) > 100 {
			return plan, errors.New("Takeover supports 1–100 existing services")
		}
		dir, _ := l.directory(name)
		hashes, err := l.runner.Run(ctx, name, dir, "config", "--hash", "*")
		if err != nil {
			return plan, err
		}
		byService := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(string(hashes)), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 || !projectName.MatchString(fields[0]) || !revisionName.MatchString(fields[1]) || byService[fields[0]] != "" {
				return plan, errors.New("Compose returned unsupported service hashes; use a compatible Compose version")
			}
			byService[fields[0]] = fields[1]
		}
		if len(byService) != len(live) {
			return plan, errors.New("The saved and live service sets differ; profiles, missing services and orphans are not supported for takeover")
		}
		sourceFile := ""
		for i, c := range live {
			item := items[i]
			if item.ComposeDirectory != record.SourceDirectory || !portainerFile.MatchString(item.ComposeFiles) || (sourceFile != "" && item.ComposeFiles != sourceFile) {
				return plan, errors.New("The project contains containers from conflicting source locations")
			}
			sourceFile = item.ComposeFiles
			if byService[c.Service] != c.ConfigHash {
				return plan, errors.New("Saved configuration does not match the live service hash. Missing Portainer-only environment values, changed configuration or a different Compose version must be resolved before takeover")
			}
		}
		canonical, err := l.runner.Run(ctx, name, dir, "config", "--format", "json")
		if err != nil {
			return plan, err
		}
		plan.takeoverFiles, err = takeoverSnapshot(canonical, live)
		if err != nil {
			return plan, err
		}
		plan.takeover = &TakeoverState{State: "pending", SourceFile: sourceFile, SourceFingerprint: sourceFingerprint, Revision: filesFingerprint(plan.takeoverFiles), Containers: live}
		if !validTakeover(plan.takeover) {
			return plan, errors.New("The captured takeover metadata is unsupported")
		}
	}
	plan.Revision = plan.takeover.Revision
	plan.Targets = []string{}
	for _, container := range plan.takeover.Containers {
		plan.Targets = append(plan.Targets, container.Name)
	}
	plan.Services, _, _, err = inspectCompose(plan.takeoverFiles["compose.yaml"], true)
	if err != nil {
		return plan, err
	}
	plan.recordVersion = recordStamp(record)
	raw, _ := json.Marshal(identities)
	plan.stamp = fingerprint(plan.recordVersion + "\x00" + plan.Revision + "\x00" + string(raw))
	return plan, nil
}

func (l *stackLibrary) checkTakeoverInventory(record StackRecord, items []Container, live []TakeoverContainer, complete bool) error {
	expected := map[string]TakeoverContainer{}
	for _, c := range record.Takeover.Containers {
		expected[c.Service] = c
	}
	if complete && len(items) != len(expected) {
		return errors.New("Takeover is incomplete: inspect missing or extra services before recovery")
	}
	for i, item := range items {
		before, ok := expected[item.Service]
		if !ok || takeoverResourceStamp(before) != takeoverResourceStamp(live[i]) {
			return errors.New("Takeover resource identities changed; no further recreation was attempted")
		}
		owned := l.ownsContainer(record, item)
		original := item.ID == before.ID && item.ComposeFiles == record.Takeover.SourceFile && item.ComposeDirectory == record.SourceDirectory && live[i].ConfigHash == before.ConfigHash
		if complete {
			if !owned || item.State != "running" {
				return errors.New("Compose finished without verified Dockspire ownership and running services; inspect takeover recovery")
			}
		} else if !owned && !original {
			return errors.New("Another manager replaced a container; takeover recovery was blocked")
		}
	}
	return nil
}

// Publish an immutable private revision before recording intent. No record is
// changed by review, and existing revision bytes must match their identity.
func (l *stackLibrary) storeTakeoverRevision(record StackRecord, files map[string]string) error {
	dir, err := l.contentDirectory(record)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(dir); err == nil {
		existing, readErr := l.safeRevisionContent(record)
		if readErr != nil {
			return readErr
		}
		if filesFingerprint(existing) != record.ConfigRevision {
			return errors.New("The selected takeover revision is damaged")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = ensurePrivateDirectory(filepath.Dir(dir)); err != nil {
		return err
	}
	temp, err := os.MkdirTemp(filepath.Dir(dir), ".takeover-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	for name, body := range files {
		if err = writePrivateFile(filepath.Join(temp, name), []byte(body)); err != nil {
			return err
		}
	}
	if err = syncDirectory(temp); err != nil {
		return err
	}
	if err = os.Rename(temp, dir); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(dir))
}

func (l *stackLibrary) runTakeover(ctx context.Context, job StackJob, plan UpdatePlan) {
	fail := func() {
		_ = l.setJob(job.ID, "failed", "failed", "Takeover did not complete. Containers may have changed. Inspect the workload and review takeover recovery; no automatic rollback or volume deletion was attempted.")
	}
	if err := l.setJob(job.ID, "running", "validating", "Rechecking the source, container identities and preserved resources"); err != nil {
		fail()
		return
	}
	_, err := l.withCompose(ctx, func() ([]byte, error) {
		current, err := l.prepareTakeover(ctx, job.Name)
		if err != nil {
			return nil, err
		}
		if current.stamp != plan.stamp || current.Revision != plan.Revision {
			return nil, errors.New("Takeover review changed")
		}
		l.mu.Lock()
		record, err := l.unchangedRecord(job.Name, current.recordVersion)
		if err == nil {
			record.ConfigRevision = current.Revision
			err = l.storeTakeoverRevision(record, current.takeoverFiles)
		}
		if err == nil {
			record.Takeover = current.takeover
			record.Fingerprint = current.Revision
			record.Services = current.Services
			record.Warnings = []string{"Portainer source retained for provenance. Keep its automation and manual writers disabled. Images are pinned to the handoff identities; edit image references explicitly before registry upgrades."}
			record.LastDeploymentAttempt = &DeploymentAttempt{OperationID: job.ID, Revision: current.Revision, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
			err = l.writeRecord(record)
		}
		l.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if err = l.setJob(job.ID, "running", "applying", "Recreating the reviewed containers with their existing images and storage"); err != nil {
			return nil, err
		}
		dir, _ := l.contentDirectory(record)
		if _, err = l.runner.Run(ctx, job.Name, dir, "up", "--detach", "--force-recreate", "--no-build", "--pull", "never", "--wait", "--wait-timeout", "60"); err != nil {
			return nil, err
		}
		if err = l.setJob(job.ID, "running", "verifying", "Verifying every recreated service and its existing image, storage and network identities"); err != nil {
			return nil, err
		}
		items, err := l.source.ProjectContainers(ctx, job.Name)
		if err != nil {
			return nil, err
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Service < items[j].Service })
		inspector := l.source.(takeoverInspector)
		live := []TakeoverContainer{}
		seen := map[string]bool{}
		for _, item := range items {
			if item.Stack != job.Name || strings.EqualFold(item.ComposeOneOff, "true") || seen[item.Service] {
				return nil, errors.New("Unexpected takeover project scope")
			}
			seen[item.Service] = true
			c, err := inspector.TakeoverContainer(ctx, item.ID)
			if err != nil {
				return nil, err
			}
			if c.ID != item.ID || c.Service != item.Service || c.Name != item.Name || c.Image != item.ImageID {
				return nil, errors.New("Takeover identity changed")
			}
			live = append(live, c)
		}
		if err = l.checkTakeoverInventory(record, items, live, true); err != nil {
			return nil, err
		}
		// A completed job alone never grants ownership; publish managed state
		// only after every live identity and resource has been verified.
		if err = l.setJob(job.ID, "running", "recording", "Saving verified Dockspire management state"); err != nil {
			return nil, err
		}
		l.mu.Lock()
		record.Takeover.State = "managed"
		record.AppliedRevision = current.Revision
		record.DeployedAt = time.Now().UTC().Format(time.RFC3339)
		err = l.writeRecord(record)
		l.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, l.setJob(job.ID, "succeeded", "complete", "Takeover verified; Dockspire now manages this project. Keep Portainer writers disabled.")
	})
	if err != nil {
		fail()
	}
}

func (l *stackLibrary) takeoverSourceBundle(sourceFile string) (map[string]string, error) {
	if !l.allowImport || l.portainerRoot == "" {
		return nil, errors.New("Portainer source access is required to verify takeover recovery")
	}
	match := portainerFile.FindStringSubmatch(sourceFile)
	if len(match) != 2 {
		return nil, errors.New("Invalid takeover source")
	}
	dir := filepath.Join(l.portainerRoot, match[1])
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Portainer recovery source is unavailable or unsafe")
	}
	raw, err := readLimited(filepath.Join(dir, filepath.Base(sourceFile)))
	if err != nil {
		return nil, errors.New("Cannot verify the authorized Portainer recovery definition")
	}
	files := map[string]string{"compose.yaml": string(raw)}
	if l.includeEnvironment {
		for _, name := range []string{".env", "stack.env"} {
			raw, err := readLimited(filepath.Join(dir, name))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, errors.New("Cannot verify an authorized recovery environment file")
			}
			files[name] = string(raw)
		}
	}
	return files, nil
}
