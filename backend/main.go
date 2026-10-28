package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

type ChaosConfig struct {
	ServerOnline              float64 `json:"server_online"`
	TrafficLevel              float64 `json:"traffic_level"`
	DBLatencyMS               float64 `json:"db_latency_ms"`
	ErrorRate                 float64 `json:"error_rate"`
	StoragePressure           float64 `json:"storage_pressure"`
	CPUThrottle               float64 `json:"cpu_throttle"`
	NetworkJitterMS           float64 `json:"network_jitter_ms"`
	BackendPoolHealth         float64 `json:"backend_pool_health"`
	LoadBalancerEffectiveness float64 `json:"load_balancer_effectiveness"`
}

func defaultChaos() ChaosConfig {
	return ChaosConfig{1, .35, 0, .02, 0, 0, 0, 1, 1}
}

type ring struct {
	max int
	xs  []float64
}

func (r *ring) push(x float64) {
	r.xs = append(r.xs, x)
	if len(r.xs) > r.max {
		r.xs = r.xs[1:]
	}
}
func (r *ring) percentile(p float64) float64 {
	if len(r.xs) == 0 {
		return 0
	}
	xs := append([]float64(nil), r.xs...)
	sort.Float64s(xs)
	i := int(math.RoundToEven(p / 100 * float64(len(xs)-1)))
	return xs[max(0, min(len(xs)-1, i))]
}

type state struct {
	mu                                sync.RWMutex
	chaos                             ChaosConfig
	investigationActive               bool
	investigationScenario             string
	windowRequests, windowErrors      int
	windowWrites, windowWriteFailures int
	uptimeOK, uptimeFail              int
	latencies, dbLatencies            ring
	dbAttempts, dbErrors              int
	lastTick                          time.Time
	ratePoints                        []ratePoint
}
type ratePoint struct {
	at    time.Time
	value float64
}

func newState() *state {
	return &state{chaos: defaultChaos(), latencies: ring{max: 400}, dbLatencies: ring{max: 250}, lastTick: time.Now()}
}

type scenario struct {
	ID, Title, Answer, InvestigationTitle, Expected string
	Alerts, Keywords, Remediations                  []string
	Chaos                                           ChaosConfig
}

