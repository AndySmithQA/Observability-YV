package main

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
)

var registerMetricsOnce sync.Once

func ensureMetrics() {
	registerMetricsOnce.Do(func() {
		prometheus.MustRegister(httpRequests, httpLatency, simRequests, simErrors, dbInserts, dbInsertFail, dbLatency, redisEnqueued, redisEnqueueFail)
	})
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	ensureMetrics()
	service := &app{state: newState()}
	mux := http.NewServeMux()
	service.routes(mux)
	server := httptest.NewServer(metricsMiddleware(mux))
	t.Cleanup(server.Close)
	return server
}

func call(t *testing.T, base, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, payload
}

func asObject(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode object: %v\n%s", err, payload)
	}
	return decoded
}

func near(t *testing.T, payload map[string]any, key string, want float64) {
	t.Helper()
	got, ok := payload[key].(float64)
	if !ok || math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %#v, want %v", key, payload[key], want)
	}
}

func TestLivenessAndSnapshotShape(t *testing.T) {
	server := newTestServer(t)
	status, payload := call(t, server.URL, http.MethodGet, "/api/ready", nil)
	if status != http.StatusOK {
		t.Fatalf("ready status %d", status)
	}
	if string(bytes.TrimSpace(payload)) != `{"ready":true}` {
		t.Fatalf("ready body %s", payload)
	}

	status, payload = call(t, server.URL, http.MethodGet, "/api/metrics", nil)
	if status != http.StatusOK {
		t.Fatalf("metrics status %d", status)
	}
	snapshot := asObject(t, payload)
	for _, key := range []string{"ts", "chaos", "requests_per_s", "error_rate", "latency_ms", "availability", "window", "database", "investigation"} {
		if _, ok := snapshot[key]; !ok {
			t.Fatalf("snapshot missing %s", key)
		}
	}
}

func TestChaosDefaultsValidationAndUpdate(t *testing.T) {
	server := newTestServer(t)
	status, payload := call(t, server.URL, http.MethodGet, "/api/chaos", nil)
	if status != http.StatusOK {
		t.Fatalf("get chaos status %d", status)
	}
	defaults := asObject(t, payload)
	near(t, defaults, "server_online", 1)

	status, payload = call(t, server.URL, http.MethodPut, "/api/chaos", map[string]float64{"server_online": 0.4})
	if status != http.StatusOK {
		t.Fatalf("put chaos status %d", status)
	}
	updated := asObject(t, payload)
	near(t, updated, "server_online", 0.4)
	near(t, updated, "traffic_level", 0.35)

	status, payload = call(t, server.URL, http.MethodPut, "/api/chaos", map[string]float64{"server_online": 2})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("invalid chaos status %d", status)
	}
	if _, ok := asObject(t, payload)["detail"]; !ok {
		t.Fatalf("validation body %s", payload)
	}

	status, _ = call(t, server.URL, http.MethodPut, "/api/chaos", defaults)
	if status != http.StatusOK {
		t.Fatalf("restore chaos status %d", status)
	}
}

func TestInvestigationContract(t *testing.T) {
	server := newTestServer(t)
	status, payload := call(t, server.URL, http.MethodGet, "/api/investigation/choices", nil)
	if status != http.StatusOK {
		t.Fatalf("choices status %d", status)
	}
	var choices []map[string]string
	if err := json.Unmarshal(payload, &choices); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, choice := range choices {
		if choice["id"] == "db" {
			found = true
		}
	}
	if !found {
		t.Fatalf("choices missing db: %s", payload)
	}

	status, payload = call(t, server.URL, http.MethodPost, "/api/investigation/guess", map[string]string{"choice_id": "db"})
	if status != http.StatusBadRequest {
		t.Fatalf("guess status %d", status)
	}
	if asObject(t, payload)["detail"] != "No active investigation" {
		t.Fatalf("guess body %s", payload)
	}

	status, payload = call(t, server.URL, http.MethodPost, "/api/investigation/start", nil)
	if status != http.StatusOK || strings.TrimSpace(string(payload)) != `{"ok":true}` {
		t.Fatalf("start %d %s", status, payload)
	}

	status, payload = call(t, server.URL, http.MethodGet, "/api/chaos", nil)
	if status != http.StatusOK {
		t.Fatalf("hidden chaos status %d", status)
	}
	near(t, asObject(t, payload), "server_online", 1)

	status, payload = call(t, server.URL, http.MethodPost, "/api/investigation/abandon", nil)
	if status != http.StatusOK || strings.TrimSpace(string(payload)) != `{"ok":true}` {
		t.Fatalf("abandon %d %s", status, payload)
	}
}

func TestGameDayErrorsAndPrometheusContract(t *testing.T) {
	server := newTestServer(t)
	status, payload := call(t, server.URL, http.MethodPost, "/api/gameday/run", map[string]string{
		"scenario_id": "does-not-exist",
		"hypothesis":  "",
	})
	if status != http.StatusNotFound {
		t.Fatalf("gameday status %d", status)
	}
	if asObject(t, payload)["detail"] != "Unknown scenario" {
		t.Fatalf("gameday body %s", payload)
	}

	status, payload = call(t, server.URL, http.MethodGet, "/metrics", nil)
	if status != http.StatusOK {
		t.Fatalf("prometheus status %d", status)
	}
	exposition := string(payload)
	for _, name := range []string{"lab_sim_requests_total", "lab_http_request_duration_seconds"} {
		if !strings.Contains(exposition, name) {
			t.Fatalf("metrics missing %s", name)
		}
	}
}

func TestMetricsWebSocketEmitsSnapshot(t *testing.T) {
	server := newTestServer(t)
	socketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/metrics"
	conn, _, err := websocket.DefaultDialer.Dial(socketURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var snapshot map[string]any
	if err := conn.ReadJSON(&snapshot); err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot["chaos"]; !ok {
		t.Fatalf("websocket snapshot missing chaos: %#v", snapshot)
	}
}
