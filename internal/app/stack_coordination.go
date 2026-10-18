package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Only metadata is hashed here. Configuration contents have their own revision
// fingerprint; immutable file bundles remain stable while external checks run.
func recordStamp(record StackRecord) string {
	raw, _ := json.Marshal(record)
	return fingerprint(string(raw))
}

// Call under mu when publishing an edit, review, or accepted operation.
func (l *stackLibrary) unchangedRecord(name, expected string) (StackRecord, error) {
	record, err := l.load(name)
	if err != nil {
		return record, err
	}
	if recordStamp(record) != expected {
		return record, errors.New("This stack changed while checks were running; reopen it and review again")
	}
	return record, nil
}

// Bound actual Compose subprocesses, including validation, independently of
// metadata locking. Waiting requests retain their own cancellation/deadline.
func (l *stackLibrary) runCompose(ctx context.Context, name, directory string, args ...string) ([]byte, error) {
	return l.withCompose(ctx, func() ([]byte, error) {
		return l.runner.Run(ctx, name, directory, args...)
	})
}

// The apply path acquires the slot before its final Docker/ownership checks,
// so queueing behind another validation cannot make those checks stale.
func (l *stackLibrary) withCompose(ctx context.Context, run func() ([]byte, error)) ([]byte, error) {
	if l.runner == nil {
		return nil, errors.New("Compose is unavailable on this host")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case l.composeSlot <- struct{}{}:
	}
	defer func() { <-l.composeSlot }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return run()
}

// Reserve the one host-wide container-operation slot before checking external
// state. This also blocks saves for this stack while leaving unrelated metadata
// operations free. No job is accepted until the fresh checks and persistence pass.
func (l *stackLibrary) reserveOperation(name, planID, confirmation string) (UpdatePlan, string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.jobsMu.Lock()
	defer l.jobsMu.Unlock()
	plan, ok := l.plans[planID]
	if !ok || plan.Name != name || time.Now().After(plan.ExpiresAt) {
		return UpdatePlan{}, "", errors.New("Review this operation again; the review expired")
	}
	expected := name
	if isLifecycle(plan.Kind) {
		expected = plan.Confirmation
	}
	if confirmation != expected {
		return plan, "", errors.New("Type the displayed stack or container name to confirm this operation")
	}
	if len(l.active) > 0 {
		return plan, "", errors.New("Another container operation is running; wait for it to finish")
	}
	id := randomID()
	l.active[name] = id
	return plan, id, nil
}

func (l *stackLibrary) releaseOperation(name, id string) {
	l.jobsMu.Lock()
	defer l.jobsMu.Unlock()
	if l.active[name] == id {
		delete(l.active, name)
	}
}
