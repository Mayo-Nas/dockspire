package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type UpdatePlan struct {
	takeover      *TakeoverState
	takeoverFiles map[string]string
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Kind          string              `json:"kind"`
	Service       string              `json:"service,omitempty"`
	ContainerID   string              `json:"containerId,omitempty"`
	Revision      string              `json:"revision"`
	Targets       []string            `json:"targets"`
	Services      []ServiceSummary    `json:"services"`
	ExpiresAt     time.Time           `json:"expiresAt"`
	Confirmation  string              `json:"confirmation,omitempty"`
	Containers    []LifecycleTarget   `json:"containers,omitempty"`
	Comparison    *RevisionComparison `json:"comparison,omitempty"`
	stamp         string
	recordVersion string
	exactRevision string
	imageBefore   map[string]observedServiceImages
}
type StackJob struct {
	ID                      string           `json:"id"`
	Name                    string           `json:"name"`
	Kind                    string           `json:"kind"`
	Service                 string           `json:"service,omitempty"`
	Status                  string           `json:"status"`
	Stage                   string           `json:"stage"`
	Message                 string           `json:"message"`
	StartedAt               string           `json:"startedAt"`
	FinishedAt              string           `json:"finishedAt,omitempty"`
	ContainerID             string           `json:"containerId,omitempty"`
	ContainerName           string           `json:"containerName,omitempty"`
	Targets                 []string         `json:"targets,omitempty"`
	Total                   int              `json:"total,omitempty"`
	Completed               int              `json:"completed,omitempty"`
	Revision                string           `json:"revision,omitempty"`
	PreviousAppliedRevision string           `json:"previousAppliedRevision,omitempty"`
	ApplyStartedAt          string           `json:"applyStartedAt,omitempty"`
	Images                  *OperationImages `json:"images,omitempty"`
}

func validOperationID(id string) bool {
	return len(id) == 32 && strings.Trim(id, "0123456789abcdef") == ""
}

func (record StackRecord) hasDeployment() bool {
	return record.DeployedAt != "" || record.LastDeploymentAttempt != nil
}

func (l *stackLibrary) needsRecovery(record StackRecord) bool {
	if record.Takeover != nil && record.Takeover.State == "pending" {
		return true
	}
	if record.LastDeploymentAttempt == nil {
		return false
	}
	attempt := record.LastDeploymentAttempt
	job, err := l.job(attempt.OperationID)
	return err != nil || job.Name != record.Name || job.Revision != attempt.Revision || job.Status != "succeeded"
}

