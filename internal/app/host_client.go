package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type hostHelperClient struct{ client *http.Client }

func newHostHelperClient(socket string) *hostHelperClient {
	return &hostHelperClient{client: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", socket)
	}}, Timeout: 70 * time.Second}}
}
func (c *hostHelperClient) call(ctx context.Context, method, path string, input, output any) error {
	body, _ := json.Marshal(input)
	request, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return errors.New("Host helper is unavailable; check the host service and its socket mount")
	}
	defer response.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(response.Body, 256<<10))
	if response.StatusCode != 200 {
		var problem struct{ Error string }
		if decoder.Decode(&problem) == nil && problem.Error != "" {
			return errors.New(problem.Error)
		}
		return errors.New("Host helper request failed")
	}
	if decoder.Decode(output) != nil {
		return errors.New("Host helper returned an invalid response")
	}
	return nil
}
func (c *hostHelperClient) available(ctx context.Context) error {
	if c == nil {
		return nil
	}
	var status HostStatus
	if err := c.call(ctx, "GET", "/status", nil, &status); err != nil {
		return err
	}
	if status.Active != nil {
		return errors.New("A host package operation is running. Wait before changing containers")
	}
	return nil
}
func registerHostClientRoutes(mux *http.ServeMux, library *stackLibrary) {
	client := func() *hostHelperClient {
		if library == nil {
			return nil
		}
		return library.host
	}
	mux.HandleFunc("GET /api/system/host", func(w http.ResponseWriter, r *http.Request) {
		if client() == nil {
			writeJSON(w, 200, HostStatus{Connected: false, Packages: []HostPackage{}, History: []HostJob{}, Message: "The restricted host helper is not connected"})
			return
		}
		var status HostStatus
		if err := client().call(r.Context(), "GET", "/status", nil, &status); err != nil {
			writeJSON(w, 200, HostStatus{Connected: false, Packages: []HostPackage{}, History: []HostJob{}, Message: err.Error()})
			return
		}
		// Never inherit an installation capability from an older host helper.
		status.UpdatesEnabled = false
		writeJSON(w, 200, status)
	})
	for _, action := range []string{"review", "update"} {
		mux.HandleFunc("POST /api/system/"+action, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": hostUpdatesDisabled})
		})
	}
	mux.HandleFunc("POST /api/system/refresh", func(w http.ResponseWriter, r *http.Request) {
		if client() == nil {
			writeJSON(w, 400, map[string]string{"error": "Host helper is not connected"})
			return
		}
		var input struct{}
		if !hostDecode(w, r, &input) {
			return
		}
		library.mu.Lock()
		defer library.mu.Unlock()
		library.jobsMu.Lock()
		busy := len(library.active) > 0
		library.jobsMu.Unlock()
		if busy {
			writeJSON(w, 400, map[string]string{"error": "Wait for the active stack operation before using host package operations"})
			return
		}
		var output json.RawMessage
		if err := client().call(r.Context(), "POST", "/refresh", input, &output); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, output)
	})
	mux.HandleFunc("GET /api/system/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if client() == nil || len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
			writeJSON(w, 404, map[string]string{"error": "Host operation not found"})
			return
		}
		var job HostJob
		if err := client().call(r.Context(), "GET", "/jobs/"+id, nil, &job); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, job)
	})
}
