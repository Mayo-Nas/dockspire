package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

type editInput struct {
	Compose            string `json:"compose"`
	Environment        string `json:"environment"`
	ReplaceEnvironment bool   `json:"replaceEnvironment"`
	Revision           string `json:"revision"`
}

var revisionName = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (l *stackLibrary) contentDirectory(record StackRecord) (string, error) {
	dir, err := l.directory(record.Name)
	if err != nil {
		return "", err
	}
	if record.ConfigRevision != "" {
		if !revisionName.MatchString(record.ConfigRevision) {
			return "", errors.New("Invalid saved configuration revision")
		}
		dir = filepath.Join(dir, "revisions", record.ConfigRevision)
	}
	return dir, nil
}

func (l *stackLibrary) content(record StackRecord) (map[string]string, error) {
	dir, err := l.contentDirectory(record)
	if err != nil {
		return nil, err
	}
	files := map[string]string{}
	for _, name := range []string{"compose.yaml", ".env", "stack.env"} {
		raw, readErr := readLimited(filepath.Join(dir, name))
		if os.IsNotExist(readErr) && name != "compose.yaml" {
			continue
		}
		if readErr != nil {
			return nil, errors.New("Cannot read saved configuration")
		}
		files[name] = string(raw)
	}
	return files, nil
}
func legacyFilesFingerprint(files map[string]string) string {
	return fingerprint(files["compose.yaml"] + "\x00.env\x00" + files[".env"] + "\x00stack.env\x00" + files["stack.env"])
}
func filesFingerprint(files map[string]string) string {
	bundle := []any{"dockspire-files-v2"}
	for _, name := range []string{"compose.yaml", ".env", "stack.env"} {
		value, present := files[name]
		bundle = append(bundle, []any{name, present, []byte(value)})
	}
	raw, _ := json.Marshal(bundle)
	return fingerprint(string(raw))
}
func (l *stackLibrary) contentFingerprint(record StackRecord) (string, error) {
	files, err := l.content(record)
	if err != nil {
		return "", err
	}
	return filesFingerprint(files), nil
}
func (l *stackLibrary) writeRecord(record StackRecord) error {
	raw, marshalErr := json.MarshalIndent(record, "", "  ")
	if marshalErr != nil || len(raw) > maxDefinition {
		return errors.New("Stack metadata exceeds the 512 KiB storage limit; no record was written")
	}
	dir, err := l.directory(record.Name)
	if err != nil {
		return err
	}
	return atomicJSON(filepath.Join(dir, "record.json"), record)
}

// Hidden fields are merged on the server. A masked preview is never deployed as configuration.
func mergeHidden(incoming, original, masked any) (any, error) {
	if text, ok := incoming.(string); ok && text == "[hidden]" {
		if masked == "[hidden]" {
			return original, nil
		}
		return nil, errors.New("A hidden placeholder has no saved value; enter a replacement")
	}
	switch next := incoming.(type) {
	case map[string]any:
		old, _ := original.(map[string]any)
		mask, _ := masked.(map[string]any)
		for key, item := range next {
			merged, err := mergeHidden(item, old[key], mask[key])
			if err != nil {
				return nil, err
			}
			next[key] = merged
		}
	case []any:
		old, _ := original.([]any)
		mask, _ := masked.([]any)
		for i, item := range next {
			// Environment list entries must be matched by variable name, not position.
			if text, ok := item.(string); ok && strings.HasSuffix(text, "=[hidden]") {
				key := strings.TrimSuffix(text, "=[hidden]")
				found := false
				for j, candidate := range mask {
					if candidate == key+"=[hidden]" && j < len(old) {
						next[i] = old[j]
						found = true
						break
					}
				}
				if !found {
					return nil, errors.New("A hidden environment variable has no saved value; enter a replacement")
				}
				continue
			}
			var before, hidden any
			if i < len(old) {
				before = old[i]
			}
			if i < len(mask) {
				hidden = mask[i]
			}
			merged, err := mergeHidden(item, before, hidden)
			if err != nil {
				return nil, err
			}
			next[i] = merged
		}
	}
	return incoming, nil
}
func restoreHidden(raw, original string) (string, error) {
	// Validate first so duplicate keys, multiple documents, and unsupported features cannot be normalized away.
	if _, _, _, err := inspectCompose(raw, true); err != nil {
		return "", err
	}
	_, preview, _, err := inspectCompose(original, true)
	if err != nil {
		return "", err
	}
	if raw == preview {
		return original, nil
	}
	var next, old, mask map[string]any
	if yaml.Unmarshal([]byte(raw), &next) != nil || yaml.Unmarshal([]byte(original), &old) != nil || yaml.Unmarshal([]byte(original), &mask) != nil {
		return "", errors.New("Cannot read Compose YAML")
	}
	redactCompose(mask)
	value, err := mergeHidden(next, old, mask)
	if err != nil {
		return "", err
	}
	return composeSource(raw, value.(map[string]any))
}

