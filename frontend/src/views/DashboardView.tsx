import { useEffect, useMemo, useState } from "react";
import { Route, Routes, useParams } from "react-router-dom";
import { Activity, Brain, Cpu, Database, Gauge, Sparkles, Wrench } from "lucide-react";
import { Dashboard as loadDashboard, GetStatus, GraphView as startGraphView } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";
import HiveTaskPage, { type FullTask } from "./HiveTaskPage";
import { useI18n } from "../lib/i18n";
import type { AttentionItem } from "../canvas/attention";
import { useCodingRuns, useHive, useHosts, type HiveTask } from "../canvas/data";
import Reactor, { Counters, usePulse, type Pillar } from "../components/Reactor";
import { BurnArea, CacheRing, CallRing, LoadGauges, ToolColumns, TurnColumns } from "../components/ReactorCharts";
import HiveDisc from "../components/HiveDisc";
import { useDaylight } from "../lib/useDaylight";
import AgentsBoard from "./AgentsBoard";
import "./dashboard.css";

type Task = HiveTask & FullTask;

function useNow(ms = 1000) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), ms);
    return () => window.clearInterval(t);
  }, [ms]);
  return now;
}

/**
 * The dashboard, one page read top to bottom: the day (to-dos, schedule,
 * wellbeing, investments), what needs the person, the latest conversations
 * and automations, the agents, and the engine's reactor at the foot. Each
 * task of the hive keeps its own page under it (/hive/tasks/<id>).
 */
export default function DashboardView({ attention, onOpenConversation }: { attention: AttentionItem[]; onOpenConversation: (session: string) => void }) {
  const hive = useHive();
  const now = useNow();
  return (
    <Routes>
      <Route path="tasks/:id" element={<TaskRoute tasks={hive.tasks as Task[]} now={now} />} />
      <Route path="*" element={<Overview hive={hive} now={now} attention={attention} onOpenConversation={onOpenConversation} />} />
    </Routes>
  );
}

function TaskRoute({ tasks, now }: { tasks: Task[]; now: number }) {
  const { id = "" } = useParams();
  return (
    <div className="view">
      <div className="panel-scroll">
        <HiveTaskPage id={id} live={tasks.find((t) => t.id === id)} now={now} />
      </div>
    </div>
  );
}

function Overview({ hive, now, attention, onOpenConversation }: {
  hive: ReturnType<typeof useHive>; now: number; attention: AttentionItem[]; onOpenConversation: (session: string) => void;
}) {
  const { t } = useI18n();
  const pulse = usePulse();
  const topo = useHosts();
  const runs = useCodingRuns();
  const stats = useStatsData();
  const brain = stats.dash?.memoryMode?.startsWith("shared") ? t("shared brain") : stats.dash?.memoryMode || t("local brain");
  const chips = (
    <div className="ab-chips">
      <span className="ab-echip" title={t("Model")}><Cpu size={12} />{stats.dash?.llm?.model || "—"}</span>
      <span className="ab-echip" title={t("Memory")}><Brain size={12} />{brain}</span>
      <span className="ab-echip" title={`${t("graph nodes")} / ${t("graph edges")}`}><Database size={12} />{stats.graph?.nodes ?? "—"} / {stats.graph?.edges ?? "—"}</span>
    </div>
  );
  return (
    <AgentsBoard hive={hive} now={now} attention={attention} onOpenConversation={onOpenConversation} pulse={pulse} hosts={topo.hosts} runs={runs}
      stats={{ usage: stats.dash?.usage, graph: stats.graph, inv: stats.inv }} chips={chips} engine={<ReactorBlock pulse={pulse} stats={stats} />} />
  );
}

// ── Reactor ────────────────────────────────────────────────────────────────

interface DashData {
  llm?: { model: string };
  memoryMode?: string;
  activeRuns?: { run_id: string }[];
  usage?: { totalTokens: number; modelTurns: number; today: number };
  tasks?: unknown[];
}
interface StatsData {
  dash: DashData | null;
  graph: { nodes?: number; edges?: number } | null;
  inv: { skills: number; mcp: number; mcpTools: number };
}

/** What the reactor's pillars and the header's facts need beyond the pulse:
 *  the ledger, the knowledge graph's size and what the install has. */
function useStatsData(): StatsData {
  const [dash, setDash] = useState<DashData | null>(null);
  const [graph, setGraph] = useState<StatsData["graph"]>(null);
  const [inv, setInv] = useState<StatsData["inv"]>({ skills: 0, mcp: 0, mcpTools: 0 });
  useEffect(() => {
    let alive = true;
    const loadDash = async () => { try { const d = (await loadDashboard()) as DashData; if (alive) setDash(d); } catch { /* keep last */ } };
    const loadInv = async () => {
      try {
        const raw = (await GetStatus()) as Record<string, unknown>;
        if (alive) setInv({ skills: Array.isArray(raw.skills) ? raw.skills.length : 0, mcp: Number(raw.mcp ?? 0), mcpTools: Number(raw.mcpTools ?? 0) });
      } catch { /* the pillars keep their last figures */ }
    };
    void loadDash(); void loadInv();
    startGraphView().then((g: Record<string, unknown>) => { if (alive) setGraph(g as StatsData["graph"]); }).catch(() => {});
    const t1 = window.setInterval(loadDash, 8000);
    const t2 = window.setInterval(loadInv, 15000);
    const off = EventsOn("longrun:tick", (p: { kind?: string }) => {
      if (p?.kind === "begin" || p?.kind === "finish" || p?.kind === "interrupted" || p?.kind === "resumed") void loadDash();
    });
    return () => { alive = false; window.clearInterval(t1); window.clearInterval(t2); off(); };
  }, []);
  return { dash, graph, inv };
}

