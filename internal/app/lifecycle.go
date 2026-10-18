package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

type lifecycleController interface {
	ControlContainer(context.Context, string, string) error
	ContainerState(context.Context, string) (string, error)
}
type LifecycleTarget struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	State   string `json:"state"`
	Service string `json:"service"`
	stamp   string
}

func isLifecycle(action string) bool {
	return action == "start" || action == "stop" || action == "restart"
}
func (l *stackLibrary) actionPlan(ctx context.Context, name, id, action string) (UpdatePlan, error) {
	if action == "" {
		return l.buildPlan(ctx, name, id)
	}
	if !isLifecycle(action) {
		return UpdatePlan{}, errors.New("Choose start, stop, or restart")
	}
	return l.buildLifecyclePlan(ctx, name, id, action)
}
func (l *stackLibrary) rebuildPlan(ctx context.Context, plan UpdatePlan) (UpdatePlan, error) {
	action := ""
	if isLifecycle(plan.Kind) {
		action = plan.Kind
	}
	return l.actionPlan(ctx, plan.Name, plan.ContainerID, action)
}
func containerIdentity(item Container) string {
	raw, _ := json.Marshal([]string{item.ID, item.Name, item.Image, item.State, item.Stack, item.Service, item.ComposeFiles, item.ComposeDirectory, item.ComposeOneOff})
	return fingerprint(string(raw))
}
func (l *stackLibrary) buildLifecyclePlan(ctx context.Context, name, id, action string) (UpdatePlan, error) {
	plan := UpdatePlan{Name: name, Kind: action, ContainerID: id, Confirmation: name, Targets: []string{}, Containers: []LifecycleTarget{}}
	record, err := l.load(name)
	if err != nil {
		return plan, err
	}
	if record.Origin != "new" || !record.hasDeployment() {
		return plan, errors.New("Start, stop, and restart require a stack deployed by Dockspire; other managers and drafts are view-only")
	}
	plan.recordVersion = recordStamp(record)
	if _, ok := l.source.(lifecycleController); !ok {
		return plan, errors.New("Container lifecycle controls are unavailable on this connection")
	}
	plan.Revision, err = l.contentFingerprint(record)
	if err != nil {
		return plan, err
	}
	containers, err := l.source.ProjectContainers(ctx, name)
	if err != nil {
		return plan, err
	}
	identities := []string{}
	selected := false
	for _, item := range containers {
		if item.Stack != name {
			continue
		}
		if !l.ownsContainer(record, item) {
			return plan, errors.New("This project contains containers from another manager; lifecycle control was blocked")
		}
		identities = append(identities, containerIdentity(item))
		if id != "" && item.ID != id {
			continue
		}
		selected = true
		if item.State != "running" && item.State != "exited" && item.State != "created" {
			return plan, fmt.Errorf("%s is %s; inspect it before using lifecycle controls", item.Name, item.State)
		}
		eligible := (action == "start" && item.State != "running") || (action != "start" && item.State == "running")
		if eligible {
			plan.Containers = append(plan.Containers, LifecycleTarget{ID: item.ID, Name: item.Name, State: item.State, Service: item.Service, stamp: containerIdentity(item)})
		}
	}
	if !selected {
		return plan, errors.New("The selected stack or container no longer exists; refresh inventory")
	}
	if len(plan.Containers) == 0 {
		return plan, errors.New("No eligible containers: start applies to stopped containers; stop and restart apply to running containers")
	}
	sort.Slice(plan.Containers, func(i, j int) bool {
		if action == "stop" {
			return plan.Containers[i].Name > plan.Containers[j].Name
		}
		return plan.Containers[i].Name < plan.Containers[j].Name
	})
	for _, item := range plan.Containers {
		plan.Targets = append(plan.Targets, item.Name)
	}
	if id != "" {
		plan.Confirmation = plan.Targets[0]
	}
	sort.Strings(identities)
	raw, _ := json.Marshal(identities)
	plan.stamp = fingerprint(plan.recordVersion + "\x00" + string(raw))
	return plan, nil
}
func (d *dockerSource) ControlContainer(ctx context.Context, id, action string) error {
	if !isLifecycle(action) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(id) {
		return errors.New("Unsupported container lifecycle request")
	}
	prefix, err := d.monitorPrefix(ctx)
	if err != nil {
		return err
	}
	path := prefix + "/containers/" + id + "/" + action
	if action != "start" {
		path += "?t=30"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, d.base+path, nil)
	if err != nil {
		return errors.New("Cannot prepare container lifecycle request")
	}
	// Stop/restart may use a 30-second graceful shutdown. Inventory retains its shorter timeout.
	client := *d.client
	client.Timeout = 45 * time.Second
	response, err := client.Do(request)
	if err != nil {
		return errors.New("Docker lifecycle request did not complete; inspect the container before retrying")
	}
	defer response.Body.Close()
	if response.StatusCode != 204 && !(response.StatusCode == 304 && action != "restart") {
		return fmt.Errorf("Docker lifecycle request failed (HTTP %d); inspect the container before retrying", response.StatusCode)
	}
	return nil
}
func (d *dockerSource) ContainerState(ctx context.Context, id string) (string, error) {
	detail, err := d.Details(ctx, id)
	return detail.State, err
}
func (l *stackLibrary) lifecycleProgress(id, stage, message string, completed int) error {
	l.jobsMu.Lock()
	defer l.jobsMu.Unlock()
	job := l.jobs[id]
	job.Stage, job.Message, job.Completed = stage, message, completed
	if err := l.persistJobLocked(job); err != nil {
		return err
	}
	l.jobs[id] = job
	return nil
}
func (l *stackLibrary) runLifecycle(ctx context.Context, job StackJob, plan UpdatePlan) {
	completed := 0
	fail := func(reason string) {
		_ = l.setJob(job.ID, "failed", "failed", fmt.Sprintf("%s Verified %d of %d targeted containers. Some containers may have changed; inspect the stack before retrying. Remaining containers were not attempted.", reason, completed, len(plan.Containers)))
	}
	progress := func(stage, message string) bool {
		if l.lifecycleProgress(job.ID, stage, message, completed) != nil {
			fail("Cannot save operation history.")
			return false
		}
		return true
	}
	if !progress("validating", "Rechecking container ownership and reviewed scope") {
		return
	}
	current, err := l.rebuildPlan(ctx, plan)
	if err != nil || current.Revision != plan.Revision || current.stamp != plan.stamp {
		fail("The definition or containers changed since review; no lifecycle request was sent.")
		return
	}
	controller := l.source.(lifecycleController)
	for _, target := range plan.Containers {
		// Recheck each exact ID before its write; preceding actions must not invalidate later targets.
		current, err = l.buildLifecyclePlan(ctx, plan.Name, target.ID, plan.Kind)
		if err != nil || current.Revision != plan.Revision || len(current.Containers) != 1 || current.Containers[0].stamp != target.stamp {
			fail("A targeted container changed before its action.")
			return
		}
		if !progress("applying", fmt.Sprintf("%s: %s (%d of %d)", strings.ToUpper(plan.Kind), target.Name, completed+1, len(plan.Containers))) {
			return
		}
		if err = controller.ControlContainer(ctx, target.ID, plan.Kind); err != nil {
			fail("Docker did not complete the requested action.")
			return
		}
		if !progress("verifying", "Checking the resulting state of "+target.Name) {
			return
		}
		check, cancel := context.WithTimeout(ctx, 10*time.Second)
		verified := false
		for check.Err() == nil {
			state, stateErr := controller.ContainerState(check, target.ID)
			if stateErr != nil {
				break
			}
			if (plan.Kind == "stop" && (state == "exited" || state == "created")) || (plan.Kind != "stop" && state == "running") {
				verified = true
				break
			}
			select {
			case <-check.Done():
			case <-time.After(250 * time.Millisecond):
			}
		}
		cancel()
		if !verified {
			fail("The resulting container state could not be verified.")
			return
		}
		completed++
	}
	if !progress("recording", "Saving the verified lifecycle result") {
		return
	}
	message := fmt.Sprintf("%s completed for %d existing container(s); no images pulled or containers recreated", strings.ToUpper(plan.Kind), completed)
	if err = l.setJob(job.ID, "succeeded", "complete", message); err != nil {
		fail("Actions completed, but final history could not be saved.")
	}
}
