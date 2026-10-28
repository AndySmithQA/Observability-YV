import { useCallback, useEffect, useState } from "react";
import type { GameDayScenarioMeta } from "./types";

interface RunResult {
  scenario_id: string;
  scenario_title: string;
  metrics: Record<string, number | undefined>;
  hypothesis_feedback: {
    strength: string;
    matched_keywords?: string[];
    expected_behavior?: string;
    hints?: string[];
  };
  hypothesis_aligned: boolean;
  reference_outcome: string;
  remediations: string[];
}

export function GameDayTab() {
  const [scenarios, setScenarios] = useState<GameDayScenarioMeta[]>([]);
  const [scenarioId, setScenarioId] = useState("");
  const [hypothesis, setHypothesis] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<RunResult | null>(null);
  const [submittedHypothesis, setSubmittedHypothesis] = useState("");

  useEffect(() => {
    fetch("/api/gameday/scenarios")
      .then((r) => r.json())
      .then((j) => {
        const list = j as GameDayScenarioMeta[];
        setScenarios(Array.isArray(list) ? list : []);
        if (list[0]?.id) setScenarioId(list[0].id);
      })
      .catch(() => setScenarios([]));
  }, []);

  const run = useCallback(async () => {
    if (!scenarioId) return;
    setBusy(true);
    setError(null);
    setResult(null);
    try {
      const r = await fetch("/api/gameday/run", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ scenario_id: scenarioId, hypothesis }),
      });
      const j = await r.json().catch(() => ({}));
      if (!r.ok) {
        setError(
          typeof j.detail === "string"
            ? j.detail
            : Array.isArray(j.detail)
              ? j.detail.map((x: { msg?: string }) => x.msg).join("; ")
              : "Run failed"
        );
        return;
      }
      setSubmittedHypothesis(hypothesis);
      setResult(j as RunResult);
    } catch {
      setError("Network error");
    } finally {
      setBusy(false);
    }
  }, [scenarioId, hypothesis]);

  return (
    <div className="mx-auto max-w-6xl space-y-6 px-4 py-4">
      <section className="rounded-xl border border-surface-border bg-surface-raised p-4">
        <h2 className="text-sm font-semibold text-slate-100">SRE game day</h2>
        <p className="mt-1 text-xs text-slate-500">
          Pick a published scenario, describe how you think the fleet will react, then run a short
          simulation. The lab applies the scenario for a few seconds, samples metrics, contrasts your
          hypothesis with what happened, and suggests preventative actions.
        </p>
        <div className="mt-4 grid gap-4 lg:grid-cols-2">
          <label className="block text-xs text-slate-500">
            Scenario
            <select
              className="mt-1 block w-full rounded-md border border-surface-border bg-surface px-3 py-2 text-sm text-slate-200"
              value={scenarioId}
              onChange={(e) => setScenarioId(e.target.value)}
            >
              {scenarios.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.title}
                </option>
              ))}
            </select>
          </label>
          <div className="text-xs text-slate-500 lg:self-end">
            The run restores your previous chaos profile when sampling completes.
          </div>
        </div>
        <label className="mt-4 block text-xs text-slate-500">
          Hypothesis (how will signals move?)
          <textarea
            className="mt-1 min-h-[96px] w-full rounded-md border border-surface-border bg-surface px-3 py-2 text-sm text-slate-200"
            value={hypothesis}
            onChange={(e) => setHypothesis(e.target.value)}
            placeholder="Example: p99 latency should jump sharply while errors rise only a little…"
          />
        </label>
        <button
          type="button"
          disabled={busy || !scenarioId}
          onClick={run}
          className="mt-4 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-white shadow-lg shadow-accent/20 disabled:opacity-40"
        >
          {busy ? "Running…" : "Run simulation"}
        </button>
        {error && (
          <p className="mt-3 text-sm text-rose-300" role="alert">
            {error}
          </p>
        )}
      </section>

      {result && (
        <>
          <section className="rounded-xl border border-surface-border bg-surface-raised p-4">
            <h3 className="text-sm font-semibold text-slate-100">Observed sample</h3>
            <p className="mt-1 text-xs text-slate-500">
              Scenario:{" "}
              <span className="font-medium text-slate-300">{result.scenario_title}</span>
            </p>
            <dl className="mt-3 grid gap-2 text-sm sm:grid-cols-2 lg:grid-cols-3">
              {Object.entries(result.metrics).map(([k, v]) => (
                <div
                  key={k}
                  className="rounded-lg border border-surface-border bg-surface px-3 py-2 font-mono text-xs text-slate-300"
                >
                  <div className="text-slate-500">{k}</div>
                  <div className="text-slate-100">
                    {v === undefined || v === null ? "—" : typeof v === "number" ? v.toFixed(4) : String(v)}
                  </div>
                </div>
              ))}
            </dl>
          </section>

          <section className="rounded-xl border border-surface-border bg-surface-raised p-4">
            <h3 className="text-sm font-semibold text-slate-100">Hypothesis vs outcome</h3>
            <p className="mt-2 text-sm text-slate-300">
              Your hypothesis:{" "}
              <span className="italic text-slate-400">
                {submittedHypothesis.trim() ||
                  "(empty — describe expected signal movement next time)"}
              </span>
            </p>
            <p className="mt-2 text-sm text-slate-300">
              Narrative match:{" "}
              <span
                className={
                  result.hypothesis_aligned ? "text-emerald-400" : "text-amber-400"
                }
              >
                {result.hypothesis_feedback.strength}
              </span>
              {result.hypothesis_feedback.matched_keywords &&
                result.hypothesis_feedback.matched_keywords.length > 0 && (
                  <span className="text-slate-500">
                    {" "}
                    (keywords hit: {result.hypothesis_feedback.matched_keywords.join(", ")})
                  </span>
                )}
            </p>
            <p className="mt-3 text-sm text-slate-400">{result.reference_outcome}</p>
            {(result.hypothesis_feedback.hints?.length ?? 0) > 0 && (
              <ul className="mt-2 list-inside list-disc text-sm text-slate-500">
                {result.hypothesis_feedback.hints?.map((h) => (
                  <li key={h}>{h}</li>
                ))}
              </ul>
            )}
          </section>

          <section className="rounded-xl border border-emerald-500/25 bg-emerald-500/5 p-4">
            <h3 className="text-sm font-semibold text-emerald-200">Remedial actions</h3>
            <ul className="mt-2 list-inside list-decimal space-y-2 text-sm text-emerald-100/90">
              {result.remediations.map((r) => (
                <li key={r}>{r}</li>
              ))}
            </ul>
          </section>
        </>
      )}
    </div>
  );
}
