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

const revisionKeep = 20
const revisionCleanupBatch = 100

type ConfigurationRevision struct {
	Revision string `json:"revision"`
	StoredAt string `json:"storedAt"`
	Initial  bool   `json:"initial,omitempty"`
	Selected bool   `json:"selected,omitempty"`
	Applied  bool   `json:"applied,omitempty"`
	Recovery bool   `json:"recovery,omitempty"`
}

type ConfigurationHistory struct {
	Revisions  []ConfigurationRevision `json:"revisions"`
	NextCursor string                  `json:"nextCursor,omitempty"`
}

type ConfigurationPreview struct {
	Revision      string              `json:"revision"`
	RecordVersion string              `json:"recordVersion"`
	Compose       string              `json:"compose"`
	Comparison    *RevisionComparison `json:"comparison"`
	CanRestore    bool                `json:"canRestore"`
}

type RevisionCleanup struct {
	Token    string   `json:"token"`
	Keep     int      `json:"keep"`
	Remove   []string `json:"remove"`
	Retained int      `json:"retained"`
	Eligible int      `json:"eligible"`
	Removed  int      `json:"removed,omitempty"`
}

// A bundle is immutable within one running library owner. Never follow links,
// including links in parent directories, or trust a directory name as its hash.
func (l *stackLibrary) safeRevisionContent(record StackRecord) (map[string]string, error) {
	if info, err := os.Lstat(l.root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Library storage is unavailable or unsafe")
	}
	dir, err := l.contentDirectory(record)
	if err != nil || !safeArchiveSource(l.root, filepath.Join(dir, "compose.yaml")) {
		return nil, errors.New("Configuration revision is missing or unsafe")
	}
	for _, name := range []string{".env", "stack.env"} {
		path := filepath.Join(dir, name)
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		}
		if !safeArchiveSource(l.root, path) {
			return nil, errors.New("Configuration revision is missing or unsafe")
		}
	}
	return l.content(record)
}

func (l *stackLibrary) verifiedRevision(name, revision string) (map[string]string, string, error) {
	if !revisionName.MatchString(revision) {
		return nil, "", errors.New("Invalid configuration revision")
	}
	record, err := l.load(name)
	if err != nil {
		return nil, "", err
	}
	for _, directory := range []string{"", revision} {
		files, err := l.safeRevisionContent(StackRecord{Name: name, ConfigRevision: directory})
		valid := err == nil && ((directory == "" && initialRevision(record, files) == revision) || (directory != "" && matchesStoredRevision(files, revision)))
		if valid {
			return files, directory, nil
		}
	}
	return nil, "", errors.New("Configuration revision is missing, unreadable, or damaged")
}

// Directory timestamps describe bundle storage, not a log of every save. Saving
// identical content reuses a hash. Restores change the selection, not these bytes.
// Called with mu held, so reviewed cleanup cannot remove a bundle during a read.
func (l *stackLibrary) revisionRowsLocked(name string) (StackRecord, []ConfigurationRevision, error) {
	record, err := l.load(name)
	if err != nil {
		return record, nil, err
	}
	initial, err := l.safeRevisionContent(StackRecord{Name: name})
	if err != nil {
		return record, nil, err
	}
	current, err := l.safeRevisionContent(record)
	if err != nil {
		return record, nil, err
	}
	selected, selectErr := l.storedRevision(record)
	if selectErr != nil {
		return record, nil, selectErr
	}
	if record.ConfigRevision != "" && !matchesStoredRevision(current, record.ConfigRevision) {
		return record, nil, errors.New("Selected configuration revision is damaged")
	}
	rows := []ConfigurationRevision{{Revision: initialRevision(record, initial), StoredAt: record.CreatedAt, Initial: true}}
	base, _ := l.directory(name)
	dir := filepath.Join(base, "revisions")
	if info, err := os.Lstat(dir); !os.IsNotExist(err) && (err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return record, nil, errors.New("Revision storage is unavailable or unsafe")
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return record, nil, errors.New("Cannot read revision storage")
	}
	if len(entries) > 10000 {
		return record, nil, errors.New("Revision storage exceeds the browsing limit; use offline maintenance")
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue // Interrupted staging/cleanup is private and never a revision.
		}
		info, err := entry.Info()
		if err != nil || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !revisionName.MatchString(entry.Name()) {
			return record, nil, errors.New("Revision storage contains an unsupported entry; inspect it offline")
		}
		if entry.Name() != rows[0].Revision {
			rows = append(rows, ConfigurationRevision{Revision: entry.Name(), StoredAt: info.ModTime().UTC().Format(time.RFC3339Nano)})
		}
	}
	for i := range rows {
		row := &rows[i]
		row.Selected, row.Applied = row.Revision == selected, row.Revision == record.AppliedRevision
		row.Recovery = record.LastDeploymentAttempt != nil && row.Revision == record.LastDeploymentAttempt.Revision
	}
	sort.Slice(rows, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, rows[i].StoredAt)
		b, _ := time.Parse(time.RFC3339Nano, rows[j].StoredAt)
		return a.After(b) || (a.Equal(b) && rows[i].Revision > rows[j].Revision)
	})
	return record, rows, nil
}