var scenarios = []scenario{
	{"db_slow", "Slow database / dependency", "db", "Incident: latency SLO breach", "Request p99 latency rises sharply while error rate may increase only slightly; RPS can remain stable if the service keeps accepting work.", []string{"Synthetics: p99 latency above 800ms for 5m", "APM: downstream dependency span duration elevated", "Burn rate: latency budget consumption spiking"}, []string{"latency", "slow", "p99", "database", "db", "query", "timeout", "backpressure"}, []string{"Add DB query timeouts and bounded connection pools; shed load when the pool is exhausted.", "Cache hot reads and add read replicas or partitioning for query isolation.", "Tune slow queries and indexes; use async workflows for non-critical paths."}, ChaosConfig{1, .48, 1600, .03, 0, .08, 0, 1, 1}},
	{"disk_pressure", "Storage full / write rejections", "disk", "Incident: burst of write failures", "Write fail rate climbs while overall error rate increases; latency may spike on failed writes.", []string{"Error budget: write-path 5xx rate > 8%", "Logs: burst of 'no space left on device' class failures", "Metric: write_fail_rate elevated vs 24h baseline"}, []string{"disk", "storage", "write", "full", "EIO", "persist"}, []string{"Alert on durable volume utilization and inode exhaustion; autoscale storage or rotate logs.", "Fail fast with clear 507/503 semantics and retry only idempotent writes.", "Use quotas and back-pressure from the write path into queues."}, ChaosConfig{1, .42, 120, .04, .52, 0, 0, 1, 1}},
	{"backend_pool_loss", "Several backends failing (target group shrink)", "lb", "Incident: intermittent 5xx and probe noise", "Error rate and failed routes increase; availability probes may flap when unhealthy targets still receive traffic.", []string{"LB target group: unhealthy host count elevated", "Synthetics: intermittent 502/503 on regional probes", "Availability: canary success rate dipped"}, []string{"backend", "pool", "target", "healthy", "502", "503", "availability"}, []string{"Tighten health checks and drain nodes before termination; ensure min healthy capacity.", "Use circuit breakers and retries with jitter only for safe methods.", "Run chaos drills on rolling deployments to validate LB behavior."}, ChaosConfig{1, .55, 0, .025, 0, 0, 0, .42, .92}},
	{"elb_misroute", "Elastic load balancing / routing broken", "lb", "Incident: routing looks random despite healthy nodes", "Healthy capacity exists but error rate stays high—traffic hits bad targets because routing or health signals are stale.", []string{"LB: stale target registrations suspected (high error with healthy count)", "Tracing: requests landing on draining hosts", "Pager: user reports fine after refresh (uneven routing)"}, []string{"load balancer", "elb", "alb", "routing", "misroute", "stale", "target"}, []string{"Verify target group attachments and health check paths; reduce stale registration TTL.", "Enable cross-zone balancing and connection draining aligned with deploys.", "Add synthetic canaries that validate routing symmetry across AZs."}, ChaosConfig{1, .5, 0, .03, 0, 0, 0, .78, .12}},
	{"app_faults", "Application error storm", "app", "Incident: sudden 5xx spike", "Error rate jumps with less impact on p99 latency than a DB stall; RPS may still be high.", []string{"Error rate SLO burn alert", "APM: exception rate step change post-deploy window", "Logs: stack traces on core API handlers"}, []string{"bug", "5xx", "exception", "deploy", "release", "application", "logic"}, []string{"Roll back or feature-flag the suspect change; add burn-rate alerts on error budgets.", "Improve test coverage for critical paths; use staged rollouts.", "Add defensive validation and graceful degradation for flaky dependencies."}, ChaosConfig{1, .52, 40, .22, 0, 0, 0, 1, 1}},
	{"cpu_saturation", "CPU throttling / noisy neighbor", "cpu", "Incident: latency creep under steady load", "Latency percentiles widen as CPU queueing grows; errors may rise modestly.", []string{"Infra: container CPU throttling counters rising", "Runtime: GC pause / thread pool queue depth elevated", "Latency: gradual p95/p99 widening with flat traffic"}, []string{"cpu", "throttle", "cgroup", "saturation", "queue", "hot"}, []string{"Right-size limits/requests; split hot shards or scale horizontally.", "Profile hot paths; cache and batch workloads; offload to workers.", "Protect with rate limits and admission control when CPU crosses SLO thresholds."}, ChaosConfig{1, .58, 80, .045, 0, .88, 0, 1, 1}},
	{"network_jitter", "Network instability", "net", "Incident: flaky tail latency", "Wide p99/p95 spread with bursty latency; availability may wobble if timeouts are tight.", []string{"Net probe: elevated RTT variance regionally", "Client-side: retry storms on idempotent GETs", "Edge: intermittent upstream timeouts"}, []string{"network", "jitter", "packet", "rt", "unstable", "wan"}, []string{"Tune client timeouts and retries; use hedged requests sparingly with budgets.", "Prefer locality and connection pooling; verify MTU/FW path changes after changes.", "Add path redundancy and measure loss/latency per hop."}, ChaosConfig{1, .46, 90, .06, 0, 0, 320, 1, 1}},
}

var (
	httpRequests     = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "lab_http_requests_total", Help: "HTTP requests"}, []string{"method", "path_group", "status"})
	httpLatency      = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "lab_http_request_duration_seconds", Help: "HTTP request latency", Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}}, []string{"path_group"})
	simRequests      = prometheus.NewCounter(prometheus.CounterOpts{Name: "lab_sim_requests_total", Help: "Synthetic requests (simulator ticks)"})
	simErrors        = prometheus.NewCounter(prometheus.CounterOpts{Name: "lab_sim_errors_total", Help: "Synthetic failed requests"})
	dbInserts        = prometheus.NewCounter(prometheus.CounterOpts{Name: "lab_db_inserts_total", Help: "Successful INSERTs to Postgres"})
	dbInsertFail     = prometheus.NewCounter(prometheus.CounterOpts{Name: "lab_db_insert_failures_total", Help: "Failed INSERT attempts"})
	dbLatency        = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "lab_db_insert_duration_seconds", Help: "Wall time per INSERT including chaos delay", Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 15}})
	redisEnqueued    = prometheus.NewCounter(prometheus.CounterOpts{Name: "lab_redis_jobs_enqueued_total", Help: "Jobs pushed to Redis for worker"})
	redisEnqueueFail = prometheus.NewCounter(prometheus.CounterOpts{Name: "lab_redis_enqueue_errors_total", Help: "Redis enqueue failures"})
)

