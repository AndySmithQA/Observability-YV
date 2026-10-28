# Observability Lab — User Guide

This project is a **learning sandbox** for observability: a running app stack, a live dashboard, **Prometheus** metrics, optional **Grafana** views, and several ways to stress or interrogate the system—open chaos, **hidden incident investigation**, and **SRE game day** hypothesis drills.

---

## What you get

| Piece | Role |
|--------|------|
| **Web UI** | Three modes: **App simulation** (KPIs, charts, baselines, alerts, chaos sliders), **Investigation** (redacted chaos, synthetic alerts, diagnosis quiz), **SRE game day** (known scenarios, hypothesis text, sampled metrics, remediations) |
| **API** | Simulated traffic, chaos knobs, JSON metrics API, **`/metrics`** for Prometheus |
| **Postgres** | Real `INSERT` workload (when running under Docker Compose) |
| **Redis** | Job queue feeding the worker |
| **Worker** | Consumes queue jobs; exposes **`:9101/metrics`** |
| **Prometheus** | Scrapes API and worker |
| **Grafana** | Pre-provisioned dashboard on Prometheus data |

---

## Prerequisites

- **Docker Desktop** (or compatible engine) with Compose v2
- A machine with enough resources for Postgres, Redis, Prometheus, Grafana, and three app containers

---

## Quick start (full stack)

From the project root:

```bash
docker compose up --build
```

Wait until all services are healthy. Then open:

| What | URL | Notes |
|------|-----|--------|
| **Lab dashboard** | [http://localhost:8080](http://localhost:8080) | Main UI (nginx → API + static) |
| **API (direct)** | [http://localhost:8000](http://localhost:8000) | Health: `/health`, readiness: `/api/ready`, Prometheus: `/metrics` |
| **Prometheus** | [http://localhost:9090](http://localhost:9090) | Targets: `lab_api`, `lab_worker` |
| **Grafana** | [http://localhost:3000](http://localhost:3000) | User **`admin`**, password **`admin`** (change in production) |
| **Worker metrics** | [http://localhost:9101/metrics](http://localhost:9101/metrics) | Raw exposition format |

In the UI, use **Prometheus stack** links for quick access to Prometheus, Grafana, and raw metric endpoints.

---

## Lab modes (tabs)

Use the header tabs to switch modes. The WebSocket **live stream** is shared: KPIs and charts update in **App simulation** and **Investigation**; **SRE game day** uses short API runs and does not require the stream for its results panel.

| Tab | Purpose |
|-----|---------|
| **App simulation** | Open chaos sliders, baselines, and configurable alerts—same interactive lab as before. |
| **Investigation** | A **hidden** failure is injected server-side. Chaos values are **redacted** in the API/WebSocket so you infer the problem from **synthetic alert** lines and telemetry. Submit a **diagnosis** (category) to see if you were right. |
| **SRE game day** | You **choose a named scenario**, write how you expect metrics to behave, then **Run simulation**. The backend applies that scenario briefly, samples metrics, compares your hypothesis to a reference narrative (keyword scoring), and lists **remedial actions**. |

**Cross-mode behavior:** Starting or adjusting chaos from **App simulation** sends **`PUT /api/chaos`**, which **ends an active investigation** and returns control to the open sliders. If an investigation is active and you open **App simulation**, a banner warns that shown chaos is redacted until you take over.

---

## How to use the dashboard

### 1. Confirm the live stream

In **App simulation** and **Investigation**, the toolbar shows **Live stream** when the browser is connected over WebSockets. Charts and KPIs update continuously (about five times per second). If you see **Reconnecting…**, check that the API is up and that you are using the same host/port as the UI (or fix proxies/firewall).

### 2. App simulation — Capture a baseline

1. Set chaos sliders to **calm** values (or click **Reset to calm defaults** in the chaos panel).
2. Let the system settle for a short time.
3. Click **Capture baseline**.

Baseline deltas (% vs baseline) appear next to several KPIs so you can see drift at a glance. Baselines are stored in the browser (**localStorage**); they are not shared across devices or browsers.

### 3. App simulation — Define alerts

Under **Alerts**, add rules such as:

- **Latency p99** greater than a threshold (e.g. 400 ms) — good for “slow path” exploration.
- **Error rate** greater than a threshold — tracks synthetic error fraction.
- **DB insert error rate** — useful when Postgres-backed workload is enabled (Docker Compose).

Use **On / Off** to disable a rule without deleting it. **Firing now** summarizes which enabled rules match the **current** metrics.

Alert definitions are also stored in **localStorage**.

### 4. App simulation — Use the chaos panel

Open **Chaos controls** (or **Chaos** on small screens). The sliders drive the **same chaos API** the simulator and (when enabled) the DB workload use:

| Control | Effect |
|---------|--------|
| **Server online** | Near **0%** → hard outage (health fails, synthetic requests error). Middle values → flakier readiness-style behavior. |
| **Backend pool health** | Fraction of **healthy backends** behind the load balancer. When low, more requests fail as if routed to **dead shards** (several servers failing). |
| **Load balancer / routing quality** | **100%** → traffic routes like a well-behaved target group; **0%** → **broken ELB-style** behavior (stale targets / misrouting), so errors rise even if some capacity is still healthy. |
| **Traffic intensity** | More synthetic load and (with Compose) more DB inserts and Redis jobs. |
| **Database latency** | Extra delay (aligned with “slow DB” behavior on inserts and synthetic path). |
| **Random 5xx / app errors** | Increases fraction of failed synthetic requests. |
| **Storage full (write failures)** | Simulated write rejections before commit (and synthetic write failures). |
| **CPU / queue pressure** | Extra processing delay. |
| **Network jitter** | Variable extra delay per request. |

Changes are sent to the API quickly (debounced), so the dashboard and Prometheus counters should move in **near real time**.

### 5. Investigation — Solve a hidden incident

1. Open the **Investigation** tab.
2. Click **Start new case**. The API picks a random incident profile and applies chaos **without revealing** the slider values (`GET /api/chaos` and streamed **`chaos`** fields show a **calm default** while the case is open).
3. Read the **Synthetic alert stream** and watch **KPIs** / **Live signals** (same WebSocket as the rest of the lab).
4. Choose **Primary category** and click **Submit diagnosis**. You get immediate feedback with the **actual** incident title. **Abandon case** resets chaos and ends the session without scoring.

**API (optional):** `GET /api/investigation/choices` — diagnosis options; `POST /api/investigation/start` — new case; `POST /api/investigation/guess` with JSON `{"choice_id":"<id>"}` — submit; `POST /api/investigation/abandon` — reset.

### 6. SRE game day — Hypothesis and remediations

1. Open the **SRE game day** tab.
2. Select a **Scenario** from the list (mirrors the backend playbook).
3. In **Hypothesis**, describe how you expect signals to move (e.g. p99 latency, error rate, write failures).
4. Click **Run simulation**. The backend applies that scenario for a few seconds, **samples metrics**, restores your **previous** chaos profile, then shows:
   - **Observed sample** — snapshot metrics from the run  
   - **Hypothesis vs outcome** — narrative match strength (**strong** / **partial** / **weak**) from keyword overlap, plus the reference outcome text  
   - **Remedial actions** — suggested preventative steps for that scenario  

**API (optional):** `GET /api/gameday/scenarios` — id + title list; `POST /api/gameday/run` with JSON `{"scenario_id":"…","hypothesis":"…"}` — run and get results. An active **Investigation** case blocks game day until you finish or abandon it (**409**).

### 7. Optional: Prometheus and Grafana

- In **Prometheus** → **Status → Targets**, confirm `lab_api` and `lab_worker` are **UP**.
- In **Grafana**, open the **Observability Lab** dashboard (auto-provisioned). You will see panels for synthetic RPS, DB insert latency, worker throughput, and HTTP latency — useful alongside the lab UI when you run the scenarios below.

---

## Scenarios to try

Work through these in order or pick what matches what you want to teach or learn. **Scenarios A–G** assume the **App simulation** tab (open chaos). **Scenario H** uses **Investigation**; **Scenario I** uses **SRE game day**.

### Scenario A — “Where does latency break first?”

1. Capture a **baseline** with chaos low.
2. Add an alert: **Latency p99** **greater than** ~300–500 ms (tune to your taste).
3. Slowly increase **Database latency** and/or **Network jitter** until the alert **fires**.
4. Compare Grafana’s **DB insert duration** / **HTTP p99** panels with the in-app chart.

**Learning goal:** tie a **threshold alert** to a **slow dependency** knob and see lag show up in both the product UI and Prometheus-backed dashboards.

### Scenario B — “Outage and recovery”

1. Baseline with **Server online** at 100%.
2. Add an alert on **Availability** **less than** e.g. **95%** (values are probe-style fractions in the app).
3. Drag **Server online** to **0%**. Watch errors spike, availability drop, and **Firing now**.
4. Restore **Server online** to 100% and watch recovery.

**Learning goal:** practice correlating **health**, **availability**, and **error rate** during a controlled outage.

### Scenario C — “Traffic storm”

1. Baseline with moderate **Traffic intensity**.
2. Add an alert on **Requests / s** **greater than** a value just above your baseline (requires tuning).
3. Raise **Traffic intensity** toward maximum. Watch RPS, latency percentiles, and worker-related metrics (queue + consumer) in Grafana.
4. Optionally add **CPU / queue pressure** to amplify queueing behavior.

**Learning goal:** see how **load** propagates to **latency** and **downstream consumers** (worker job rate).

### Scenario D — “Errors without touching traffic much”

1. Keep traffic moderate.
2. Add an alert: **Error rate** **greater than** e.g. **0.05–0.10** (rates are 0–1 in metric logic).
3. Increase **Random 5xx / app errors** until the alert fires.
4. Observe that you can break **SLO-style error budgets** even when “servers are up.”

**Learning goal:** separate **availability** from **correctness** / error ratio.

### Scenario E — “Storage / write path”

(Relevant when **Postgres** is running — full Docker stack.)

1. Capture baseline with **Storage full** at 0%.
2. Add an alert on **DB insert error rate** **greater than** e.g. **0.05**.
3. Raise **Storage full** until DB insert failures rise and the alert fires.
4. Cross-check **`lab_db_insert_failures_total`** / histogram in Prometheus or Grafana.

**Learning goal:** connect **write failures** to concrete metrics and alerts, similar to disk or quota incidents.

### Scenario F — “Compound incident”

Combine **partial server online**, **high traffic**, **DB latency**, and **error rate** at low levels simultaneously. Capture baseline **before** the mix, then ramp multiple sliders slowly.

**Learning goal:** see **multi-signal** alerting and how **baselines** help spot “everything is a little wrong” vs one dominant failure mode.

### Scenario G — “Backend pool vs broken load balancer”

1. Capture baseline with **Backend pool health** and **Load balancer / routing quality** at **100%**.
2. Add alerts on **Error rate** and optionally **Availability**.
3. **Part A — shrinking pool:** Lower **Backend pool health** (e.g. 40%) while keeping **Load balancer / routing quality** high. Observe errors from **routed-to-dead-shard** behavior.
4. **Part B — bad routing:** Raise **Backend pool health** to a middling value (e.g. 75–80%), then drag **Load balancer / routing quality** toward **0%**. Errors should remain painful even though capacity is not as depleted as in Part A.

**Learning goal:** distinguish **not enough healthy targets** from **bad ELB / stale registration / misrouting** using similar-looking error signals.

### Scenario H — “Investigation drill”

Run several **Investigation** cases back-to-back without peeking at **App simulation** sliders. After each answer, note which **synthetic alerts** and metric shapes drove your category choice.

**Learning goal:** practice **opaque** failures where tooling shows symptoms, not root-cause config.

### Scenario I — “Game day narrative”

Pick a **SRE game day** scenario you already understand; write a precise hypothesis, run the sim, then refine your wording until the outcome panel rates **strong** or **partial** alignment. Read the **Remedial actions** aloud as a mini postmortem.

**Learning goal:** connect **expected signals**, **observed samples**, and **preventative** follow-ups.

---

## Local development (without Docker)

You can run the API and UI on the host for UI work:

- **API:** with Go installed, run `go run .` from `backend`. Without `DATABASE_URL` / `REDIS_URL`, the **physical DB and queue path are off**; the simulator and WebSocket UI still work. Run `go test .` in `backend` for the API contract tests. The worker is `go run .` from `worker` and needs Redis.
- **UI:** in `frontend`, `npm install` and `npm run dev` — Vite proxies `/api` and `/ws` to `127.0.0.1:8000`.

For the **full** experience (real inserts, worker, Prometheus, Grafana), use **Docker Compose** as above.

---

## Troubleshooting

| Symptom | Things to check |
|---------|------------------|
| UI stuck on **Reconnecting…** | API running? Same host as UI? Port `8080` nginx → API. Try [http://localhost:8000/api/ready](http://localhost:8000/api/ready). |
| No DB KPI strip | Only shown when the API was started **with** `DATABASE_URL` (Compose provides this). |
| Prometheus target **DOWN** | Container name/port: API `:8000`, worker `:9101`. Wait for `api` healthcheck after Postgres is ready. |
| Grafana shows no data | Time range (last 15–30 min), Prometheus datasource URL inside Grafana, targets UP. |
| **health** returns 503 | Expected when **Server online** chaos is near zero — that simulates outage. Use **`/api/ready`** for liveness that ignores chaos. |
| **Game day** returns **409** | An **Investigation** case is still open; **Abandon** or **Submit diagnosis** first (or end the case via **`PUT /api/chaos`** from App simulation). |
| **Investigation** chaos looks “normal” in the UI | By design: streamed **`chaos`** is redacted while a case is active; rely on alerts, KPIs, and charts. |

---

## Security note

Default Grafana credentials and open CORS are intended for **local learning only**. Do not expose this stack to the public internet without hardening, secrets management, and authentication.