func randomID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("Cannot generate operation ID")
	}
	return hex.EncodeToString(raw[:])
}
func (l *stackLibrary) isBusy(name string) bool {
	l.jobsMu.Lock()
	defer l.jobsMu.Unlock()
	return l.active[name] != ""
}
func (l *stackLibrary) ownsContainer(record StackRecord, item Container) bool {
	if strings.EqualFold(item.ComposeOneOff, "true") {
		return false
	}
	base, _ := l.directory(record.Name)
	dir := filepath.Clean(item.ComposeDirectory)
	if dir != base {
		relative, err := filepath.Rel(filepath.Join(base, "revisions"), dir)
		if err != nil || !revisionName.MatchString(relative) {
			return false
		}
	}
	return item.ComposeFiles == filepath.Join(dir, "compose.yaml") && projectName.MatchString(item.Service)
}
func (l *stackLibrary) buildPlan(ctx context.Context, name, containerID string) (UpdatePlan, error) {
	record, err := l.load(name)
	if err != nil {
		return UpdatePlan{}, err
	}
	if !record.managed() {
		return UpdatePlan{}, errors.New("Imported copies require an explicitly confirmed, verified takeover before normal deployment")
	}
	revision, err := l.storedRevision(record)
	if err != nil {
		return UpdatePlan{}, err
	}
	plan := UpdatePlan{Name: name, Revision: revision, Kind: "deploy", Targets: []string{}, Services: record.Services, ContainerID: containerID}
	plan.recordVersion = recordStamp(record)
	plan.exactRevision, err = l.contentFingerprint(record)
	if err != nil {
		return plan, err
	}
	if record.DeployedAt != "" {
		plan.Kind = "update"
	}
	if l.needsRecovery(record) {
		plan.Kind = "recover"
	}
	if containerID != "" && record.DeployedAt == "" {
		return plan, errors.New("Deploy this stack before updating a service")
	}
	if containerID != "" && plan.Kind == "recover" {
		return plan, errors.New("Inspect the previous deployment and review recovery of the whole stack before updating a service")
	}
	containers, err := l.source.ProjectContainers(ctx, name)
	if err != nil {
		return plan, err
	}
	var targets []Container
	for _, item := range containers {
		if item.Stack != name {
			continue
		}
		if !record.hasDeployment() {
			return plan, errors.New("A Docker project already uses this name; deployment was cancelled")
		}
		if !l.ownsContainer(record, item) {
			return plan, errors.New("This project contains containers from another manager; update was blocked")
		}
		targets = append(targets, item)
		if item.ID == containerID {
			plan.Service = item.Service
		}
	}
	if containerID != "" {
		if plan.Service == "" {
			return plan, errors.New("The selected container changed or no longer exists; refresh inventory")
		}
		currentFiles, readErr := l.content(record)
		if readErr != nil || !l.appliedMatches(record, currentFiles) {
			return plan, errors.New("The saved definition has unapplied changes; review and update the whole stack first")
		}
		plan.Kind = "service-update"
		plan.Services = nil
		for _, service := range record.Services {
			if service.Name == plan.Service {
				plan.Services = append(plan.Services, service)
			}
		}
		if len(plan.Services) == 0 {
			return plan, errors.New("Service is absent from the saved definition")
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })
	for _, item := range targets {
		if plan.Service == "" || item.Service == plan.Service {
			plan.Targets = append(plan.Targets, item.Name)
		}
	}
	// Human-readable uptime changes between requests; only stable identity and scope belong in this check.
	identities := []string{}
	for _, item := range targets {
		identities = append(identities, strings.Join([]string{item.ID, item.Name, item.Image, item.State, item.Stack, item.Service, item.ComposeFiles, item.ComposeDirectory}, "\x00"))
	}
	material, _ := json.Marshal(identities)
	plan.stamp = fingerprint(plan.recordVersion + "\x00" + plan.exactRevision + "\x00" + string(material))
	plan.imageBefore = l.observeImages(record, plan.Service, containers)
	return plan, nil
}
func (l *stackLibrary) review(ctx context.Context, name, containerID string) (UpdatePlan, error) {
	return l.reviewAction(ctx, name, containerID, "")
}
func (l *stackLibrary) reviewAction(ctx context.Context, name, containerID, action string) (UpdatePlan, error) {
	if l.isBusy(name) {
		return UpdatePlan{}, errors.New("An operation is already running for this stack")
	}
	plan, err := l.actionPlan(ctx, name, containerID, action)
	if err != nil {
		return plan, err
	}
	if !isLifecycle(plan.Kind) && plan.Kind != "takeover" {
		plan.Comparison, err = l.comparisonForPlan(plan)
		if err != nil {
			return plan, err
		}
		// Image interpolation can itself contain a secret default. Display only
		// plain image references; Compose still reads the original private files.
		plan.Services = append([]ServiceSummary{}, plan.Services...)
		for i := range plan.Services {
			plan.Services[i].Image = comparisonValue("image", plan.Services[i].Image)
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return plan, err
	}
	if l.isBusy(name) {
		return plan, errors.New("An operation is already running for this stack")
	}
	if _, err := l.unchangedRecord(name, plan.recordVersion); err != nil {
		return plan, err
	}
	plan.ID, plan.ExpiresAt = randomID(), time.Now().Add(5*time.Minute)
	l.jobsMu.Lock()
	defer l.jobsMu.Unlock()
	for id, old := range l.plans {
		if time.Now().After(old.ExpiresAt) {
			delete(l.plans, id)
		}
	}
	if len(l.plans) >= 256 {
		return plan, errors.New("Too many open reviews; wait for older reviews to expire")
	}
	stored := plan
	stored.Comparison = nil // Display-only data must not inflate the review cache.
	stored.imageBefore = nil
	stored.takeover, stored.takeoverFiles = nil, nil
	l.plans[plan.ID] = stored
	return plan, nil
}
func (l *stackLibrary) persistJob(job StackJob) error {
	l.jobsMu.Lock()
	defer l.jobsMu.Unlock()
	return l.persistJobLocked(job)
}
func (l *stackLibrary) persistJobLocked(job StackJob) error {
	dir := filepath.Join(l.root, "jobs")
	if err := ensurePrivateDirectory(dir); err != nil {
		return err
	}
	if l.historyIndex != nil {
		if err := l.syncHistoryIndexLocked(); err != nil {
			return err
		}
	}
	if err := atomicJSON(filepath.Join(dir, job.ID+".json"), job); err != nil {
		return err
	}
	if l.historyIndex != nil {
		l.indexJobLocked(job)
		l.updateHistoryStampLocked()
	}
	return nil
}
func (l *stackLibrary) setJob(id, status, stage, message string) error {
	l.jobsMu.Lock()
	defer l.jobsMu.Unlock()
	job := l.jobs[id]
	job.Status, job.Stage, job.Message = status, stage, message
	if stage == "applying" && !isLifecycle(job.Kind) && job.ApplyStartedAt == "" {
		job.ApplyStartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if status != "running" {
		job.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	}
	err := l.persistJobLocked(job)
	l.jobs[id] = job
	if err == nil && status != "running" {
		l.pruneHistoryLocked(job.Name)
	}
	return err
}
func (l *stackLibrary) startJob(ctx context.Context, name, planID, confirmation string, portainerWritersDisabled ...bool) (StackJob, error) {
	if _, demo := l.runner.(demoCompose); demo {
		return StackJob{}, errors.New("Container operations are disabled in demo mode")
	}
	plan, id, err := l.reserveOperation(name, planID, confirmation)
	if err != nil {
		return StackJob{}, err
	}
	accepted := false
	defer func() {
		if !accepted {
			l.releaseOperation(name, id)
		}
	}()
	if plan.Kind == "takeover" && (len(portainerWritersDisabled) != 1 || !portainerWritersDisabled[0]) {
		return StackJob{}, errors.New("Confirm that Portainer automation, webhooks and other writers are disabled before takeover")
	}
	if err := l.host.available(ctx); err != nil {
		return StackJob{}, err
	}
	current, err := l.rebuildPlan(ctx, plan)
	if err != nil {
		return StackJob{}, err
	}
	if current.Revision != plan.Revision || current.stamp != plan.stamp || current.Kind != plan.Kind {
		return StackJob{}, errors.New("The definition or containers changed; review this operation again")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return StackJob{}, err
	}
	if time.Now().After(plan.ExpiresAt) {
		return StackJob{}, errors.New("Review this operation again; the review expired")
	}
	job := StackJob{ID: id, Name: name, Kind: plan.Kind, Service: plan.Service, Status: "running", Stage: "queued", Message: "Operation accepted", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	job.Revision, job.Targets = plan.Revision, append([]string{}, plan.Targets...)
	record, err := l.unchangedRecord(name, current.recordVersion)
	if err != nil {
		return StackJob{}, err
	}
	job.PreviousAppliedRevision = record.AppliedRevision
	if isLifecycle(plan.Kind) {
		job.ContainerID, job.Total, job.Targets = plan.ContainerID, len(plan.Containers), append([]string{}, plan.Targets...)
		if plan.ContainerID != "" {
			job.ContainerName = plan.Targets[0]
		}
	}
	if err = l.persistJob(job); err != nil {
		return StackJob{}, errors.New("Cannot save operation history; no containers changed")
	}
	l.jobsMu.Lock()
	l.jobs[job.ID] = job
	l.active[name] = job.ID
	delete(l.plans, planID)
	l.jobsMu.Unlock()
	accepted = true
	if plan.Kind == "takeover" {
		plan = current
	}
	go l.runJob(job, plan)
	return job, nil
}
func (l *stackLibrary) runJob(job StackJob, plan UpdatePlan) {
	defer l.releaseOperation(job.Name, job.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if plan.Kind == "takeover" {
		l.runTakeover(ctx, job, plan)
		return
	}
	if isLifecycle(plan.Kind) {
		l.runLifecycle(ctx, job, plan)
		return
	}
	fail := func(message string) { _ = l.setJob(job.ID, "failed", "failed", message) }
	progress := func(stage, message string) bool {
		if l.setJob(job.ID, "running", stage, message) != nil {
			fail("Operation history could not be written. Inspect this stack before retrying.")
			return false
		}
		return true
	}
	if !progress("validating", "Validating saved Compose configuration") {
		return
	}
	record, err := l.load(job.Name)
	if err != nil {
		fail(err.Error())
		return
	}
	dir, err := l.contentDirectory(record)
	if err != nil {
		fail(err.Error())
		return
	}
	if _, err = l.runCompose(ctx, job.Name, dir, "config", "--quiet"); err != nil {
		fail("Compose validation failed. Edit and validate the definition and environment before retrying.")
		return
	}
	if !progress("pulling", "Pulling service images; existing containers are still running") {
		return
	}
	policy := "always"
	if plan.Kind == "deploy" || plan.Kind == "recover" {
		policy = "missing"
	}
	args := []string{"pull", "--policy", policy}
	if plan.Service != "" {
		args = append(args, plan.Service)
	}
	// Local immutable image IDs cannot be pulled from a registry. A later
	// explicit tag edit resumes normal pulls for that service.
	pullServices := []string{}
	hasLocalPins := false
	for _, service := range plan.Services {
		if plan.Service == "" || service.Name == plan.Service {
			if strings.HasPrefix(service.Image, "sha256:") && revisionName.MatchString(strings.TrimPrefix(service.Image, "sha256:")) {
				hasLocalPins = true
			} else {
				pullServices = append(pullServices, service.Name)
			}
		}
	}
	if len(pullServices) > 0 {
		if hasLocalPins {
			args = append([]string{"pull", "--policy", policy}, pullServices...)
		}
		_, err = l.runCompose(ctx, job.Name, dir, args...)
	}
	if err != nil {
		fail("Image pull failed. Existing containers were not recreated. Check the image tag, registry access, and connectivity.")
		return
	}
	applyAttempted := false
	var before map[string]observedServiceImages
	_, err = l.withCompose(ctx, func() ([]byte, error) {
		current, err := l.buildPlan(ctx, job.Name, plan.ContainerID)
		if err != nil {
			return nil, errors.New("The project changed during image download or while waiting to apply. Refresh inventory and review again")
		}
		if current.Revision != plan.Revision || current.stamp != plan.stamp || current.Kind != plan.Kind {
			return nil, errors.New("The definition or containers changed during image download or while waiting to apply; no recreation was attempted. Review again")
		}
		before = current.imageBefore
		l.setImageObservations(job.ID, operationImages(plan.Services, before, nil))
		if err := l.setJob(job.ID, "running", "applying", "Applying Compose changes and waiting for services to be running or healthy"); err != nil {
			return nil, errors.New("Operation history could not be written. No Compose apply was attempted")
		}
		// Persist ownership intent before Compose can create partial resources.
		record.LastDeploymentAttempt = &DeploymentAttempt{OperationID: job.ID, Revision: plan.Revision, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		l.mu.Lock()
		err = l.writeRecord(record)
		l.mu.Unlock()
		if err != nil {
			return nil, errors.New("Cannot save the deployment attempt; no Compose apply was attempted. Reopen the stack before retrying.")
		}
		args := []string{"up", "--detach", "--no-build", "--pull", "never", "--wait", "--wait-timeout", "60"}
		if plan.Service != "" {
			args = append(args, "--no-deps", plan.Service)
		}
		applyAttempted = true
		if _, err := l.runner.Run(ctx, job.Name, dir, args...); err != nil {
			return nil, errors.New("Compose did not finish successfully. Some containers may have changed; inspect this stack before retrying. No automatic rollback was attempted.")
		}
		return nil, nil
	})
	if applyAttempted {
		// A timed-out/failed apply may still have changed containers. Observe it
		// once, with a separate short read deadline, without retrying the apply.
		observedCtx, observedCancel := context.WithTimeout(ctx, 5*time.Second)
		containers, readErr := l.source.ProjectContainers(observedCtx, job.Name)
		observedCancel()
		var after map[string]observedServiceImages
		if readErr == nil {
			after = l.observeImages(record, plan.Service, containers)
		}
		l.setImageObservations(job.ID, operationImages(plan.Services, before, after))
	}
	if err != nil {
		fail(err.Error())
		return
	}
	if !progress("recording", "Saving the successful operation") {
		return
	}
	l.mu.Lock()
	record.DeployedAt = time.Now().UTC().Format(time.RFC3339)
	if plan.Service == "" {
		record.AppliedRevision = plan.Revision
	}
	err = l.writeRecord(record)
	l.mu.Unlock()
	if err != nil {
		fail("Containers started, but the deployment record could not be saved. Inspect the stack before retrying.")
		return
	}
	if err = l.setJob(job.ID, "succeeded", "complete", "Compose completed; targeted services are running or healthy"); err != nil {
		fail("Containers started, but the final operation history could not be saved. Inspect the stack before retrying.")
	}
}
func (l *stackLibrary) job(id string) (StackJob, error) {
	if !validOperationID(id) {
		return StackJob{}, errors.New("Operation not found")
	}
	l.jobsMu.Lock()
	defer l.jobsMu.Unlock()
	if job, ok := l.jobs[id]; ok {
		return job, nil
	}
	return l.readJobFile(id)
}
func (l *stackLibrary) readJobFile(id string) (StackJob, error) {
	if !validOperationID(id) {
		return StackJob{}, errors.New("Operation not found")
	}
	raw, err := readLimited(filepath.Join(l.root, "jobs", id+".json"))
	var job StackJob
	if err != nil || json.Unmarshal(raw, &job) != nil || job.ID != id || !projectName.MatchString(job.Name) {
		return job, errors.New("Operation not found")
	}
	// An unfinished job from an earlier process cannot be assumed to have succeeded.
	if job.Status == "running" {
		job.Status, job.Stage, job.Message = "interrupted", "interrupted", "The application restarted. Inspect the stack before reviewing another operation."
	}
	return job, nil
}
func (l *stackLibrary) history(name string) ([]StackJob, error) {
	result, err := l.historyDetails(name)
	return result.Jobs, err
}

type StackHistory struct {
	Jobs       []StackJob `json:"jobs"`
	Warnings   []string   `json:"warnings"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

func (l *stackLibrary) historyDetails(name string) (StackHistory, error) {
	return l.historyPage(name, "", l.historyKeep)
}