type app struct {
	state      *state
	db         *pgxpool.Pool
	redis      *redis.Client
	physicalDB bool
}

func main() {
	prometheus.MustRegister(httpRequests, httpLatency, simRequests, simErrors, dbInserts, dbInsertFail, dbLatency, redisEnqueued, redisEnqueueFail)
	a := &app{state: newState()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if url := os.Getenv("DATABASE_URL"); url != "" {
		url = strings.Replace(url, "postgresql+asyncpg://", "postgresql://", 1)
		pool, err := pgxpool.New(ctx, url)
		if err != nil {
			log.Fatalf("database connection: %v", err)
		}
		if _, err = pool.Exec(ctx, "CREATE TABLE IF NOT EXISTS events (id SERIAL PRIMARY KEY, created_at TIMESTAMPTZ DEFAULT now(), source VARCHAR(32) NOT NULL)"); err != nil {
			log.Fatalf("creating events table: %v", err)
		}
		a.db, a.physicalDB = pool, true
		defer pool.Close()
	}
	if url := os.Getenv("REDIS_URL"); url != "" {
		opts, err := redis.ParseURL(url)
		if err != nil {
			log.Printf("invalid REDIS_URL: %v", err)
		} else {
			a.redis = redis.NewClient(opts)
			defer a.redis.Close()
		}
	}
	go a.simulate(ctx)
	go a.resetWindows(ctx)
	if a.physicalDB {
		go a.dbWorkload(ctx)
	}

	mux := http.NewServeMux()
	a.routes(mux)
	server := &http.Server{Addr: ":8000", Handler: metricsMiddleware(mux)}
	log.Fatal(server.ListenAndServe())
}

func (a *app) routes(mux *http.ServeMux) {
	mux.HandleFunc("/metrics", promhttp.Handler().ServeHTTP)
	mux.HandleFunc("/api/ready", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]bool{"ready": true}) })
	mux.HandleFunc("/health", a.health)
	mux.HandleFunc("/api/chaos", a.chaos)
	mux.HandleFunc("/api/metrics", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.snapshot()) })
	mux.HandleFunc("/api/investigation/choices", a.choices)
	mux.HandleFunc("/api/investigation/start", a.investigationStart)
	mux.HandleFunc("/api/investigation/guess", a.investigationGuess)
	mux.HandleFunc("/api/investigation/abandon", a.investigationAbandon)
	mux.HandleFunc("/api/gameday/scenarios", a.gamedayScenarios)
	mux.HandleFunc("/api/gameday/run", a.gamedayRun)
	mux.HandleFunc("/ws/metrics", a.wsMetrics)
}

func cors(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Access-Control-Allow-Methods", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func detail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"detail": message})
}
func validation(w http.ResponseWriter, field, msg string) {
	writeJSON(w, 422, map[string]any{"detail": []map[string]any{{"type": "value_error", "loc": []string{"body", field}, "msg": msg}}})
}

