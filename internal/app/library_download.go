package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

var errArchiveBusy = errors.New("Another archive download is in progress. Wait for it to finish and retry.")
var errArchiveActive = errors.New("Wait for the current container operation to finish, then retry the download.")

type archiveDownload struct {
	file      *os.File
	directory string
	library   *stackLibrary
	once      sync.Once
}

func (download *archiveDownload) close() {
	download.once.Do(func() {
		if download.file != nil {
			download.file.Close()
		}
		if download.directory != "" {
			os.RemoveAll(download.directory)
		}
		<-download.library.archiveSlot
	})
}

// Capturing under both publication locks gives one coherent library state.
// Compression and network writes use private copies after releasing the locks.
// Other processes and direct changes to a live library remain unsupported.
func (l *stackLibrary) snapshotArchive(ctx context.Context, project string) (selection archiveSelection, directory string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.jobsMu.Lock()
	defer l.jobsMu.Unlock()
	if err = ctx.Err(); err != nil {
		return
	}
	if len(l.active) > 0 {
		err = errArchiveActive
		return
	}
	if project != "" && !projectName.MatchString(project) {
		err = errors.New("Invalid stack name")
		return
	}
	if err = ensurePrivateDirectory(l.root); err != nil {
		return
	}
	if err = l.selectArchiveFiles(ctx, project, &selection); err != nil {
		return
	}
	directory, err = os.MkdirTemp(l.root, ".download-")
	if err != nil {
		return
	}
	defer func() {
		if err != nil {
			os.RemoveAll(directory)
		}
	}()
	var total int64
	for name, source := range selection.files {
		if err = ctx.Err(); err != nil {
			return
		}
		var raw []byte
		raw, err = readLimited(source)
		if err != nil {
			return
		}
		total += int64(len(raw))
		if total > archiveLimit {
			err = errors.New("Library exceeds the archive size limit")
			return
		}
		destination := filepath.Join(directory, "files", filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return
		}
		// Staging is disposable, not a durable publication. The completed ZIP is
		// synced before it can be served. One bounded file is held in memory.
		if err = os.WriteFile(destination, raw, 0600); err != nil {
			return
		}
		selection.files[name] = destination
	}
	return
}

func (l *stackLibrary) prepareArchiveDownload(ctx context.Context, project string) (download *archiveDownload, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case l.archiveSlot <- struct{}{}:
	default:
		return nil, errArchiveBusy
	}
	download = &archiveDownload{library: l}
	defer func() {
		if err != nil {
			download.close()
		}
	}()
	var selection archiveSelection
	selection, download.directory, err = l.snapshotArchive(ctx, project)
	if err != nil {
		return
	}
	output := filepath.Join(download.directory, "download.zip")
	if err = writeArchiveFiles(ctx, output, selection); err != nil {
		return
	}
	if err = os.RemoveAll(filepath.Join(download.directory, "files")); err != nil {
		return
	}
	if err = ctx.Err(); err != nil {
		return
	}
	download.file, err = os.Open(output)
	return
}

func registerArchiveRoutes(mux *http.ServeMux, library *stackLibrary) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			IncludeSecrets bool `json:"includeSecrets"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var extra any
		if decoder.Decode(&input) != nil || decoder.Decode(&extra) != io.EOF || !input.IncludeSecrets {
			writeJSON(w, 400, map[string]string{"error": "Download requires acknowledging that the archive contains plaintext secrets."})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
		defer cancel()
		project := r.PathValue("name")
		download, err := library.prepareArchiveDownload(ctx, project)
		if err != nil {
			status, message := http.StatusInternalServerError, "Cannot prepare archive. Check saved library integrity, file permissions, and free disk space, then retry."
			if errors.Is(err, errArchiveBusy) || errors.Is(err, errArchiveActive) {
				status, message = http.StatusConflict, err.Error()
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				status, message = http.StatusRequestTimeout, "Archive preparation was cancelled or timed out. Retry the download."
			}
			writeJSON(w, status, map[string]string{"error": message})
			return
		}
		defer download.close()
		info, err := download.file.Stat()
		if err != nil || ctx.Err() != nil {
			writeJSON(w, 500, map[string]string{"error": "Cannot read the completed archive. Retry the download."})
			return
		}
		filename := "dockspire-library-" + time.Now().UTC().Format("20060102T150405Z") + ".zip"
		if project != "" {
			filename = "dockspire-" + project + "-compose.zip"
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		w.Header().Set("Cache-Control", "no-store")
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(150 * time.Second))
		// A slow client holds only its completed private ZIP and the download
		// slot. It cannot hold metadata locks or create a second archive.
		_, _ = io.Copy(w, download.file)
	}
	mux.HandleFunc("POST /api/library/backup", handler)
	mux.HandleFunc("POST /api/stacks/{name}/export", handler)
}
