package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallationReportsMissingAndUnsupportedCompose(t *testing.T) {
	if result := checkComposeBinary(context.Background(), filepath.Join(t.TempDir(), "missing-compose")); result.State != "missing" {
		t.Fatal(result)
	}
	for _, test := range []struct{ raw, state string }{{"v5.5.0\n", "ready"}, {"2.40.3", "ready"}, {"1.29.2", "incompatible"}, {"untrusted text with fixture-secret", "unavailable"}, {strings.Repeat("1", 200), "unavailable"}} {
		result := composeVersionResult([]byte(test.raw), nil)
		if result.State != test.state || strings.Contains(result.Message, "fixture-secret") {
			t.Fatal(result)
		}
	}
	if result := composeVersionResult([]byte("5.5.0"), errors.New("fixture-secret")); result.State != "unavailable" {
		t.Fatal(result)
	}
}
func TestInstallationEndpointWorksWithoutDockerInventory(t *testing.T) {
	request := httptest.NewRequest("GET", "http://localhost/api/system/installation", nil)
	response := httptest.NewRecorder()
	newHandler(failingSource{}).ServeHTTP(response, request)
	var result InstallationStatus
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Ready || len(result.Checks) != 2 {
		t.Fatal(response.Body.String())
	}
}
func TestInstallationDistinguishesEngineCompatibility(t *testing.T) {
	for _, version := range []struct{ body, state string }{{`{"Version":"29.7.2","ApiVersion":"1.55","MinAPIVersion":"1.40"}`, "ready"}, {`{"Version":"99.0.0","ApiVersion":"1.80","MinAPIVersion":"1.79"}`, "incompatible"}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/version" {
				t.Error("installation check fetched unrelated inventory")
			}
			w.Write([]byte(version.body))
		}))
		source := &dockerSource{client: server.Client(), base: server.URL}
		result := installationCheck(context.Background(), source, nil)
		server.Close()
		if result.Checks[0].State != version.state {
			t.Fatal(result)
		}
	}
}
