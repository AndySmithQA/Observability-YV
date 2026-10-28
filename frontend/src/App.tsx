import { useState } from "react";
import { AppSimulationTab } from "./AppSimulationTab";
import { GameDayTab } from "./GameDayTab";
import { InvestigationTab } from "./InvestigationTab";
import { useMetricsWs } from "./useMetricsWs";

type LabTab = "sim" | "investigation" | "gameday";

export default function App() {
  const { metrics, connected, series } = useMetricsWs();
  const [tab, setTab] = useState<LabTab>("sim");
  const [chaosOpen, setChaosOpen] = useState(true);

  return (
    <div className="min-h-screen bg-surface">
      <header className="border-b border-surface-border bg-surface-raised/80 backdrop-blur sticky top-0 z-30">
        <div className="mx-auto max-w-6xl px-4 py-4">
          <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
            <div>
              <h1 className="text-lg font-semibold tracking-tight text-white">
                Observability Lab
              </h1>
              <p className="text-sm text-slate-500">
                Baselines, investigations, and game-day learning loops — watch signals react in real
                time.
              </p>
            </div>
            <nav className="flex flex-wrap gap-2" aria-label="Lab modes">
              {(
                [
                  ["sim", "App simulation"],
                  ["investigation", "Investigation"],
                  ["gameday", "SRE game day"],
                ] as const
              ).map(([id, label]) => (
                <button
                  key={id}
                  type="button"
                  onClick={() => setTab(id)}
                  className={`rounded-lg px-3 py-2 text-sm font-medium transition ${
                    tab === id
                      ? "bg-accent text-white shadow-lg shadow-accent/20"
                      : "border border-surface-border bg-surface text-slate-300 hover:border-slate-500"
                  }`}
                >
                  {label}
                </button>
              ))}
            </nav>
          </div>
        </div>
      </header>

      {tab === "sim" && (
        <AppSimulationTab
          metrics={metrics}
          connected={connected}
          series={series}
          chaosOpen={chaosOpen}
          setChaosOpen={setChaosOpen}
        />
      )}
      {tab === "investigation" && (
        <InvestigationTab metrics={metrics} connected={connected} series={series} />
      )}
      {tab === "gameday" && <GameDayTab />}
    </div>
  );
}