func (a *app) health(w http.ResponseWriter, r *http.Request) {
	a.state.mu.RLock()
	online := a.state.chaos.ServerOnline
	a.state.mu.RUnlock()
	if online < .03 {
		writeJSON(w, 503, map[string]any{"status": "down", "server_online": online})
		return
	}
	writeJSON(w, 200, map[string]any{"status": "ok", "server_online": online})
}
func (a *app) chaos(w http.ResponseWriter, r *http.Request) {
	cors(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return
	}
	switch r.Method {
	case http.MethodGet:
		a.state.mu.RLock()
		cfg := a.state.chaos
		active := a.state.investigationActive
		a.state.mu.RUnlock()
		if active {
			cfg = defaultChaos()
		}
		writeJSON(w, 200, cfg)
	case http.MethodPut:
		var raw map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&raw) != nil {
			validation(w, "body", "Input should be a valid dictionary")
			return
		}
		a.state.mu.RLock()
		cfg := a.state.chaos
		a.state.mu.RUnlock()
		if err := applyConfig(&cfg, raw); err != nil {
			validation(w, "chaos", err.Error())
			return
		}
		a.state.mu.Lock()
		a.state.chaos = cfg
		a.state.investigationActive = false
		a.state.investigationScenario = ""
		a.state.mu.Unlock()
		writeJSON(w, 200, cfg)
	default:
		w.WriteHeader(405)
	}
}
func applyConfig(cfg *ChaosConfig, raw map[string]json.RawMessage) error {
	fields := map[string]*float64{"server_online": &cfg.ServerOnline, "traffic_level": &cfg.TrafficLevel, "db_latency_ms": &cfg.DBLatencyMS, "error_rate": &cfg.ErrorRate, "storage_pressure": &cfg.StoragePressure, "cpu_throttle": &cfg.CPUThrottle, "network_jitter_ms": &cfg.NetworkJitterMS, "backend_pool_health": &cfg.BackendPoolHealth, "load_balancer_effectiveness": &cfg.LoadBalancerEffectiveness}
	for name, target := range fields {
		if v, ok := raw[name]; ok {
			if err := json.Unmarshal(v, target); err != nil {
				return errors.New("Input should be a valid number")
			}
		}
	}
	for name, value := range map[string]float64{"server_online": cfg.ServerOnline, "traffic_level": cfg.TrafficLevel, "db_latency_ms": cfg.DBLatencyMS, "error_rate": cfg.ErrorRate, "storage_pressure": cfg.StoragePressure, "cpu_throttle": cfg.CPUThrottle, "network_jitter_ms": cfg.NetworkJitterMS, "backend_pool_health": cfg.BackendPoolHealth, "load_balancer_effectiveness": cfg.LoadBalancerEffectiveness} {
		limit := 1.0
		if name == "db_latency_ms" {
			limit = 5000
		}
		if name == "network_jitter_ms" {
			limit = 500
		}
		if value < 0 || value > limit {
			return fmt.Errorf("Input should be less than or equal to %v", limit)
		}
	}
	return nil
}

func scenarioByID(id string) *scenario {
	for i := range scenarios {
		if scenarios[i].ID == id {
			return &scenarios[i]
		}
	}
	return nil
}
func (a *app) choices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, []map[string]string{{"id": "db", "label": "Database or downstream slowness"}, {"id": "disk", "label": "Storage pressure / write path failures"}, {"id": "lb", "label": "Load balancer, routing, or unhealthy backend pool"}, {"id": "app", "label": "Application defects or high random 5xx rate"}, {"id": "cpu", "label": "CPU saturation / throttling"}, {"id": "net", "label": "Network jitter or unreliable paths"}})
}
func (a *app) investigationStart(w http.ResponseWriter, r *http.Request) {
	sc := scenarios[rand.Intn(len(scenarios))]
	a.state.mu.Lock()
	a.state.chaos = sc.Chaos
	a.state.investigationActive = true
	a.state.investigationScenario = sc.ID
	a.state.mu.Unlock()
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *app) investigationGuess(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChoiceID string `json:"choice_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.ChoiceID == "" {
		validation(w, "choice_id", "String should have at least 1 character")
		return
	}
	a.state.mu.RLock()
	active, id := a.state.investigationActive, a.state.investigationScenario
	a.state.mu.RUnlock()
	if !active || id == "" {
		detail(w, 400, "No active investigation")
		return
	}
	sc := scenarioByID(id)
	a.endInvestigation()
	if sc == nil {
		detail(w, 500, "Unknown investigation scenario")
		return
	}
	writeJSON(w, 200, map[string]any{"correct": body.ChoiceID == sc.Answer, "actual_title": sc.Title, "answer_category": sc.Answer})
}
func (a *app) endInvestigation() {
	a.state.mu.Lock()
	a.state.investigationActive = false
	a.state.investigationScenario = ""
	a.state.chaos = defaultChaos()
	a.state.mu.Unlock()
}
func (a *app) investigationAbandon(w http.ResponseWriter, r *http.Request) {
	a.endInvestigation()
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *app) gamedayScenarios(w http.ResponseWriter, r *http.Request) {
	out := make([]map[string]string, 0, len(scenarios))
	for _, s := range scenarios {
		out = append(out, map[string]string{"id": s.ID, "title": s.Title})
	}
	writeJSON(w, 200, out)
}
func (a *app) gamedayRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ScenarioID string `json:"scenario_id"`
		Hypothesis string `json:"hypothesis"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.ScenarioID == "" {
		validation(w, "scenario_id", "String should have at least 1 character")
		return
	}
	sc := scenarioByID(body.ScenarioID)
	if sc == nil {
		detail(w, 404, "Unknown scenario")
		return
	}
	a.state.mu.Lock()
	if a.state.investigationActive {
		a.state.mu.Unlock()
		detail(w, 409, "Finish or abandon the investigation before a game day run.")
		return
	}
	previous := a.state.chaos
	a.state.chaos = sc.Chaos
	a.state.mu.Unlock()
	time.Sleep(6500 * time.Millisecond)
	snapshot := a.snapshot()
	a.state.mu.Lock()
	a.state.chaos = previous
	a.state.mu.Unlock()
	feedback := scoreHypothesis(body.Hypothesis, sc)
	writeJSON(w, 200, map[string]any{"scenario_id": sc.ID, "scenario_title": sc.Title, "metrics": map[string]any{"requests_per_s": snapshot["requests_per_s"], "error_rate": snapshot["error_rate"], "write_fail_rate": snapshot["write_fail_rate"], "latency_p99": snapshot["latency_ms"].(map[string]float64)["p99"], "latency_p95": snapshot["latency_ms"].(map[string]float64)["p95"], "availability": snapshot["availability"]}, "hypothesis_feedback": feedback, "hypothesis_aligned": feedback["strength"] == "strong" || feedback["strength"] == "partial", "reference_outcome": sc.Expected, "remediations": sc.Remediations})
}

