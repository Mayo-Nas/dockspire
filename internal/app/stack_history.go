package app

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type historyRef struct {
	ID      string
	Started time.Time
}

// The index is derived, never authoritative. Scan once on cold access and again
// only if another writer changes directory entries. Normal requests read only
// their selected stack's page, and normal writes update this index directly.
func (l *stackLibrary) syncHistoryIndexLocked() error {
	dir := filepath.Join(l.root, "jobs")
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		l.historyIndex = map[string][]historyRef{}
		l.historyStamp = time.Time{}
		return nil
	}
	if err != nil || !info.IsDir() {
		return errors.New("Cannot read operation history")
	}
	if l.historyIndex != nil && info.ModTime().Equal(l.historyStamp) {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return errors.New("Cannot read operation history")
	}
	l.historyIndex = map[string][]historyRef{}
	l.historyDamaged = false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		job, err := l.readJobFile(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			l.historyDamaged = true
			continue
		}
		started, _ := time.Parse(time.RFC3339Nano, job.StartedAt)
		l.historyIndex[job.Name] = append(l.historyIndex[job.Name], historyRef{job.ID, started})
	}
	for _, refs := range l.historyIndex {
		sort.Slice(refs, func(i, j int) bool {
			return refs[i].Started.After(refs[j].Started) || (refs[i].Started.Equal(refs[j].Started) && refs[i].ID > refs[j].ID)
		})
	}
	l.historyStamp = info.ModTime()
	return nil
}

func (l *stackLibrary) updateHistoryStampLocked() {
	if info, err := os.Stat(filepath.Join(l.root, "jobs")); err == nil {
		l.historyStamp = info.ModTime()
	}
}
func (l *stackLibrary) indexJobLocked(job StackJob) {
	refs := l.historyIndex[job.Name]
	for _, ref := range refs {
		if ref.ID == job.ID {
			return
		}
	}
	started, _ := time.Parse(time.RFC3339Nano, job.StartedAt)
	ref := historyRef{job.ID, started}
	position := sort.Search(len(refs), func(i int) bool {
		return refs[i].Started.Before(started) || (refs[i].Started.Equal(started) && refs[i].ID < job.ID)
	})
	refs = append(refs, historyRef{})
	copy(refs[position+1:], refs[position:])
	refs[position] = ref
	l.historyIndex[job.Name] = refs
}

func (l *stackLibrary) historyPage(name, cursor string, limit int) (StackHistory, error) {
	result := StackHistory{Jobs: []StackJob{}, Warnings: []string{}}
	if _, err := l.directory(name); err != nil {
		return result, err
	}
	if limit < 1 || limit > 1000 || (cursor != "" && !validOperationID(cursor)) {
		return result, errors.New("Invalid history page")
	}
	l.jobsMu.Lock()
	defer l.jobsMu.Unlock()
	if err := l.syncHistoryIndexLocked(); err != nil {
		return result, err
	}
	refs := l.historyIndex[name]
	start := 0
	if cursor != "" {
		found := false
		for i, ref := range refs {
			if ref.ID == cursor {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return result, errors.New("History changed; reopen the stack to load recent operations")
		}
	}
	damaged := l.historyDamaged
	end := min(start+limit, len(refs))
	for i := start; i < end; i++ {
		ref := refs[i]
		job, err := l.readJobFile(ref.ID)
		if err != nil || job.Name != name {
			damaged = true
			continue
		}
		if current, ok := l.jobs[ref.ID]; ok {
			job = current
		}
		result.Jobs = append(result.Jobs, job)
	}
	if end < len(refs) {
		result.NextCursor = refs[end-1].ID
	}
	if damaged {
		result.Warnings = append(result.Warnings, "Some operation records in the library could not be read. History may be incomplete; damaged files were left unchanged.")
	}
	if l.historyCleanupFailed[name] {
		result.Warnings = append(result.Warnings, "Some older operation records could not be removed. Retention will be retried after the next operation.")
	}
	return result, nil
}

// Called after a terminal result, with jobsMu held. Never prune configurations,
// damaged records, the active operation, the recorded attempt, or the most recent
// successful Compose operation (the available deployment baseline).
func (l *stackLibrary) pruneHistoryLocked(name string) (cleanupErr error) {
	defer func() { l.historyCleanupFailed[name] = cleanupErr != nil }()
	if l.historyKeep < 1 {
		return errors.New("Invalid history retention")
	}
	if err := l.syncHistoryIndexLocked(); err != nil {
		return err
	}
	record, err := l.load(name)
	if err != nil {
		return err
	}
	protected := map[string]bool{l.active[name]: true}
	if record.LastDeploymentAttempt != nil {
		protected[record.LastDeploymentAttempt.OperationID] = true
	}
	refs := l.historyIndex[name]
	baseline := false
	appliedBaseline := record.AppliedRevision == ""
	kept := make([]historyRef, 0, len(refs))
	for i, ref := range refs {
		job, err := l.readJobFile(ref.ID)
		_, timeErr := time.Parse(time.RFC3339Nano, job.StartedAt)
		knownStatus := job.Status == "succeeded" || job.Status == "failed" || job.Status == "interrupted" || job.Status == "running"
		if err != nil || job.Name != name || timeErr != nil || !knownStatus {
			l.historyDamaged = true
			kept = append(kept, ref)
			continue
		}
		if !baseline && job.Status == "succeeded" && !isLifecycle(job.Kind) {
			protected[ref.ID] = true
			baseline = true
		}
		if !appliedBaseline && job.Status == "succeeded" && !isLifecycle(job.Kind) && job.Revision == record.AppliedRevision {
			protected[ref.ID] = true
			appliedBaseline = true
		}
		if i < l.historyKeep || protected[ref.ID] {
			kept = append(kept, ref)
			continue
		}
		if err := os.Remove(filepath.Join(l.root, "jobs", ref.ID+".json")); err != nil {
			cleanupErr = errors.New("Some older operation records could not be removed")
			kept = append(kept, ref)
			continue
		}
		delete(l.jobs, ref.ID)
	}
	l.historyIndex[name] = kept
	l.updateHistoryStampLocked()
	if err := syncDirectory(filepath.Join(l.root, "jobs")); err != nil && !os.IsNotExist(err) {
		cleanupErr = errors.New("Cannot confirm operation retention")
	}
	return cleanupErr
}
