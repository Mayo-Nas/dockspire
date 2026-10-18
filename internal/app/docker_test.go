package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNegotiateAPI(t *testing.T) {
	for _, test := range []struct {
		name, maximum, minimum, want string
		fail                         bool
	}{
		{"current", "1.55", "1.40", "1.55", false},
		{"newer engine", "1.60", "1.44", "1.55", false},
		{"older supported", "1.44", "1.24", "1.44", false},
		{"minimum too new", "1.60", "1.56", "", true},
		{"maximum too old", "1.43", "1.24", "", true},
		{"invalid", "garbage", "1.44", "", true},
		{"inconsistent", "1.44", "1.55", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := negotiateAPI(test.maximum, test.minimum)
			if (err != nil) != test.fail || got != test.want {
				t.Fatalf("got %q, %v; want %q, fail=%v", got, err, test.want, test.fail)
			}
		})
	}
}

func TestSnapshotNegotiatesAndOmitsSensitiveFields(t *testing.T) {
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected write: %s", r.Method)
		}
		requests = append(requests, r.URL.RequestURI())
		responses := map[string]string{
			"/version":                        `{"Version":"29.7.2","ApiVersion":"1.55","MinAPIVersion":"1.40"}`,
			"/v1.55/info":                     `{"Name":"fixture","NCPU":4,"MemTotal":4294967296,"OperatingSystem":"Linux","Architecture":"x86_64","RegistryConfig":{"secret":"do-not-expose"}}`,
			"/v1.55/containers/json?all=true": `[{"Id":"one","Names":["/web"],"Image":"example/web","State":"running","Status":"Up","Labels":{"com.docker.compose.project":"notes","private.token":"do-not-expose"},"Env":["PASSWORD=do-not-expose"],"Ports":[{"IP":"::1","PublicPort":8080,"PrivatePort":80,"Type":"tcp"}]},{"Id":"two","Names":["/db"],"State":"exited","Labels":{"com.docker.compose.project":"notes"}}]`,
			"/v1.55/images/json":              `[{"Id":"image","RepoTags":null,"Size":100}]`,
			"/v1.55/networks":                 `[{"Id":"network","Name":"bridge","Driver":"bridge","Scope":"local","Options":{"private.token":"do-not-expose"}}]`,
			"/v1.55/volumes":                  `{"Volumes":null}`,
		}
		value, ok := responses[r.URL.RequestURI()]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(value))
	}))
	defer server.Close()
	source := dockerSource{client: server.Client(), base: server.URL}
	got, err := source.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 6 {
		t.Fatalf("unexpected requests: %v", requests)
	}
	if got.Host.API != "1.55" || got.Containers[0].Name != "db" || got.Stacks[0].State != "partial" || got.Stacks[0].Running != 1 || got.Stacks[0].Containers != 2 {
		t.Fatalf("incorrect inventory: %+v", got)
	}
	if got.Containers[1].Ports[0] != "[::1]:8080 → 80/tcp" {
		t.Fatalf("invalid IPv6 port: %v", got.Containers[1].Ports)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "do-not-expose") || strings.Contains(string(encoded), "PASSWORD") {
		t.Fatal("sensitive Docker fields leaked")
	}
	if got.Volumes == nil || got.Images[0].Tags[0] != "<untagged>" {
		t.Fatal("empty Docker resource handling failed")
	}
}

func TestDockerErrorsDoNotExposeResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "private-password", http.StatusForbidden) }))
	defer server.Close()
	source := dockerSource{client: server.Client(), base: server.URL}
	_, err := source.Snapshot(context.Background())
	if err == nil || strings.Contains(err.Error(), "private-password") || !strings.Contains(err.Error(), "403") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestGroupStacksIgnoresStandaloneAndSorts(t *testing.T) {
	stacks := groupStacks([]Container{{Name: "manual", State: "running"}, {Stack: "zeta", State: "exited"}, {Stack: "alpha", State: "running"}})
	if len(stacks) != 2 || stacks[0].Name != "alpha" || stacks[0].State != "running" || stacks[1].State != "stopped" {
		t.Fatalf("incorrect groups: %v", stacks)
	}
}

type failingSource struct{}

func (failingSource) Snapshot(context.Context) (Inventory, error) {
	return Inventory{}, errors.New("Docker unavailable")
}
func (failingSource) ProjectContainers(context.Context, string) ([]Container, error) {
	return nil, errors.New("Docker unavailable")
}

func TestHTTPBoundary(t *testing.T) {
	for _, test := range []struct {
		name, method, path, host string
		status                   int
	}{
		{"inventory", "GET", "/api/inventory", "localhost:8787", 200},
		{"homepage", "GET", "/", "127.0.0.1:8787", 200},
		{"script", "GET", "/app.js", "localhost", 200},
		{"write blocked", "POST", "/api/inventory", "localhost", 405},
		{"delete blocked", "DELETE", "/api/containers/one", "localhost", 405},
		{"remote hostname blocked", "GET", "/api/inventory", "attacker.example", 403},
		{"lookalike blocked", "GET", "/api/inventory", "localhost.attacker.example", 403},
		{"unknown API", "GET", "/api/nope", "localhost", 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Host = test.host
			recorder := httptest.NewRecorder()
			newHandler(demoSource{}).ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("got %d: %s", recorder.Code, recorder.Body.String())
			}
			if recorder.Header().Get("Content-Security-Policy") == "" {
				t.Fatal("missing CSP")
			}
			if test.name == "inventory" && !strings.Contains(recorder.Body.String(), `"mode":"demo"`) {
				t.Fatal("demo is not labeled")
			}
		})
	}
	request := httptest.NewRequest("GET", "/api/inventory", nil)
	request.Host = "localhost"
	recorder := httptest.NewRecorder()
	newHandler(failingSource{}).ServeHTTP(recorder, request)
	if recorder.Code != 503 {
		t.Fatalf("Docker failures must not look successful: %d", recorder.Code)
	}
}
