package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const monitorTestID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func logFrame(stream byte, text string) []byte {
	frame := make([]byte, 8+len(text))
	frame[0] = stream
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(text)))
	copy(frame[8:], text)
	return frame
}
func TestLogDecoding(t *testing.T) {
	stamp := "2026-10-16T10:00:00.123456789Z"
	raw := append(logFrame(1, stamp+" hel"), logFrame(1, "lo\n")...)
	raw = append(raw, logFrame(2, stamp+" <script>alert(1)</script>\n")...)
	raw = append(raw, logFrame(1, "partial")...)
	got, err := decodeContainerLogs(raw, false, 300)
	if err != nil || len(got.Lines) != 3 || got.Lines[0].Time != stamp || got.Lines[0].Text != "hello" || got.Lines[1].Stream != "stderr" || got.Lines[2].Text != "partial" {
		t.Fatalf("Invalid framed logs: %+v, %v", got, err)
	}
	got, err = decodeContainerLogs([]byte(stamp+" tty\n\x00last\r\n"), true, 1)
	if err != nil || len(got.Lines) != 1 || got.Lines[0].Text != "last" || got.Lines[0].Stream != "console" || !got.Truncated {
		t.Fatalf("Invalid TTY tail: %+v %v", got, err)
	}
	for _, bad := range [][]byte{{1, 0}, {3, 0, 0, 0, 0, 0, 0, 0}, logFrame(1, "abc")[:9]} {
		if _, err := decodeContainerLogs(bad, false, 300); err == nil {
			t.Fatal("Invalid log frame accepted")
		}
	}
	huge := logFrame(1, strings.Repeat("x", maxLogBytes*2))
	got, err = decodeContainerLogs(huge, false, 300)
	if err != nil || !got.Truncated || len(got.Lines) != 1 || len(got.Lines[0].Text) > maxLogBytes {
		t.Fatalf("Large log was not safely bounded: %v", err)
	}
}
func TestResourceCalculations(t *testing.T) {
	var raw dockerStats
	err := json.Unmarshal([]byte(`{"read":"2026-10-16T10:00:00Z","cpu_stats":{"cpu_usage":{"total_usage":500},"system_cpu_usage":3000,"online_cpus":4},"precpu_stats":{"cpu_usage":{"total_usage":200},"system_cpu_usage":2000},"memory_stats":{"usage":1000,"limit":2000,"stats":{"inactive_file":200,"total_inactive_file":100}},"networks":{"eth0":{"rx_bytes":10,"tx_bytes":20},"eth1":{"rx_bytes":30,"tx_bytes":40}},"blkio_stats":{"io_service_bytes_recursive":[{"op":"Read","value":100},{"op":"write","value":200},{"op":"Total","value":300}]},"pids_stats":{"current":5}}`), &raw)
	if err != nil {
		t.Fatal(err)
	}
	got := summarizeStats(raw)
	if !got.Available || got.CPUPercent == nil || *got.CPUPercent != 120 || got.MemoryUsage != 800 || got.MemoryPercent != 40 || got.NetworkRx != 40 || got.NetworkTx != 60 || got.BlockRead != 100 || got.BlockWrite != 200 || got.PIDs != 5 {
		t.Fatalf("Incorrect stats: %+v", got)
	}
	delete(raw.Memory.Stats, "inactive_file")
	raw.CPU.CPUUsage.TotalUsage = 100
	got = summarizeStats(raw)
	if got.CPUPercent != nil || got.MemoryUsage != 900 {
		t.Fatal("Counter reset or cgroup v1 cache calculation failed")
	}
	raw.PreviousCPU.SystemCPUUsage = 0
	raw.Memory.Stats["total_inactive_file"] = 3000
	got = summarizeStats(raw)
	if got.CPUPercent != nil || got.MemoryUsage != 1000 {
		t.Fatal("First sample or invalid cache caused underflow")
	}
	if summarizeStats(dockerStats{}).Available {
		t.Fatal("Stopped container produced a live sample")
	}
}
func TestMonitoringReadOnlyAndAllowlisted(t *testing.T) {
	requests := []string{}
	docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("Diagnostics attempted a Docker mutation")
		}
		requests = append(requests, r.URL.RequestURI())
		switch r.URL.Path {
		case "/version":
			io.WriteString(w, `{"ApiVersion":"1.55","MinAPIVersion":"1.40"}`)
		case "/v1.55/containers/" + monitorTestID + "/json":
			io.WriteString(w, `{"Id":"`+monitorTestID+`","Name":"/web","Image":"sha256:image","Created":"2026-10-15T00:00:00Z","RestartCount":2,"Config":{"Image":"example/web","Tty":false,"Env":["PASSWORD=do-not-expose"],"Cmd":["do-not-expose"],"Labels":{"com.docker.compose.project":"notes","com.docker.compose.service":"web","private.token":"do-not-expose"},"Healthcheck":{"Test":["do-not-expose"]}},"State":{"Status":"running","Health":{"Status":"unhealthy","FailingStreak":3,"Log":[{"End":"2026-10-16T10:00:00Z","ExitCode":1,"Output":"do-not-expose"}]}},"HostConfig":{"RestartPolicy":{"Name":"always"},"LogConfig":{"Type":"local","Config":{"token":"do-not-expose"}}},"NetworkSettings":{"Ports":{"80/tcp":[{"HostIp":"::1","HostPort":"8080"}]}},"Mounts":[{"Type":"volume","Source":"/var/lib/docker/volumes/data/_data","Destination":"/data","RW":true}]}`)
		case "/v1.55/containers/" + monitorTestID + "/logs":
			since, sinceErr := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
			if sinceErr != nil || time.Since(time.Unix(since, 0)) < 299*time.Second || time.Since(time.Unix(since, 0)) > 305*time.Second {
				t.Error("Docker log time filter must use Unix seconds for the selected range")
			}
			if r.URL.Query().Get("tail") != "100" || r.URL.Query().Get("follow") != "false" || r.URL.Query().Get("since") == "" || r.URL.Query().Get("details") != "" {
				t.Error("Unsafe or unbounded Docker log request")
			}
			w.Write(logFrame(1, "2026-10-16T10:00:00Z fixture log\n"))
		case "/v1.55/containers/" + monitorTestID + "/stats":
			if r.URL.Query().Get("stream") != "false" {
				t.Error("Unbounded Docker stats stream")
			}
			io.WriteString(w, `{}`)
		default:
			t.Errorf("Unexpected Docker path: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer docker.Close()
	source := &dockerSource{client: docker.Client(), base: docker.URL}
	app := newHandler(source)
	for _, section := range []string{"details", "logs?tail=100&range=5m", "stats"} {
		r := httptest.NewRequest("GET", "http://localhost/api/containers/"+monitorTestID+"/"+section, nil)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != 200 || strings.Contains(w.Body.String(), "do-not-expose") {
			t.Fatalf("Bad diagnostics response %d: %s", w.Code, w.Body.String())
		}
		if strings.HasPrefix(section, "details") {
			var detail ContainerDetails
			json.Unmarshal(w.Body.Bytes(), &detail)
			if detail.Health != "unhealthy" || detail.LastHealthExit == nil || *detail.LastHealthExit != 1 || detail.Ports[0] != "[::1]:8080 → 80/tcp" || detail.RestartCount != 2 || len(detail.Mounts) != 1 {
				t.Fatalf("Wrong details: %+v", detail)
			}
		}
	}
	before := len(requests)
	for _, test := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/api/containers/not-an-id/details", 400},
		{"GET", "/api/containers/" + monitorTestID + "/logs?tail=all", 400},
		{"GET", "/api/containers/" + monitorTestID + "/logs?tail=2001", 400},
		{"GET", "/api/containers/" + monitorTestID + "/logs?range=forever", 400},
		{"GET", "/api/containers/" + monitorTestID + "/exec", 404},
		{"POST", "/api/containers/" + monitorTestID + "/logs", 405},
	} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest(test.method, "http://localhost"+test.path, nil))
		if w.Code != test.want {
			t.Errorf("%s got %d", test.path, w.Code)
		}
	}
	if len(requests) != before {
		t.Fatal("Invalid request reached Docker")
	}
}
func TestMonitoringErrorsAndCancellation(t *testing.T) {
	for _, code := range []int{404, 500} {
		docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "private-credential", code) }))
		source := &dockerSource{client: docker.Client(), base: docker.URL}
		_, err := source.monitorRequest(context.Background(), "/anything")
		if err == nil || strings.Contains(err.Error(), "private-credential") {
			t.Fatal("Docker error body was exposed")
		}
		docker.Close()
	}
	cancelled := make(chan struct{})
	docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done(); close(cancelled) }))
	defer docker.Close()
	source := &dockerSource{client: docker.Client(), base: docker.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := source.monitorRequest(ctx, "/logs"); err == nil {
		t.Fatal("Cancelled diagnostics succeeded")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("Docker request did not stop on cancellation")
	}
}
func TestDemoMonitoringAndLogBound(t *testing.T) {
	app := newHandler(demoSource{})
	for _, section := range []string{"details", "stats", "logs"} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/containers/demo-3/"+section, nil))
		if w.Code != 200 {
			t.Fatalf("Demo %s failed: %s", section, w.Body)
		}
	}
	got, err := decodeContainerLogs(bytes.Repeat([]byte("line\n"), maxLogBytes), true, 2000)
	if err != nil || !got.Truncated || len(got.Lines) > 2000 {
		t.Fatal("TTY log bound failed")
	}
}