func (l *stackLibrary) revisionHistory(name, cursor string, limit int) (ConfigurationHistory, error) {
	result := ConfigurationHistory{Revisions: []ConfigurationRevision{}}
	if limit < 1 || limit > 100 || (cursor != "" && !revisionName.MatchString(cursor)) {
		return result, errors.New("Invalid revision page")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, rows, err := l.revisionRowsLocked(name)
	if err != nil {
		return result, err
	}
	start := 0
	if cursor != "" {
		start = -1
		for i, row := range rows {
			if row.Revision == cursor {
				start = i + 1
				break
			}
		}
		if start == -1 {
			return result, errors.New("Revisions changed; reopen configuration history")
		}
	}
	end := min(start+limit, len(rows))
	result.Revisions = append(result.Revisions, rows[start:end]...)
	if end < len(rows) {
		result.NextCursor = rows[end-1].Revision
	}
	return result, nil
}

func (l *stackLibrary) revisionPreview(name, revision string) (ConfigurationPreview, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	record, err := l.load(name)
	if err != nil {
		return ConfigurationPreview{}, err
	}
	current, err := l.safeRevisionContent(record)
	if err != nil {
		return ConfigurationPreview{}, err
	}
	files, _, err := l.verifiedRevision(name, revision)
	if err != nil {
		return ConfigurationPreview{}, err
	}
	_, masked, _, err := inspectCompose(files["compose.yaml"], false)
	if err != nil {
		return ConfigurationPreview{}, errors.New("Saved revision cannot be previewed")
	}
	c := &RevisionComparison{Status: "available", BaseRevision: filesFingerprint(current), Revision: revision, Changes: []ConfigurationChange{}}
	return ConfigurationPreview{revision, recordStamp(record), masked, compareConfiguration(c, current, files), record.managed() && !l.isBusy(name)}, nil
}

func (l *stackLibrary) restoreRevision(ctx context.Context, name, revision, expected, confirmation string) (StackRecord, error) {
	if confirmation != name || expected == "" {
		return StackRecord{}, errors.New("Review the revision and type the stack name to restore it as a draft")
	}
	l.mu.Lock()
	record, err := l.unchangedRecord(name, expected)
	if err == nil && (!record.managed() || l.isBusy(name)) {
		err = errors.New("Only an idle Dockspire-managed stack can restore a revision")
	}
	var files map[string]string
	var directory string
	if err == nil {
		files, directory, err = l.verifiedRevision(name, revision)
	}
	l.mu.Unlock()
	if err != nil {
		return record, err
	}
	// Compose config only: validation never deploys or changes running containers.
	validated, err := l.validateBundle(ctx, name, files)
	if err != nil {
		return record, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return record, err
	}
	if _, err := l.unchangedRecord(name, expected); err != nil {
		return record, err
	}
	if l.isBusy(name) {
		return record, errors.New("Wait for the active operation before restoring a revision")
	}
	if _, _, err := l.verifiedRevision(name, revision); err != nil {
		return record, err // A concurrent reviewed cleanup may have removed it.
	}
	record.ConfigRevision, record.Fingerprint = directory, filesFingerprint(files)
	record.Services, record.Warnings, record.EnvironmentNames = validated.Services, validated.Warnings, validated.EnvironmentNames
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	record.EditVersion++
	if err := l.writeRecord(record); err != nil {
		return record, errors.New("Cannot confirm draft restore; reopen the configuration before retrying")
	}
	return record, nil
}

// Called with mu then jobsMu held. Unknown history blocks deletion across the
// library: an unreadable record cannot safely be attributed to a different stack.
func (l *stackLibrary) revisionCleanupLocked(ctx context.Context, name string) (RevisionCleanup, error) {
	result := RevisionCleanup{Keep: revisionKeep, Remove: []string{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	record, rows, err := l.revisionRowsLocked(name)
	if err != nil {
		return result, err
	}
	if !record.managed() || l.active[name] != "" {
		return result, errors.New("Only an idle Dockspire-managed stack can clean up revisions")
	}
	protected := map[string]bool{}
	if record.Takeover != nil {
		protected[record.Takeover.Revision] = true
	}
	available := map[string]bool{}
	for _, row := range rows {
		available[row.Revision] = true
		if row.Initial || row.Selected || row.Applied || row.Recovery {
			protected[row.Revision] = true
		}
	}
	for _, revision := range []string{record.AppliedRevision, record.ConfigRevision} {
		if revision != "" && !available[revision] {
			return result, errors.New("A referenced revision is unavailable; cleanup is blocked")
		}
	}
	if record.LastDeploymentAttempt != nil && !available[record.LastDeploymentAttempt.Revision] {
		return result, errors.New("A recovery revision is unavailable; cleanup is blocked")
	}
	if record.Takeover != nil && !available[record.Takeover.Revision] {
		return result, errors.New("The takeover revision is unavailable; cleanup is blocked")
	}
	entries, err := os.ReadDir(filepath.Join(l.root, "jobs"))
	if err != nil && !os.IsNotExist(err) {
		return result, errors.New("Cannot inspect retained history; cleanup is blocked")
	}
	if len(entries) > archiveFileLimit {
		return result, errors.New("History exceeds the maintenance limit; cleanup is blocked")
	}
	jobStamps := []string{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(l.root, "jobs", entry.Name())
		if !safeArchiveSource(l.root, path) {
			return result, errors.New("Unsafe operation history; cleanup is blocked")
		}
		job, err := l.readJobFile(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return result, errors.New("Damaged operation history; cleanup is blocked")
		}
		knownStatus := job.Status == "succeeded" || job.Status == "failed" || job.Status == "interrupted"
		knownKind := isLifecycle(job.Kind) || job.Kind == "deploy" || job.Kind == "update" || job.Kind == "service-update" || job.Kind == "recover" || job.Kind == "takeover"
		if _, err := time.Parse(time.RFC3339Nano, job.StartedAt); err != nil || !knownStatus || !knownKind {
			return result, errors.New("Unrecognized operation history; cleanup is blocked")
		}
		if job.Name != name {
			continue
		}
		stamp, _ := json.Marshal(job)
		jobStamps = append(jobStamps, fingerprint(string(stamp)))
		for _, revision := range []string{job.Revision, job.PreviousAppliedRevision} {
			if revision == "" {
				continue // Legacy jobs may not have a recorded revision.
			}
			if !available[revision] {
				return result, errors.New("An operation references an unavailable revision; cleanup is blocked")
			}
			protected[revision] = true
		}
	}
	for i := len(rows) - 1; i >= revisionKeep; i-- {
		row := rows[i]
		if protected[row.Revision] {
			continue
		}
		result.Eligible++
		if len(result.Remove) == revisionCleanupBatch {
			continue
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		_, directory, err := l.verifiedRevision(name, row.Revision)
		if err != nil || directory == "" {
			return result, errors.New("A cleanup candidate is damaged; no revisions were removed")
		}
		base, _ := l.directory(name)
		contents, err := os.ReadDir(filepath.Join(base, "revisions", directory))
		if err != nil {
			return result, errors.New("Cannot inspect revision files; cleanup is blocked")
		}
		for _, file := range contents {
			if !file.Type().IsRegular() || (file.Name() != "compose.yaml" && file.Name() != ".env" && file.Name() != "stack.env") {
				return result, errors.New("Unexpected revision contents; cleanup is blocked")
			}
		}
		result.Remove = append(result.Remove, row.Revision)
	}
	result.Retained = len(rows) - len(result.Remove)
	state, _ := json.Marshal(struct {
		Record string
		Rows   []ConfigurationRevision
		Jobs   []string
		Remove []string
	}{recordStamp(record), rows, jobStamps, result.Remove})
	result.Token = fingerprint(string(state))
	return result, nil
}

func (l *stackLibrary) reviewRevisionCleanup(ctx context.Context, name string) (RevisionCleanup, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.jobsMu.Lock()
	defer l.jobsMu.Unlock()
	return l.revisionCleanupLocked(ctx, name)
}

func (l *stackLibrary) cleanRevisions(ctx context.Context, name, token, confirmation string) (RevisionCleanup, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.jobsMu.Lock()
	defer l.jobsMu.Unlock()
	plan, err := l.revisionCleanupLocked(ctx, name)
	if err != nil {
		return plan, err
	}
	if token == "" || token != plan.Token || confirmation != name {
		return plan, errors.New("Reopen revision cleanup and type the stack name to confirm")
	}
	if err := ctx.Err(); err != nil || len(plan.Remove) == 0 {
		return plan, err
	}
	base, _ := l.directory(name)
	trash, err := os.MkdirTemp(base, ".revision-cleanup-")
	if err != nil {
		return plan, errors.New("Cannot stage revision cleanup")
	}
	defer os.RemoveAll(trash)
	for _, revision := range plan.Remove {
		if err := ctx.Err(); err != nil {
			return plan, err
		}
		// Rename entire bundles before removal. A crash can leave private trash,
		// but cannot leave a partially deleted revision visible to backup/restore.
		if err := os.Rename(filepath.Join(base, "revisions", revision), filepath.Join(trash, revision)); err != nil {
			return plan, errors.New("Cleanup stopped after a storage error; reopen history before retrying")
		}
		plan.Removed++
	}
	if syncDirectory(filepath.Join(base, "revisions")) != nil || syncDirectory(trash) != nil || syncDirectory(base) != nil {
		return plan, errors.New("Cleanup completed but durable storage could not be confirmed; reopen history")
	}
	if err := os.RemoveAll(trash); err != nil {
		return plan, errors.New("Old versions were retired, but private cleanup files remain; inspect storage offline")
	}
	if err := syncDirectory(base); err != nil {
		return plan, errors.New("Temporary files were removed but durable cleanup could not be confirmed")
	}
	return plan, nil
}
