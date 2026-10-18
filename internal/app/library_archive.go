package app

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const archiveLimit = 128 << 20
const archiveFileLimit = 20000

type libraryManifest struct {
	Version   int               `json:"version"`
	Kind      string            `json:"kind"`
	CreatedAt string            `json:"createdAt"`
	Project   string            `json:"project,omitempty"`
	Files     map[string]string `json:"files"`
}

type archiveSelection struct {
	manifest libraryManifest
	files    map[string]string
}

func libraryArchivePath(name string) bool {
	if strings.ContainsAny(name, "\\:") {
		return false
	}
	parts := strings.Split(name, "/")
	if len(parts) == 2 && parts[0] == "jobs" {
		return strings.HasSuffix(parts[1], ".json") && validOperationID(strings.TrimSuffix(parts[1], ".json"))
	}
	if len(parts) < 3 || parts[0] != "stacks" || !projectName.MatchString(parts[1]) {
		return false
	}
	file := parts[len(parts)-1]
	config := file == "compose.yaml" || file == ".env" || file == "stack.env"
	return (len(parts) == 3 && (config || file == "record.json")) || (len(parts) == 5 && parts[2] == "revisions" && revisionName.MatchString(parts[3]) && config)
}

// Offline commands deliberately do not construct Docker adapters, run Compose,
// or read any manager's source/credentials. Stop the server before using them.
func (l *stackLibrary) writeArchive(filename, project string) (err error) {
	if info, statErr := os.Lstat(l.root); statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Choose an existing library directory")
	}
	output, err := filepath.Abs(filename)
	if err != nil {
		return errors.New("Invalid archive destination")
	}
	if relative, relErr := filepath.Rel(l.root, output); relErr == nil && (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return errors.New("Choose an archive destination outside the data directory")
	}
	selection := archiveSelection{}
	if err := l.selectArchiveFiles(context.Background(), project, &selection); err != nil {
		return err
	}
	return writeArchiveFiles(context.Background(), output, selection)
}

func (l *stackLibrary) selectArchiveFiles(ctx context.Context, project string, selection *archiveSelection) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if info, err := os.Lstat(l.root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Choose an existing library directory")
	}
	selection.manifest = libraryManifest{Version: 1, Kind: "stacklane-library", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Files: map[string]string{}}
	selection.files = map[string]string{}
	manifest, files := &selection.manifest, selection.files
	if project != "" {
		base, err := l.directory(project)
		if err != nil || !safeArchiveSource(l.root, filepath.Join(base, "record.json")) {
			return errors.New("Choose an available saved stack")
		}
		record, loadErr := l.load(project)
		if loadErr != nil {
			return loadErr
		}
		dir, _ := l.contentDirectory(record)
		for _, name := range []string{"compose.yaml", ".env", "stack.env"} {
			if _, statErr := os.Lstat(filepath.Join(dir, name)); os.IsNotExist(statErr) && name != "compose.yaml" {
				continue
			}
			files[name] = filepath.Join(dir, name)
		}
		manifest.Kind, manifest.Project = "compose-export", project
		files["stacklane-record.json"] = filepath.Join(base, "record.json")
	} else {
		for _, group := range []string{"stacks", "jobs"} {
			directory := filepath.Join(l.root, group)
			if _, statErr := os.Lstat(directory); os.IsNotExist(statErr) {
				continue
			}
			if err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if walkErr != nil {
					return errors.New("Cannot read library files")
				}
				if entry.Type()&os.ModeSymlink != 0 {
					return errors.New("Library backups do not follow symbolic links")
				}
				if entry.IsDir() {
					if strings.HasPrefix(entry.Name(), ".") {
						return filepath.SkipDir
					}
					return nil
				}
				relative, _ := filepath.Rel(l.root, path)
				name := filepath.ToSlash(relative)
				if libraryArchivePath(name) {
					if len(files) >= archiveFileLimit {
						return errors.New("Library exceeds the archive file limit")
					}
					files[name] = path
				}
				return nil
			}); err != nil {
				return err
			}
		}
	}
	if len(files) > archiveFileLimit {
		return errors.New("Library exceeds the archive file limit")
	}
	var size int64
	for _, source := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !safeArchiveSource(l.root, source) {
			return errors.New("Library archives do not follow symbolic links or non-regular files")
		}
		info, err := os.Lstat(source)
		if err != nil || info.Size() > maxDefinition {
			return errors.New("A library file exceeds the archive file size limit")
		}
		size += info.Size()
		if size > archiveLimit {
			return errors.New("Library exceeds the archive size limit")
		}
	}
	if project == "" {
		return validateArchiveLibraryContext(ctx, l)
	}
	return nil
}