/** The wheel, one standing pillar per figure, with the charts in a column
 *  beside it. Order is the order round the ring, clockwise from twelve. */
function ReactorBlock({ pulse, stats }: { pulse: ReturnType<typeof usePulse>; stats: StatsData }) {
  const { t } = useI18n();
  const day = useDaylight();
  const { dash, graph, inv } = stats;
  const pillars = useMemo<Pillar[]>(() => {
    // Ink by day, light by night: the same figures in the room's own colours.
    const [CY, AM, WH, PK, TL, VI, LI, RO] = day
      ? ["#c98208", "#b06a00", "#33434d", "#cf3655", "#0e7c92", "#0f8a8a", "#12805c", "#d0313f"]
      : ["#f2a516", "#ffb547", "#ffffff", "#ff7a93", "#4dffdb", "#3fd0c9", "#9dff6a", "#ff5c7a"];
    return [
      { key: "today", label: "tokens today", value: dash?.usage?.today ?? 0, color: CY },
      { key: "all", label: "tokens all time", value: dash?.usage?.totalTokens ?? 0, color: CY },
      { key: "proc", label: "tokens process", value: pulse.tokens, color: CY },
      { key: "cached", label: "cached", value: pulse.cached, color: AM },
      { key: "turns", label: "turns today", value: dash?.usage?.modelTurns ?? 0, color: CY },
      { key: "calls", label: "tool calls", value: pulse.calls, color: WH },
      { key: "mcpcalls", label: "mcp calls", value: pulse.mcp, color: PK },
      { key: "memcalls", label: "memory calls", value: pulse.memory, color: TL },
      { key: "fails", label: "failed", value: pulse.fails, color: pulse.fails ? RO : "#8a96b0" },
      { key: "mcp", label: "mcp servers", value: inv.mcp, color: PK },
      { key: "mcptools", label: "mcp tools", value: inv.mcpTools, color: PK },
      { key: "skills", label: "skills", value: inv.skills, color: LI },
      { key: "tasks", label: "tasks on record", value: dash?.tasks?.length ?? 0, color: LI },
      { key: "nodes", label: "graph nodes", value: graph?.nodes ?? 0, color: VI },
      { key: "edges", label: "graph edges", value: graph?.edges ?? 0, color: VI },
      { key: "cpu", label: "cpu", value: pulse.cpu, color: VI, text: pulse.cpu.toFixed(0) + "%" },
      { key: "heap", label: "heap", value: pulse.heap / 1048576, color: VI, text: (pulse.heap / 1048576).toFixed(0) + " MB" },
      { key: "gor", label: "goroutines", value: pulse.goroutines, color: "#8a96b0" },
    ];
  }, [day, dash, graph, inv, pulse.tokens, pulse.cached, pulse.calls, pulse.mcp, pulse.memory, pulse.fails, pulse.cpu, pulse.heap, pulse.goroutines]);

  return (
    <section className="cr-scope cr-hero dsh-hero" aria-label={t("Reactor")}>
      <div className="cr-panel cr-reactor" data-pet-spot="reactor" data-pet-label="the reactor: the ring of light pillars around the knowledge graph">
        <Reactor snap={pulse} pillars={pillars} inside={<HiveDisc />} />
      </div>
      <div className="cr-side" data-pet-spot="charts" data-pet-label="the column of charts beside the reactor">
        <div className="cr-panel tight">
          <div className="cr-panel-h"><Activity size={13} /><span className="cr-eyebrow">{t("This process")}</span><span className={`cr-tag ${pulse.live ? "cyan" : ""}`}>{pulse.live ? "burning" : "idle"}</span></div>
          <Counters snap={pulse} />
        </div>
        <div className="cr-panel tight">
          <div className="cr-panel-h"><Sparkles size={13} /><span className="cr-eyebrow">{t("Cache")}</span></div>
          <CacheRing snap={pulse} />
        </div>
        <div className="cr-panel tight">
          <div className="cr-panel-h"><Wrench size={13} /><span className="cr-eyebrow">{t("Calls")}</span></div>
          <CallRing snap={pulse} />
        </div>
        <div className="cr-panel tight">
          <div className="cr-panel-h"><Activity size={13} /><span className="cr-eyebrow">{t("Two minutes")}</span><span className="cr-tag cyan">tokens</span><span className="cr-tag lime">reasoning</span></div>
          <BurnArea snap={pulse} />
        </div>
        <div className="cr-panel tight">
          <div className="cr-panel-h"><Gauge size={13} /><span className="cr-eyebrow">{t("Tokens per turn")}</span><span className="cr-tag">{pulse.rounds}</span></div>
          <TurnColumns snap={pulse} />
        </div>
        <div className="cr-panel tight">
          <div className="cr-panel-h"><Wrench size={13} /><span className="cr-eyebrow">{t("Calls per tool")}</span><span className="cr-tag">{pulse.tools.length}</span></div>
          <ToolColumns snap={pulse} />
        </div>
        <div className="cr-panel tight">
          <div className="cr-panel-h"><Cpu size={13} /><span className="cr-eyebrow">Load</span><span className="cr-tag">{pulse.goroutines} goroutines</span></div>
          <LoadGauges snap={pulse} />
        </div>
      </div>
    </section>
  );
}
