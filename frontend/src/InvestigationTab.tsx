import { useCallback, useEffect, useState } from "react";
import {
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import type { MetricsSnapshot } from "./types";
import type { ChartRow } from "./useMetricsWs";
import { Kpi } from "./AppSimulationTab";

type Choice = { id: string; label: string };

export interface InvestigationTabProps {
  metrics: MetricsSnapshot | null;
  connected: boolean;
  series: ChartRow[];
}

export function InvestigationTab(props: InvestigationTabProps) {
  const { metrics, connected, series } = props;
  const [choices, setChoices] = useState<Choice[]>([]);
  const [pick, setPick] = useState<string>("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [lastResult, setLastResult] = useState<{
    correct: boolean;
    actual_title: string;
  } | null>(null);

  useEffect(() => {
    fetch("/api/investigation/choices")
      .then((r) => r.json())
      .then((j) => {
        const list = j as Choice[];
        setChoices(Array.isArray(list) ? list : []);
        if (list[0]?.id) setPick(list[0].id);
      })
      .catch(() => setChoices([]));
  }, []);

  const start = useCallback(async () => {
    setBusy(true);
    setMsg(null);
    setLastResult(null);
    try {
      const r = await fetch("/api/investigation/start", { method: "POST" });
      if (!r.ok) throw new Error(await r.text());
      setMsg("Case started — use alerts, charts, and KPIs. Chaos controls are hidden.");
    } catch (e) {
      setMsg(e instanceof Error ? e.message : "Failed to start");
    } finally {
      setBusy(false);
    }
  }, []);

  const abandon = useCallback(async () => {
    setBusy(true);
    try {
      await fetch("/api/investigation/abandon", { method: "POST" });
      setMsg(null);
      setLastResult(null);
    } finally {
      setBusy(false);
    }
  }, []);

  const submit = useCallback(async () => {
    if (!pick) return;
    setBusy(true);
    setMsg(null);
    try {
      const r = await fetch("/api/investigation/guess", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ choice_id: pick }),
      });
      const j = await r.json().catch(() => ({}));
      if (!r.ok) {
        setMsg(typeof j.detail === "string" ? j.detail : "Guess failed");
        return;
      }
      setLastResult({
        correct: Boolean(j.correct),
        actual_title: String(j.actual_title ?? ""),
      });
      setMsg(null);
    } catch {
      setMsg("Network error");
    } finally {
      setBusy(false);
    }
  }, [pick]);

  const inv = metrics?.investigation;
  const syn = inv?.synthetic_alerts ?? [];

  return (
    <div className="mx-auto max-w-6xl space-y-6 px-4 py-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span
          className={`rounded-full px-3 py-1 text-xs font-medium ${
            connected
              ? "bg-emerald-500/15 text-emerald-400"
              : "bg-rose-500/15 text-rose-400"
          }`}
        >
          {connected ? "Live stream" : "Reconnecting…"}
        </span>
      </div>

      <section className="rounded-xl border border-surface-border bg-surface-raised p-4">
        <h2 className="text-sm font-semibold text-slate-100">Root cause investigation</h2>
        <p className="mt-1 text-xs text-slate-500">
          A hidden incident is injected server-side. Alerting snippets and telemetry update in real
          time, but chaos knobs are redacted—pick the category that best explains what you see.
        </p>
        <div className="mt-4 flex flex-wrap gap-2">
          <button
            type="button"
            disabled={busy || Boolean(inv?.active)}
            onClick={start}
            className="rounded-lg bg-accent px-4 py-2 text-sm font-medium text-white shadow-lg shadow-accent/20 disabled:opacity-40"
          >
            Start new case
          </button>
          <button
            type="button"
            disabled={busy || !inv?.active}
            onClick={abandon}
            className="rounded-lg border border-surface-border px-4 py-2 text-sm text-slate-300 disabled:opacity-40"
          >
            Abandon case
          </button>
        </div>
        {inv?.active && (
          <p className="mt-3 text-sm font-medium text-amber-200/90">
            Active: {inv.case_title ?? "Incident in progress"}
          </p>
        )}
        {msg && <p className="mt-3 text-sm text-slate-400">{msg}</p>}
        {lastResult && (
          <div
            className={`mt-3 rounded-lg border px-3 py-2 text-sm ${
              lastResult.correct
                ? "border-emerald-500/40 bg-emerald-500/10 text-emerald-200"
                : "border-amber-500/40 bg-amber-500/10 text-amber-100"
            }`}
          >
            {lastResult.correct ? "Correct — " : "Not quite — "}
            actual profile was <span className="font-medium">{lastResult.actual_title}</span>.
          </div>
        )}
      </section>

      {syn.length > 0 && (
        <section className="rounded-xl border border-rose-500/30 bg-rose-500/5 p-4">
          <h2 className="text-sm font-semibold text-rose-200">Synthetic alert stream</h2>
          <ul className="mt-2 list-inside list-disc space-y-1 text-sm text-rose-100/90">
            {syn.map((s) => (
              <li key={s}>{s}</li>
            ))}
          </ul>
        </section>
      )}

      {/* Reuse KPI + chart pattern without chaos sidebar */}
      <section className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Kpi
          label="Requests / s"
          value={metrics?.requests_per_s}
          fmt={(n) => n.toFixed(1)}
          delta={null}
        />
        <Kpi
          label="Error rate"
          value={metrics != null ? metrics.error_rate * 100 : undefined}
          fmt={(n) => `${n.toFixed(2)}%`}
          delta={null}
        />
        <Kpi
          label="p99 latency"
          value={metrics?.latency_ms.p99}
          fmt={(n) => `${n.toFixed(0)} ms`}
          delta={null}
        />
        <Kpi
          label="Write fail rate"
          value={metrics != null ? metrics.write_fail_rate * 100 : undefined}
          fmt={(n) => `${n.toFixed(2)}%`}
          delta={null}
        />
      </section>

      <section className="rounded-xl border border-surface-border bg-surface-raised p-4">
        <h2 className="mb-3 text-sm font-semibold text-slate-100">Live signals</h2>
        <div className="h-72 w-full">
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={series}>
              <CartesianGrid strokeDasharray="3 3" stroke="#1e2836" />
              <XAxis dataKey="t" tick={false} stroke="#64748b" />
              <YAxis yAxisId="left" stroke="#64748b" width={44} />
              <YAxis yAxisId="right" orientation="right" stroke="#64748b" width={44} />
              <Tooltip
                contentStyle={{
                  background: "#131820",
                  border: "1px solid #1e2836",
                  borderRadius: 8,
                }}
              />
              <Legend />
              <Line
                yAxisId="left"
                type="monotone"
                dataKey="requests_per_s"
                name="RPS"
                stroke="#60a5fa"
                dot={false}
                strokeWidth={2}
              />
              <Line
                yAxisId="left"
                type="monotone"
                dataKey="latency_p99"
                name="p99 ms"
                stroke="#f472b6"
                dot={false}
                strokeWidth={2}
              />
              <Line
                yAxisId="right"
                type="monotone"
                dataKey="error_rate"
                name="Errors %"
                stroke="#fb923c"
                dot={false}
                strokeWidth={2}
              />
            </LineChart>
          </ResponsiveContainer>
        </div>
      </section>

      <section className="rounded-xl border border-surface-border bg-surface-raised p-4">
        <h2 className="text-sm font-semibold text-slate-100">Your diagnosis</h2>
        <p className="mt-1 text-xs text-slate-500">
          Choose the primary failure category. Submit ends the case and reveals the injected profile.
        </p>
        <div className="mt-4 space-y-4">
          <label className="block text-xs text-slate-500">
            Primary category
            <select
              className="mt-1 block w-full max-w-xl rounded-md border border-surface-border bg-surface px-3 py-2 text-sm text-slate-200"
              value={pick}
              onChange={(e) => setPick(e.target.value)}
            >
              {choices.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.label}
                </option>
              ))}
            </select>
          </label>
          <button
            type="button"
            disabled={busy || !inv?.active || !pick}
            onClick={submit}
            className="rounded-lg bg-slate-700 px-4 py-2 text-sm font-medium text-white hover:bg-slate-600 disabled:opacity-40"
          >
            Submit diagnosis
          </button>
        </div>
      </section>
    </div>
  );
}