func safeArchiveSource(root, source string) bool {
	relative, err := filepath.Rel(root, source)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	path := root
	parts := strings.Split(relative, string(filepath.Separator))
	for i, part := range parts {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if i == len(parts)-1 {
			return info.Mode().IsRegular()
		}
		if !info.IsDir() {
			return false
		}
	}
	return false
}

func writeArchiveFiles(ctx context.Context, output string, selection archiveSelection) (err error) {
	manifest, files := selection.manifest, selection.files
	file, err := os.CreateTemp(filepath.Dir(output), ".stacklane-backup-")
	if err != nil {
		return errors.New("Cannot create private archive")
	}
	temp := file.Name()
	defer os.Remove(temp)
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return err
	}
	writer := zip.NewWriter(file)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var total int64
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw, readErr := readLimited(files[name])
		if readErr != nil {
			return errors.New("Cannot safely read a library file")
		}
		total += int64(len(raw))
		if total > archiveLimit {
			return errors.New("Library exceeds the archive size limit")
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0600)
		entry, createErr := writer.CreateHeader(header)
		if createErr != nil {
			return createErr
		}
		if _, err := entry.Write(raw); err != nil {
			return err
		}
		digest := sha256.Sum256(raw)
		manifest.Files[name] = hex.EncodeToString(digest[:])
	}
	metadata, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil || len(metadata) > 4<<20 {
		return errors.New("Cannot encode archive manifest")
	}
	entry, err := writer.Create("manifest.json")
	if err != nil {
		return err
	}
	if _, err = entry.Write(metadata); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if info, err := file.Stat(); err != nil || info.Size() > archiveLimit+(4<<20) {
		return errors.New("Completed archive exceeds the archive size limit")
	}
	// Catch concurrent modification rather than acknowledge a mixed snapshot.
	// Operators must still stop the server; this is not a multi-process lock.
	for name, source := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw, err := readLimited(source)
		if err != nil || fingerprint(string(raw)) != manifest.Files[name] {
			return errors.New("Library changed during backup; stop the server and retry")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	// Hard links publish without another copy on local filesystems. Removable
	// drives and some shares need an exclusive-create copy instead. Both refuse
	// existing destinations; a failed copy is removed, and restore verifies CRCs
	// and content digests rather than accepting an incomplete archive.
	if err := os.Link(temp, output); err != nil {
		if err := copyNewArchive(temp, output); err != nil {
			return err
		}
	}
	return syncDirectory(filepath.Dir(output))
}

func copyNewArchive(source, destination string) (err error) {
	input, err := os.Open(source)
	if err != nil {
		return errors.New("Cannot read completed archive")
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("Cannot publish archive; destination must be unused")
	}
	defer func() {
		output.Close()
		if err != nil {
			os.Remove(destination)
		}
	}()
	if _, err = io.Copy(output, input); err != nil {
		return errors.New("Cannot copy completed archive")
	}
	if err = output.Sync(); err != nil {
		return errors.New("Cannot sync completed archive")
	}
	return output.Close()
}

func validateArchiveLibrary(l *stackLibrary) error {
	return validateArchiveLibraryContext(context.Background(), l)
}

func validateArchiveLibraryContext(ctx context.Context, l *stackLibrary) error {
	records, err := l.list()
	if err != nil {
		return err
	}
	available := map[string]map[string]bool{}
	for _, item := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		record, err := l.load(item.Name)
		if err != nil {
			return errors.New("Cannot archive a library with unavailable stack metadata")
		}
		initial := record
		initial.ConfigRevision = ""
		files, err := l.content(initial)
		if err != nil {
			return err
		}
		revisions := map[string]bool{filesFingerprint(files): true}
		base, _ := l.directory(record.Name)
		entries, readErr := os.ReadDir(filepath.Join(base, "revisions"))
		if readErr != nil && !os.IsNotExist(readErr) {
			return errors.New("Cannot read revision storage")
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !entry.IsDir() || !revisionName.MatchString(entry.Name()) {
				continue
			}
			revision := record
			revision.ConfigRevision = entry.Name()
			content, err := l.content(revision)
			if err != nil || filesFingerprint(content) != entry.Name() {
				return errors.New("A saved configuration revision is incomplete or damaged")
			}
			revisions[entry.Name()] = true
		}
		if record.ConfigRevision != "" && !revisions[record.ConfigRevision] {
			return errors.New("Selected configuration is missing")
		}
		if record.AppliedRevision != "" && !revisions[record.AppliedRevision] {
			return errors.New("Applied configuration is missing")
		}
		if record.Takeover != nil && !revisions[record.Takeover.Revision] {
			return errors.New("Takeover configuration is missing")
		}
		if record.LastDeploymentAttempt != nil && !revisions[record.LastDeploymentAttempt.Revision] {
			return errors.New("Recovery configuration is missing")
		}
		available[record.Name] = revisions
	}
	jobs, err := os.ReadDir(filepath.Join(l.root, "jobs"))
	if err != nil && !os.IsNotExist(err) {
		return errors.New("Cannot read operation history")
	}
	for _, entry := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		job, err := l.readJobFile(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			continue
		}
		revisions := available[job.Name]
		if revisions == nil {
			continue
		}
		for _, revision := range []string{job.Revision, job.PreviousAppliedRevision} {
			if revisionName.MatchString(revision) && !revisions[revision] {
				return errors.New("An operation's configuration revision is missing")
			}
		}
	}
	return nil
}

