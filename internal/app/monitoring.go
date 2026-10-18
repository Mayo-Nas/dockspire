package app

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const maxLogBytes = 256 << 10

type containerMonitor interface {
	Details(context.Context, string) (ContainerDetails, error)
	Logs(context.Context, string, LogOptions) (ContainerLogs, error)
	Stats(context.Context, string) (ContainerStats, error)
}

type ContainerDetails struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	Image          string           `json:"image"`
	ImageID        string           `json:"imageId"`
	State          string           `json:"state"`
	Health         string           `json:"health"`
	FailingStreak  int              `json:"failingStreak"`
	LastHealthAt   string           `json:"lastHealthAt,omitempty"`
	LastHealthExit *int             `json:"lastHealthExit,omitempty"`
	RestartCount   int              `json:"restartCount"`
	ExitCode       int              `json:"exitCode"`
	OOMKilled      bool             `json:"oomKilled"`
	CreatedAt      string           `json:"createdAt"`
	StartedAt      string           `json:"startedAt"`
	FinishedAt     string           `json:"finishedAt"`
	RestartPolicy  string           `json:"restartPolicy"`
	Stack          string           `json:"stack"`
	Service        string           `json:"service"`
	TTY            bool             `json:"tty"`
	LogDriver      string           `json:"logDriver"`
	Ports          []string         `json:"ports"`
	Mounts         []ContainerMount `json:"mounts"`
}
type ContainerMount struct {
	Type        string `json:"type"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	ReadOnly    bool   `json:"readOnly"`
}
type LogOptions struct {
	Tail  int
	Since string
}
type LogLine struct {
	Time   string `json:"time,omitempty"`
	Stream string `json:"stream"`
	Text   string `json:"text"`
}
type ContainerLogs struct {
	Lines     []LogLine `json:"lines"`
	Truncated bool      `json:"truncated"`
	CheckedAt string    `json:"checkedAt"`
}
type ContainerStats struct {
	Available     bool     `json:"available"`
	ReadAt        string   `json:"readAt"`
	CPUPercent    *float64 `json:"cpuPercent"`
	MemoryUsage   uint64   `json:"memoryUsage"`
	MemoryLimit   uint64   `json:"memoryLimit"`
	MemoryPercent float64  `json:"memoryPercent"`
	NetworkRx     uint64   `json:"networkRx"`
	NetworkTx     uint64   `json:"networkTx"`
	BlockRead     uint64   `json:"blockRead"`
	BlockWrite    uint64   `json:"blockWrite"`
	PIDs          uint64   `json:"pids"`
}
type monitorError struct {
	status  int
	message string
}

func (e monitorError) Error() string { return e.message }

func containerHealth(status string) string {
	for _, value := range []string{"unhealthy", "healthy"} {
		if strings.Contains(status, "("+value+")") {
			return value
		}
	}
	if strings.Contains(status, "(health: starting)") {
		return "starting"
	}
	return "none"
}
func (d *dockerSource) monitorPrefix(ctx context.Context) (string, error) {
	var version struct{ ApiVersion, MinAPIVersion string }
	if err := d.get(ctx, "/version", &version); err != nil {
		return "", err
	}
	api, err := negotiateAPI(version.ApiVersion, version.MinAPIVersion)
	return "/v" + api, err
}
func (d *dockerSource) monitorRequest(ctx context.Context, path string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, d.base+path, nil)
	if err != nil {
		return nil, errors.New("Cannot prepare diagnostics request")
	}
	response, err := d.client.Do(request)
	if err != nil {
		return nil, errors.New("Docker diagnostics are unavailable; check the connection and retry")
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		if response.StatusCode == 404 {
			return nil, monitorError{404, "Container no longer exists; refresh the inventory"}
		}
		return nil, monitorError{502, "Docker could not provide these diagnostics; the logging driver or host may not support this view"}
	}
	return response, nil
}
func (d *dockerSource) monitorJSON(ctx context.Context, path string, out any) error {
	response, err := d.monitorRequest(ctx, path)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(out) != nil {
		return errors.New("Docker returned invalid diagnostics")
	}
	return nil
}
func (d *dockerSource) detailsAt(ctx context.Context, prefix, id string) (ContainerDetails, error) {
	// Decode only display fields. Never return environment, command, label maps,
	// health-check commands/output, or logging-driver options to the browser.
	var raw struct {
		ID, Name, Image, Created string
		RestartCount             int
		Config                   struct {
			Image  string
			Tty    bool
			Labels map[string]string
		}
		State struct {
			Status, StartedAt, FinishedAt string
			ExitCode                      int
			OOMKilled                     bool
			Health                        *struct {
				Status        string
				FailingStreak int
				Log           []struct {
					End      string
					ExitCode int
				}
			}
		}
		HostConfig struct {
			RestartPolicy struct{ Name string }
			LogConfig     struct{ Type string }
		}
		NetworkSettings struct {
			Ports map[string][]struct{ HostIp, HostPort string }
		}
		Mounts []struct {
			Type, Source, Destination string
			RW                        bool
		}
	}
	if err := d.monitorJSON(ctx, prefix+"/containers/"+id+"/json", &raw); err != nil {
		return ContainerDetails{}, err
	}
	result := ContainerDetails{ID: raw.ID, Name: strings.TrimPrefix(raw.Name, "/"), Image: raw.Config.Image, ImageID: raw.Image, State: raw.State.Status,
		Health: "none", RestartCount: raw.RestartCount, ExitCode: raw.State.ExitCode, OOMKilled: raw.State.OOMKilled,
		CreatedAt: raw.Created, StartedAt: raw.State.StartedAt, FinishedAt: raw.State.FinishedAt, RestartPolicy: raw.HostConfig.RestartPolicy.Name,
		Stack: raw.Config.Labels["com.docker.compose.project"], Service: raw.Config.Labels["com.docker.compose.service"], TTY: raw.Config.Tty, LogDriver: raw.HostConfig.LogConfig.Type,
		Ports: []string{}, Mounts: []ContainerMount{}}
	if h := raw.State.Health; h != nil {
		result.Health, result.FailingStreak = h.Status, h.FailingStreak
		if len(h.Log) > 0 {
			last := h.Log[len(h.Log)-1]
			result.LastHealthAt = last.End
			result.LastHealthExit = &last.ExitCode
		}
	}
	for port, bindings := range raw.NetworkSettings.Ports {
		if len(bindings) == 0 {
			result.Ports = append(result.Ports, port+" (exposed)")
		}
		for _, binding := range bindings {
			result.Ports = append(result.Ports, fmt.Sprintf("%s → %s", net.JoinHostPort(binding.HostIp, binding.HostPort), port))
		}
	}
	sort.Strings(result.Ports)
	for _, mount := range raw.Mounts {
		result.Mounts = append(result.Mounts, ContainerMount{mount.Type, mount.Source, mount.Destination, !mount.RW})
	}
	return result, nil
}
func (d *dockerSource) Details(ctx context.Context, id string) (ContainerDetails, error) {
	prefix, err := d.monitorPrefix(ctx)
	if err != nil {
		return ContainerDetails{}, err
	}
	return d.detailsAt(ctx, prefix, id)
}

func (d *dockerSource) Logs(ctx context.Context, id string, options LogOptions) (ContainerLogs, error) {
	result := ContainerLogs{Lines: []LogLine{}, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	prefix, err := d.monitorPrefix(ctx)
	if err != nil {
		return result, err
	}
	details, err := d.detailsAt(ctx, prefix, id)
	if err != nil {
		return result, err
	}
	query := url.Values{"stdout": {"true"}, "stderr": {"true"}, "timestamps": {"true"}, "follow": {"false"}, "tail": {strconv.Itoa(options.Tail)}}
	if options.Since != "" {
		query.Set("since", options.Since)
	}
	response, err := d.monitorRequest(ctx, prefix+"/containers/"+id+"/logs?"+query.Encode())
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxLogBytes+1))
	if err != nil {
		return result, errors.New("Container log retrieval was interrupted; retry the view")
	}
	return decodeContainerLogs(raw, details.TTY, options.Tail)
}
func decodeContainerLogs(raw []byte, tty bool, tail int) (ContainerLogs, error) {
	result := ContainerLogs{Lines: []LogLine{}, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	byteLimited := len(raw) > maxLogBytes
	if byteLimited {
		raw = raw[:maxLogBytes]
		result.Truncated = true
	}
	pending := map[string]string{}
	appendLine := func(stream, text string) {
		line := LogLine{Stream: stream, Text: strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\t' {
				return -1
			}
			return r
		}, text)}
		if stamp, text, ok := strings.Cut(line.Text, " "); ok {
			if _, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
				line.Time = stamp
				line.Text = text
			}
		}
		result.Lines = append(result.Lines, line)
		// Bound decoded-line memory as well as the raw Docker response.
		if len(result.Lines) >= 2*tail {
			copy(result.Lines, result.Lines[len(result.Lines)-tail:])
			result.Lines = result.Lines[:tail]
			result.Truncated = true
		}
	}
	consume := func(stream string, data []byte) {
		parts := strings.Split(pending[stream]+string(data), "\n")
		for _, text := range parts[:len(parts)-1] {
			appendLine(stream, text)
		}
		pending[stream] = parts[len(parts)-1]
	}
	if tty {
		consume("console", raw)
	} else {
		for len(raw) > 0 {
			if len(raw) < 8 {
				if byteLimited {
					break
				}
				return result, errors.New("Docker returned an incomplete log frame")
			}
			if (raw[0] != 1 && raw[0] != 2) || raw[1] != 0 || raw[2] != 0 || raw[3] != 0 {
				return result, errors.New("Docker returned an unsupported log format")
			}
			size := uint64(binary.BigEndian.Uint32(raw[4:8]))
			stream := "stdout"
			if raw[0] == 2 {
				stream = "stderr"
			}
			raw = raw[8:]
			if size > uint64(len(raw)) {
				if !byteLimited {
					return result, errors.New("Docker returned an incomplete log frame")
				}
				consume(stream, raw)
				break
			}
			consume(stream, raw[:int(size)])
			raw = raw[int(size):]
		}
	}
	for _, stream := range []string{"stdout", "stderr", "console"} {
		if pending[stream] != "" {
			appendLine(stream, pending[stream])
		}
	}
	if len(result.Lines) > tail {
		result.Lines = result.Lines[len(result.Lines)-tail:]
		result.Truncated = true
	}
	return result, nil
}

type dockerCPU struct {
	CPUUsage struct {
		TotalUsage  uint64   `json:"total_usage"`
		PerCPUUsage []uint64 `json:"percpu_usage"`
	} `json:"cpu_usage"`
	SystemCPUUsage uint64 `json:"system_cpu_usage"`
	OnlineCPUs     uint64 `json:"online_cpus"`
}
type dockerStats struct {
	Read        string    `json:"read"`
	CPU         dockerCPU `json:"cpu_stats"`
	PreviousCPU dockerCPU `json:"precpu_stats"`
	Memory      struct {
		Usage, Limit uint64
		Stats        map[string]uint64
	} `json:"memory_stats"`
	Networks map[string]struct {
		Rx uint64 `json:"rx_bytes"`
		Tx uint64 `json:"tx_bytes"`
	} `json:"networks"`
	Block struct {
		Entries []struct {
			Op    string
			Value uint64
		} `json:"io_service_bytes_recursive"`
	} `json:"blkio_stats"`
	PIDs struct{ Current uint64 } `json:"pids_stats"`
}

func summarizeStats(raw dockerStats) ContainerStats {
	result := ContainerStats{ReadAt: raw.Read, MemoryUsage: raw.Memory.Usage, MemoryLimit: raw.Memory.Limit, PIDs: raw.PIDs.Current}
	read, err := time.Parse(time.RFC3339Nano, raw.Read)
	result.Available = err == nil && !read.IsZero()
	cache := raw.Memory.Stats["total_inactive_file"]
	if value, ok := raw.Memory.Stats["inactive_file"]; ok {
		cache = value
	}
	if cache < result.MemoryUsage {
		result.MemoryUsage -= cache
	}
	if result.MemoryLimit > 0 {
		result.MemoryPercent = float64(result.MemoryUsage) / float64(result.MemoryLimit) * 100
	}
	cpus := raw.CPU.OnlineCPUs
	if cpus == 0 {
		cpus = uint64(len(raw.CPU.CPUUsage.PerCPUUsage))
	}
	if cpus > 0 && raw.CPU.SystemCPUUsage > raw.PreviousCPU.SystemCPUUsage && raw.CPU.CPUUsage.TotalUsage >= raw.PreviousCPU.CPUUsage.TotalUsage && raw.PreviousCPU.SystemCPUUsage > 0 {
		value := float64(raw.CPU.CPUUsage.TotalUsage-raw.PreviousCPU.CPUUsage.TotalUsage) / float64(raw.CPU.SystemCPUUsage-raw.PreviousCPU.SystemCPUUsage) * float64(cpus) * 100
		result.CPUPercent = &value
	}
	for _, network := range raw.Networks {
		result.NetworkRx += network.Rx
		result.NetworkTx += network.Tx
	}
	for _, entry := range raw.Block.Entries {
		switch strings.ToLower(entry.Op) {
		case "read":
			result.BlockRead += entry.Value
		case "write":
			result.BlockWrite += entry.Value
		}
	}
	return result
}
func (d *dockerSource) Stats(ctx context.Context, id string) (ContainerStats, error) {
	prefix, err := d.monitorPrefix(ctx)
	if err != nil {
		return ContainerStats{}, err
	}
	var raw dockerStats
	if err = d.monitorJSON(ctx, prefix+"/containers/"+id+"/stats?stream=false", &raw); err != nil {
		return ContainerStats{}, err
	}
	return summarizeStats(raw), nil
}

func registerMonitoringRoutes(mux *http.ServeMux, source inventorySource) {
	monitor, ok := source.(containerMonitor)
	slots := make(chan struct{}, 4)
	idPattern := regexp.MustCompile(`^[a-f0-9]{64}$`)
	_, demo := source.(demoSource)
	mux.HandleFunc("GET /api/containers/{id}/{section}", func(w http.ResponseWriter, r *http.Request) {
		id, section := r.PathValue("id"), r.PathValue("section")
		if !idPattern.MatchString(id) && !(demo && regexp.MustCompile(`^demo-[1-4]$`).MatchString(id)) {
			writeJSON(w, 400, map[string]string{"error": "Choose a container from the current inventory"})
			return
		}
		if section != "details" && section != "logs" && section != "stats" {
			writeJSON(w, 404, map[string]string{"error": "Unknown diagnostics view"})
			return
		}
		if !ok {
			writeJSON(w, 503, map[string]string{"error": "Container diagnostics are unavailable"})
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			writeJSON(w, 429, map[string]string{"error": "Diagnostics are busy; retry shortly"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		var value any
		var err error
		switch section {
		case "details":
			value, err = monitor.Details(ctx, id)
		case "stats":
			value, err = monitor.Stats(ctx, id)
		case "logs":
			options := LogOptions{Tail: 300}
			query := r.URL.Query()
			if query.Get("tail") != "" {
				options.Tail, err = strconv.Atoi(query.Get("tail"))
				if err != nil || options.Tail < 1 || options.Tail > 2000 {
					writeJSON(w, 400, map[string]string{"error": "Log tail must be between 1 and 2000 lines"})
					return
				}
			}
			switch query.Get("range") {
			case "", "all":
			case "5m":
				options.Since = strconv.FormatInt(time.Now().Add(-5*time.Minute).Unix(), 10)
			case "1h":
				options.Since = strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)
			case "24h":
				options.Since = strconv.FormatInt(time.Now().Add(-24*time.Hour).Unix(), 10)
			default:
				writeJSON(w, 400, map[string]string{"error": "Choose a supported log time range"})
				return
			}
			value, err = monitor.Logs(ctx, id, options)
		}
		if err != nil {
			status := 503
			var diagnostic monitorError
			if errors.As(err, &diagnostic) {
				status = diagnostic.status
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, value)
	})
}