var looseTokens = regexp.MustCompile(`latency|error|fail|slow|spike|crash|timeout`)

func scoreHypothesis(hypothesis string, sc *scenario) map[string]any {
	text := strings.ToLower(hypothesis)
	hits := []string{}
	for _, keyword := range sc.Keywords {
		if strings.Contains(text, strings.ToLower(keyword)) {
			hits = append(hits, keyword)
		}
	}
	score := len(hits)*2 + min(4, len(looseTokens.FindAllString(text, -1)))
	strength := "weak"
	if score >= 5 {
		strength = "strong"
	} else if score >= 2 {
		strength = "partial"
	}
	hints := []string{}
	if strength == "weak" {
		hints = []string{"Try naming the signals you expect (latency, errors, availability, writes) and how strongly they move."}
	}
	return map[string]any{"strength": strength, "matched_keywords": hits, "matched_count": len(hits), "expected_behavior": sc.Expected, "hints": hints}
}

func (a *app) snapshot() map[string]any {
	a.state.mu.RLock()
	defer a.state.mu.RUnlock()
	cfg := a.state.chaos
	if a.state.investigationActive {
		cfg = defaultChaos()
	}
	requests := max(1, a.state.windowRequests)
	availability := 1.0
	if a.state.uptimeOK+a.state.uptimeFail > 0 {
		availability = float64(a.state.uptimeOK) / float64(a.state.uptimeOK+a.state.uptimeFail)
	}
	rps := a.rateLocked()
	dbRate := 0.0
	if a.state.dbAttempts > 0 {
		dbRate = float64(a.state.dbErrors) / float64(a.state.dbAttempts)
	}
	investigation := map[string]any{"active": false, "synthetic_alerts": []string{}}
	if a.state.investigationActive {
		if sc := scenarioByID(a.state.investigationScenario); sc != nil {
			investigation = map[string]any{"active": true, "case_title": sc.InvestigationTitle, "synthetic_alerts": sc.Alerts}
		}
	}
	return map[string]any{"ts": float64(time.Now().UnixNano()) / 1e9, "chaos": cfg, "requests_per_s": round(rps, 2), "error_rate": round(float64(a.state.windowErrors)/float64(requests), 4), "write_fail_rate": round(ratio(a.state.windowWriteFailures, a.state.windowWrites), 4), "latency_ms": map[string]float64{"p50": round(a.state.latencies.percentile(50), 2), "p95": round(a.state.latencies.percentile(95), 2), "p99": round(a.state.latencies.percentile(99), 2)}, "availability": round(availability, 4), "window": map[string]int{"requests": a.state.windowRequests, "errors": a.state.windowErrors, "writes": a.state.windowWrites, "write_failures": a.state.windowWriteFailures}, "database": map[string]any{"configured": a.physicalDB, "insert_attempts": a.state.dbAttempts, "insert_errors": a.state.dbErrors, "error_rate": round(dbRate, 4), "latency_ms": map[string]float64{"p99": round(a.state.dbLatencies.percentile(99), 2)}}, "investigation": investigation}
}
func (a *app) rateLocked() float64 {
	cutoff := time.Now().Add(-5 * time.Second)
	xs := []ratePoint{}
	for _, p := range a.state.ratePoints {
		if p.at.After(cutoff) {
			xs = append(xs, p)
		}
	}
	if len(xs) == 0 {
		return 0
	}
	if len(xs) < 2 {
		return xs[0].value / 5
	}
	dt := xs[len(xs)-1].at.Sub(xs[0].at).Seconds()
	return sumRates(xs) / math.Max(dt, .001)
}
func sumRates(xs []ratePoint) float64 {
	x := 0.0
	for _, p := range xs {
		x += p.value
	}
	return x
}
func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}
func round(x float64, places int) float64 { p := math.Pow10(places); return math.Round(x*p) / p }
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (a *app) simulate(ctx context.Context) {
	tick := time.NewTicker(80 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			a.simulationTick()
		}
	}
}
func poisson(lambda float64) int {
	if lambda <= 0 {
		return 0
	}
	limit := math.Exp(-lambda)
	product := 1.0
	k := 0
	for product > limit {
		k++
		product *= rand.Float64()
	}
	return max(0, k-1)
}
func (a *app) simulationTick() {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	now := time.Now()
	dt := math.Max(now.Sub(a.state.lastTick).Seconds(), .001)
	a.state.lastTick = now
	cfg := a.state.chaos
	n := min(8000, poisson((35+cfg.TrafficLevel*380)*dt))
	nErr := 0
	hardDown := cfg.ServerOnline < .03
	flaky := !hardDown && cfg.ServerOnline < .85
	for i := 0; i < n; i++ {
		a.state.windowRequests++
		if hardDown {
			a.state.windowErrors++
			nErr++
			a.state.latencies.push(8)
			continue
		}
		if rand.Float64() < .1 {
			if flaky && rand.Float64() > cfg.ServerOnline {
				a.state.uptimeFail++
			} else {
				a.state.uptimeOK++
			}
		}
		unhealthy := 1 - math.Max(0, math.Min(1, cfg.BackendPoolHealth))
		lb := math.Max(0, math.Min(1, cfg.LoadBalancerEffectiveness))
		if unhealthy > .001 || lb < .999 {
			p := unhealthy*lb + (1-lb)*math.Min(1, unhealthy+.45*(1-unhealthy))
			if rand.Float64() < p {
				a.state.windowErrors++
				nErr++
				a.state.latencies.push(22 + rand.Float64()*55)
				continue
			}
		}
		latency := cfg.DBLatencyMS + cfg.NetworkJitterMS*rand.Float64() + cfg.CPUThrottle*200*rand.Float64()
		if rand.Float64() < .38 {
			a.state.windowWrites++
			if rand.Float64() < cfg.StoragePressure {
				a.state.windowWriteFailures++
				a.state.windowErrors++
				nErr++
				a.state.latencies.push(math.Min(latency+80, 8000))
				continue
			}
		}
		if rand.Float64() < cfg.ErrorRate {
			a.state.windowErrors++
			nErr++
			a.state.latencies.push(latency*.35 + 5 + rand.Float64()*115)
			continue
		}
		a.state.latencies.push(latency + rand.ExpFloat64()*14)
	}
	a.state.ratePoints = append(a.state.ratePoints, ratePoint{now, float64(n) / dt})
	cutoff := now.Add(-60 * time.Second)
	i := 0
	for ; i < len(a.state.ratePoints) && a.state.ratePoints[i].at.Before(cutoff); i++ {
	}
	a.state.ratePoints = a.state.ratePoints[i:]
	if n > 0 {
		simRequests.Add(float64(n))
	}
	if nErr > 0 {
		simErrors.Add(float64(nErr))
	}
}
func (a *app) resetWindows(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.state.mu.Lock()
			a.state.windowRequests = 0
			a.state.windowErrors = 0
			a.state.windowWrites = 0
			a.state.windowWriteFailures = 0
			a.state.dbAttempts = 0
			a.state.dbErrors = 0
			a.state.dbLatencies.xs = nil
			a.state.mu.Unlock()
		}
	}
}
func (a *app) dbWorkload(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(400 * time.Millisecond):
	}
	for ctx.Err() == nil {
		a.state.mu.RLock()
		cfg := a.state.chaos
		a.state.mu.RUnlock()
		if cfg.ServerOnline < .03 {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		for i := 0; i < max(1, int(2+cfg.TrafficLevel*18)); i++ {
			a.oneDBOp(ctx, cfg)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
func (a *app) oneDBOp(ctx context.Context, cfg ChaosConfig) {
	a.state.mu.Lock()
	a.state.dbAttempts++
	a.state.mu.Unlock()
	started := time.Now()
	ok := false
	defer func() {
		elapsed := time.Since(started)
		a.state.mu.Lock()
		if !ok {
			a.state.dbErrors++
		}
		a.state.dbLatencies.push(float64(elapsed) / float64(time.Millisecond))
		a.state.mu.Unlock()
		dbLatency.Observe(elapsed.Seconds())
		if ok {
			dbInserts.Inc()
		} else {
			dbInsertFail.Inc()
		}
	}()
	time.Sleep(time.Duration(cfg.DBLatencyMS * float64(time.Millisecond)))
	time.Sleep(time.Duration(cfg.CPUThrottle * rand.Float64() * .04 * float64(time.Second)))
	if rand.Float64() < cfg.StoragePressure {
		return
	}
	if _, err := a.db.Exec(ctx, "INSERT INTO events (source) VALUES ('api')"); err != nil {
		return
	}
	ok = true
	if a.redis != nil && rand.Float64() < .7 {
		if err := a.redis.LPush(ctx, "obs_jobs", fmt.Sprintf("%.6f", float64(time.Now().UnixNano())/1e9)).Err(); err != nil {
			redisEnqueueFail.Inc()
		} else {
			redisEnqueued.Inc()
		}
	}
}

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func (a *app) wsMetrics(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	writeMu := sync.Mutex{}
	done := make(chan struct{})
	lastReadMu := sync.Mutex{}
	lastRead := time.Now()
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				writeMu.Lock()
				err := conn.WriteJSON(a.snapshot())
				writeMu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				lastReadMu.Lock()
				idle := time.Since(lastRead) >= 120*time.Second
				lastReadMu.Unlock()
				if idle {
					writeMu.Lock()
					err := conn.WriteJSON(map[string]string{"type": "ping"})
					writeMu.Unlock()
					if err != nil {
						return
					}
					lastReadMu.Lock()
					lastRead = time.Now()
					lastReadMu.Unlock()
				}
			}
		}
	}()
	defer close(done)
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err) {
				return
			}
			return
		}
		lastReadMu.Lock()
		lastRead = time.Now()
		lastReadMu.Unlock()
		if string(raw) == "ping" || string(raw) == `{"type":"ping"}` {
			writeMu.Lock()
			err = conn.WriteMessage(websocket.TextMessage, []byte("pong"))
			writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

type recordingWriter struct {
	http.ResponseWriter
	status int
}

func (w *recordingWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cors(w, r)
		if r.URL.Path == "/metrics" || strings.HasPrefix(r.URL.Path, "/ws") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rw := &recordingWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		group := pathGroup(r.URL.Path)
		httpRequests.WithLabelValues(r.Method, group, fmt.Sprint(rw.status)).Inc()
		httpLatency.WithLabelValues(group).Observe(time.Since(start).Seconds())
	})
}
func pathGroup(path string) string {
	switch {
	case strings.HasPrefix(path, "/ws"):
		return "/ws"
	case strings.HasPrefix(path, "/api/chaos"):
		return "/api/chaos"
	case strings.HasPrefix(path, "/api/metrics"):
		return "/api/metrics"
	case strings.HasPrefix(path, "/metrics"):
		return "/metrics"
	case strings.HasPrefix(path, "/api/"):
		return "/api/*"
	case strings.HasPrefix(path, "/health"):
		return "/health"
	case strings.HasPrefix(path, "/api/ready"):
		return "/api/ready"
	}
	out := strings.TrimRight(path, "/")
	if out == "" {
		out = "/"
	}
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}

var _ = errors.New