func restoreLibrary(filename, destination string) error {
	if info, err := os.Lstat(filename); err != nil || !info.Mode().IsRegular() || info.Size() > archiveLimit+(4<<20) {
		return errors.New("Choose a regular backup file within the archive size limit")
	}
	root, err := filepath.Abs(destination)
	if err != nil {
		return errors.New("Invalid restore destination")
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		return errors.New("Restore requires a data directory that does not exist; existing data is never replaced")
	}
	reader, err := zip.OpenReader(filename)
	if err != nil {
		return errors.New("Cannot read backup archive")
	}
	defer reader.Close()
	if len(reader.File) > archiveFileLimit+1 {
		return errors.New("Backup contains too many files")
	}
	entries := map[string]*zip.File{}
	for _, file := range reader.File {
		if _, duplicate := entries[file.Name]; duplicate || !file.Mode().IsRegular() {
			return errors.New("Backup contains duplicate or non-regular entries")
		}
		if file.Name != "manifest.json" && !libraryArchivePath(file.Name) {
			return errors.New("Backup contains an unsupported path")
		}
		entries[file.Name] = file
	}
	metadata := entries["manifest.json"]
	if metadata == nil || metadata.UncompressedSize64 > 4<<20 {
		return errors.New("Backup manifest is missing or too large")
	}
	stream, err := metadata.Open()
	if err != nil {
		return errors.New("Cannot read backup manifest")
	}
	raw, err := io.ReadAll(io.LimitReader(stream, (4<<20)+1))
	stream.Close()
	var manifest libraryManifest
	if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Version != 1 || manifest.Kind != "stacklane-library" || len(manifest.Files) != len(entries)-1 {
		return errors.New("Unsupported or incomplete library backup")
	}
	if err := ensurePrivateDirectory(filepath.Dir(root)); err != nil {
		return errors.New("Cannot prepare restore parent")
	}
	stage, err := os.MkdirTemp(filepath.Dir(root), ".stacklane-restore-")
	if err != nil {
		return errors.New("Cannot stage restored library")
	}
	defer os.RemoveAll(stage)
	var total uint64
	for name, digest := range manifest.Files {
		file := entries[name]
		if file == nil || !libraryArchivePath(name) || !revisionName.MatchString(digest) || file.UncompressedSize64 > maxDefinition {
			return errors.New("Backup file manifest is invalid")
		}
		total += file.UncompressedSize64
		if total > archiveLimit {
			return errors.New("Backup exceeds the restore size limit")
		}
		stream, err := file.Open()
		if err != nil {
			return errors.New("Cannot open a backup entry")
		}
		raw, err := io.ReadAll(io.LimitReader(stream, maxDefinition+1))
		stream.Close()
		if err != nil || len(raw) > maxDefinition || fingerprint(string(raw)) != digest {
			return errors.New("Backup file checksum failed")
		}
		path := filepath.Join(stage, filepath.FromSlash(name))
		if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
			return errors.New("Cannot prepare private restore directory")
		}
		if err := writePrivateFile(path, raw); err != nil {
			return errors.New("Cannot write restored file")
		}
	}
	library := newLibrary(stage, "", false, nil, nil)
	if err := validateArchiveLibrary(library); err != nil {
		return err
	}
	if err := filepath.WalkDir(stage, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return syncDirectory(path)
		}
		return nil
	}); err != nil {
		return errors.New("Cannot sync restored library")
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		return errors.New("Restore destination became occupied")
	}
	if err := os.Rename(stage, root); err != nil {
		return errors.New("Cannot publish restored library")
	}
	if err := syncDirectory(filepath.Dir(root)); err != nil {
		return errors.New("Cannot confirm restored library publication")
	}
	fmt.Println("Library restored. No Docker operations were performed.")
	return nil
}