func (l *stackLibrary) edit(ctx context.Context, name string, input editInput, save bool) (StackRecord, error) {
	record, err := l.load(name)
	if err != nil {
		return record, err
	}
	if !record.managed() {
		return record, errors.New("Imported copies remain read-only until an explicitly confirmed takeover completes")
	}
	if l.isBusy(name) {
		return record, errors.New("Wait for the active operation before editing this stack")
	}
	expected := recordStamp(record)
	files, err := l.content(record)
	if err != nil {
		return record, err
	}
	if input.Revision == "" || input.Revision != filesFingerprint(files) {
		return record, errors.New("This definition changed; reopen it before saving")
	}
	originalFingerprint := filesFingerprint(files)
	compose, err := restoreHidden(input.Compose, files["compose.yaml"])
	if err != nil {
		return record, err
	}
	files["compose.yaml"] = compose
	if input.ReplaceEnvironment {
		files[".env"], files["stack.env"] = input.Environment, input.Environment
	}
	validated, err := l.validateBundle(ctx, name, files)
	if err != nil {
		return record, err
	}
	// Compose validation and queueing must not hold the library mutation lock.
	// Recheck metadata as well as content so a completed deployment or another
	// same-content save cannot be overwritten by this older editor request.
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return record, err
	}
	if l.isBusy(name) {
		return record, errors.New("Wait for the active operation before editing this stack")
	}
	if _, err := l.unchangedRecord(name, expected); err != nil {
		return record, err
	}
	record.Services, record.Warnings, record.EnvironmentNames = validated.Services, validated.Warnings, validated.EnvironmentNames
	record.Fingerprint = filesFingerprint(files)
	if !save {
		return record, nil
	}
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	record.EditVersion++
	if record.Fingerprint == originalFingerprint {
		if err := l.writeRecord(record); err != nil {
			return record, errors.New("Cannot confirm configuration save; reopen the definition before retrying")
		}
		return record, nil
	}
	record.ConfigRevision = record.Fingerprint
	dir, _ := l.contentDirectory(record)
	if err = ensurePrivateDirectory(filepath.Dir(dir)); err != nil {
		return record, errors.New("Cannot create revision storage")
	}
	if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
		temp, mkErr := os.MkdirTemp(filepath.Dir(dir), ".revision-")
		if mkErr != nil {
			return record, errors.New("Cannot stage revision")
		}
		defer os.RemoveAll(temp)
		for filename, body := range files {
			if err = writePrivateFile(filepath.Join(temp, filename), []byte(body)); err != nil {
				return record, errors.New("Cannot store revision")
			}
		}
		if err = syncDirectory(temp); err != nil {
			return record, errors.New("Cannot sync staged revision")
		}
		if err = os.Rename(temp, dir); err != nil {
			return record, errors.New("Cannot finalize revision")
		}
		if err = syncDirectory(filepath.Dir(dir)); err != nil {
			return record, errors.New("Cannot sync revision storage")
		}
	} else if statErr != nil {
		return record, errors.New("Cannot access revision storage")
	}
	if err = l.writeRecord(record); err != nil {
		return record, errors.New("Cannot confirm configuration save; reopen the definition before retrying")
	}
	return record, nil
}
