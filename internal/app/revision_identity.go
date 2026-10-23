package app

import "errors"

// Historical identifiers remain stable. Only newly stored bundles use v2.
func initialRevision(record StackRecord, files map[string]string) string {
	if record.InitialFingerprintVersion == 2 {
		return filesFingerprint(files)
	}
	return legacyFilesFingerprint(files)
}
func matchesStoredRevision(files map[string]string, revision string) bool {
	return revision == filesFingerprint(files) || revision == legacyFilesFingerprint(files)
}
func (l *stackLibrary) storedRevision(record StackRecord) (string, error) {
	files, err := l.content(record)
	if err != nil {
		return "", err
	}
	if record.ConfigRevision == "" {
		return initialRevision(record, files), nil
	}
	if !matchesStoredRevision(files, record.ConfigRevision) {
		return "", errors.New("Selected configuration revision is damaged")
	}
	return record.ConfigRevision, nil
}
func (l *stackLibrary) appliedMatches(record StackRecord, current map[string]string) bool {
	previous, _, err := l.verifiedRevision(record.Name, record.AppliedRevision)
	return err == nil && filesFingerprint(previous) == filesFingerprint(current)
}
