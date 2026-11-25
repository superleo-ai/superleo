import { useCallback, useEffect, useMemo, useState } from "react";
import { Route, Routes, useNavigate, useParams } from "react-router-dom";
import { Activity, BotIcon, Brain, ChevronRightIcon, Clock, Cpu, CpuIcon, Database, Gauge, HexagonIcon, Radio, Sparkles, TerminalIcon, TriangleAlertIcon, Wrench, type LucideIcon } from "lucide-react";
import { Conversations, Dashboard as loadDashboard, GetStatus, GraphView as startGraphView, HiveRetire, HiveSpawn } from "../../wailsjs/go/app/App";
import { app } from "../../wailsjs/go/models";
import { EventsOn } from "../../wailsjs/runtime";
import HiveTaskPage, { type FullTask } from "./HiveTaskPage";
import { useI18n } from "../lib/i18n";
import { PATHS, attentionPath, taskPath } from "../lib/routes";
import { attentionTitle, whenOf, type AttentionItem } from "../canvas/attention";
import { elapsed, oneLine, useCodingRuns, useHive, useHosts, type Host, type HostAgent, type HiveTask } from "../canvas/data";
import Reactor, { Counters, usePulse, fmtK, type Pillar } from "../components/Reactor";
import { BurnArea, CacheRing, CallRing, LoadGauges, ToolColumns, TurnColumns } from "../components/ReactorCharts";
import HiveDisc from "../components/HiveDisc";
import { useDaylight } from "../lib/useDaylight";
import { useEngineUsage } from "../components/HiveMeter";
import { useNotedUpcoming } from "../lib/useNotedUpcoming";
import { notifyOf, upcomingWhen, type ScheduleItem } from "../lib/schedule";
import { ModeChip } from "../components/ModeChip";
import PodConfirm, { type PodChange } from "../components/PodConfirm";
import { cliName, isCore, isStandby, kindLabel } from "../world/hosts";
import { focusInWorld } from "../world/bus";
import { clientKind } from "../lib/hivelink";
import { useFeatures } from "../lib/features";
import { queenTitle, useQueenName } from "../lib/queenName";
import { isLegacyConsole, legacyConsoleTitle, pairedDeviceNames, useQueenSession } from "../lib/queenConsole";
import { withoutCallNotes } from "../lib/format";
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
 * The dashboard: the reactor (one pillar per figure, the charts beside it),
 * the plan's usage and the hive's state, the hive's machines and what runs on
 * them, what needs the person, what is next, and the latest conversations. Each task of the hive keeps its own page under it
 * (/hive/tasks/<id>).
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

  const hosts = (topo.hosts ?? []).filter((h) => h.kind !== "ha");
  const hostsLive = hosts.filter((h) => h.state === "live").length;
  const needs = attention.filter((i) => i.level === "needs");
  const hiveRunning = hive.tasks.filter((x) => x.state === "running");
  const codingRunning = runs.filter((r) => r.state === "running");

  const stats = useStatsData();
  const live = pulse.live || (stats.dash?.activeRuns?.length ?? 0) > 0;
  const brain = stats.dash?.memoryMode?.startsWith("shared") ? t("shared brain") : stats.dash?.memoryMode || t("local brain");
  const clock = new Date(now).toTimeString().slice(0, 8);

  return (
    <div className="dsh" data-testid="dashboard">
      <header className="dsh-head">
        <h1>{t("Dashboard")}</h1>
        <span className={live ? "dsh-state on" : "dsh-state"}><i />{t(live ? "Working now" : "Idle")}</span>
        <div className="dsh-chips">
          <span className="dsh-chip" title={t("Model")}><Cpu size={12} />{stats.dash?.llm?.model || "—"}</span>
          <span className="dsh-chip" title={t("Memory")}><Brain size={12} />{brain}</span>
          <span className="dsh-chip" title={`${t("graph nodes")} / ${t("graph edges")}`}><Database size={12} />{stats.graph?.nodes ?? "—"} / {stats.graph?.edges ?? "—"}</span>
          <span className="dsh-chip" title={t("in flight")}><Radio size={12} />{stats.dash?.activeRuns?.length ?? 0}</span>
        </div>
        <span className="dsh-clock"><Clock size={13} />{clock}</span>
      </header>

      <Figures hostsLive={hostsLive} hostsAll={hosts.length} running={hiveRunning.length + codingRunning.length} needs={needs.length} />

      <ReactorBlock pulse={pulse} stats={stats} />

      <div className="dsh-grid">
        <HiveSection hive={hive} hosts={topo.hosts} live={hostsLive} />
        <UpNext now={now} attention={attention} />
        <RunningNow now={now} needs={needs} tasks={hiveRunning as Task[]} runs={codingRunning} />
        <Recent onOpenConversation={onOpenConversation} />
      </div>
    </div>
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

// ── Figures ────────────────────────────────────────────────────────────────

/** The strip of what the reactor lacks: the plan's usage when the queen
 *  thinks on a subscription, and the hive's state. */
function Figures({ hostsLive, hostsAll, running, needs }: { hostsLive: number; hostsAll: number; running: number; needs: number }) {
  const { t } = useI18n();
  const plan = useEngineUsage();
  const onPlan = plan.mode === "subscription" && !!plan.cli;
  const wins = (plan.windows ?? []).slice(0, 2);
  const resets = (at?: number) => (at ? new Date(at * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false }) : "");
  return (
    <div className="dsh-figs" role="list">
      {onPlan && wins.map((w) => (
        <Fig key={w.label} label={`${t(`${plan.cli} plan`)} · ${t(w.label)}`} value={`${Math.round(w.percent)}%`} gauge={w.percent} sub={resets(w.resetsAt) && `${t("resets")} ${resets(w.resetsAt)}`} />
      ))}
      {onPlan && wins.length === 0 && plan.cli === "claude" && !plan.remote && (
        <Fig label={`${t("claude plan")} · ${t("tokens today")}`} value={fmtK(plan.tokens ?? 0)} />
      )}
      <Fig label={t("machines")} value={`${hostsLive}`} of={hostsAll} bad={hostsLive < hostsAll} />
      <Fig label={t("running")} value={String(running)} live={running > 0} />
      <Fig label={t("need you")} value={String(needs)} live={needs > 0} />
    </div>
  );
}

function Fig({ label, value, of, sub, gauge, bad, live }: { label: string; value: string; of?: number; sub?: string; gauge?: number; bad?: boolean; live?: boolean }) {
  return (
    <div className={`dsh-fig${bad ? " bad" : ""}${live ? " live" : ""}`} role="listitem">
      <span title={label}>{label}</span>
      <b>{value}{of !== undefined && <small>/{of}</small>}</b>
      {gauge !== undefined && (
        <i className="dsh-gauge" aria-hidden><i style={{ width: `${Math.max(0, Math.min(100, gauge))}%` }} /></i>
      )}
      {sub && <em>{sub}</em>}
    </div>
  );
}

// ── Hive ───────────────────────────────────────────────────────────────────

/** Each host with the hosts inside it (a node's pods) under it. */
function nest(hosts: Host[]): { h: Host; depth: number }[] {
  const ids = new Set(hosts.map((h) => h.id));
  const out: { h: Host; depth: number }[] = [];
  const walk = (h: Host, depth: number) => {
    out.push({ h, depth });
    hosts.filter((x) => x.parent === h.id).forEach((x) => walk(x, depth + 1));
  };
  hosts.filter((h) => !h.parent || !ids.has(h.parent)).forEach((h) => walk(h, 0));
  return out;
}

/** One state as a dot: the signal for at work, ink for up, red for gone,
 *  a ring for known but not running. */
const dot = (state: string) =>
  state === "working" || state === "waiting" || state === "running" ? "live"
    : state === "live" || state === "idle" || state === "active" ? "on"
      : state === "lost" || state === "standby-unready" || state === "failed" || state === "down" ? "bad" : "off";

const agentRole = (a: HostAgent) => (a.kind === "cli" ? "cli" : a.kind === "agent" ? "agent" : a.role === "machine" ? "machine agent" : a.role || "superleo");

function HiveSection({ hive, hosts, live }: { hive: ReturnType<typeof useHive>; hosts: Host[] | null; live: number }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const features = useFeatures();
  const [making, setMaking] = useState<"" | "spawn" | "retire">("");
  const [asking, setAsking] = useState<PodChange | null>(null);
  const [note, setNote] = useState("");
  const busy = (name: string) => hive.tasks.some((x) => x.worker === name && x.state === "running");

  // The world is where a machine is looked at: the shell brings it up and
  // selects the thing. A coding CLI's page is its runs.
  const show = (kind: string, id: string) => {
    if (clientKind() === "desktop") window.dispatchEvent(new CustomEvent("superleo:world-focus", { detail: { kind, id } }));
    else focusInWorld(kind, id);
  };
  const openAgent = (h: Host, a: HostAgent) => {
    if (a.kind === "cli" && features?.coding) return navigate(PATHS.coding);
    if (a.kind === "superleo") return isCore(a) ? show("hall", "queen") : show("worker", a.name);
    if (a.kind === "cli") return show("cli", h.agents.some(isCore) ? cliName(a) : `${cliName(a)}.${a.via}`);
    if (isStandby(a)) return show("service", a.service || a.name);
    return show("agent", `${a.name}.${a.via || h.name}`);
  };

  const resize = async (kind: "spawn" | "retire") => {
    setMaking(kind);
    setNote("");
    try {
      const r: any = kind === "spawn" ? await HiveSpawn(1) : await HiveRetire(1, false);
      if (!r?.ok) setNote(String(r?.error || t("That did not work.")));
    } catch (e: any) {
      setNote(String(e?.message || e));
    } finally {
      setMaking("");
    }
  };

  const total = (hosts ?? []).filter((h) => h.kind !== "ha").length;
  return (
    <section className="dsh-sec dsh-hive" aria-label={t("Hive")}>
      <div className="dsh-sh">
        <h2>{t("Hive")}</h2>
        {hosts && <em>{live}/{total}</em>}
        {hive.role === "queen" && hive.spawner?.enabled && (
          <span className="dsh-sh-act">
            <button type="button" disabled={making !== ""} onClick={() => setAsking("retire")}>{making === "retire" ? t("Retiring…") : t("− Worker")}</button>
            <button type="button" disabled={making !== ""} onClick={() => setAsking("spawn")}>{making === "spawn" ? t("Starting…") : t("+ Worker")}</button>
            <PodConfirm change={asking} onAnswer={(yes) => { const k = asking; setAsking(null); if (yes && k) void resize(k); }} />
          </span>
        )}
      </div>
      {note && <div className="dsh-err">{note}</div>}
      <div className="dsh-body">
        {hosts === null && hive.members.length === 0 && <div className="dsh-none">{hive.loaded ? t("No machines") : "…"}</div>}
        {hosts === null && hive.members.map((m) => (
          <button type="button" key={m.name} className="dsh-host" onClick={() => show("worker", m.name)}>
            <span className="dsh-host-h">
              <i className={`dsh-dot ${busy(m.name) ? "live" : dot(m.state)}`} />
              <b>{m.name.replace(/^superleo-/, "")}</b>
              {m.engine && <span className="dsh-tag">{m.engine}</span>}
            </span>
          </button>
        ))}
        {hosts && hosts.length === 0 && <div className="dsh-none">{t("No machines")}</div>}
        {hosts && nest(hosts).map(({ h, depth }) => (
          <div key={h.id} className={`dsh-host${h.state !== "live" ? " " + h.state : ""}`} style={{ marginLeft: depth * 18 }}>
            <button type="button" className="dsh-host-h" onClick={() => show("host", h.id)}>
              <i className={`dsh-dot ${dot(h.state)}`} />
              <b>{h.name}</b>
              <span className="dsh-tag">{t(kindLabel(h.kind))}</span>
              <em>{h.state === "live" ? [h.os, h.arch].filter(Boolean).join(" ") : h.state === "lost" ? t("not answering") : ""}</em>
            </button>
            {h.agents.length > 0 && (
              <div className="dsh-agents">
                {h.agents.map((a) => {
                  const state = a.kind === "superleo" && busy(a.name) ? "working" : a.activity || a.state;
                  return (
                    <button type="button" key={a.kind + a.name} className={`dsh-agent${isStandby(a) ? " ghost" : ""}`} onClick={() => openAgent(h, a)}
                      title={[a.about, a.mcp?.length ? `MCP: ${a.mcp.join(", ")}` : ""].filter(Boolean).join("\n") || undefined}>
                      <i className={`dsh-dot ${dot(state)}`} />
                      {a.kind === "cli" ? cliName(a) : a.name.replace(/^superleo-/, "")}
                      <small>{t(agentRole(a))}{state !== "live" && state !== "installed" && state !== "configured" ? ` · ${t(state.replace("-unready", ", not ready"))}` : ""}</small>
                    </button>
                  );
                })}
              </div>
            )}
          </div>
        ))}
      </div>
    </section>
  );
}

// ── What runs now, and what needs you ───────────────────────────────────────

function RunningNow({ now, needs, tasks, runs }: { now: number; needs: AttentionItem[]; tasks: Task[]; runs: ReturnType<typeof useCodingRuns> }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const short = (n: string) => n.replace(/^superleo-/, "");
  const who = (x: Task) => (x.dir === "peer" ? `${short(x.from ?? "")} ⇄ ${short(x.worker)}` : x.dir === "out" ? short(x.worker) : t("Queen"));
  const phase = (x: Task) => (x.phase === "tool" ? x.tool || "tool" : x.phase === "writing" ? t("writing…") : x.phase === "queued" ? t("queued") : t("thinking…"));
  const openNeed = (it: AttentionItem) => {
    const p = attentionPath(it);
    navigate(p === PATHS.hive ? PATHS.tasks : p);
  };
  const count = needs.length + tasks.length + runs.length;
  return (
    <section className="dsh-sec dsh-now" aria-label={t("Running now")}>
      <div className="dsh-sh">
        <h2>{t("Running now")}</h2>
        <em>{count}</em>
      </div>
      <div className="dsh-body">
        {count === 0 && <div className="dsh-none">{t("Nothing running")}</div>}
        {needs.map((it, i) => (
          <button type="button" key={`n-${it.kind}-${it.ref}-${i}`} className="dsh-row need" onClick={() => openNeed(it)}>
            <TriangleAlertIcon size={14} />
            <span className="dsh-row-m"><b>{attentionTitle(it)}</b>{it.detail && <small>{oneLine(it.detail)}</small>}</span>
            <em>{whenOf(it, new Date(now))}</em>
          </button>
        ))}
        {tasks.map((x) => (
          <button type="button" key={x.id} className="dsh-row" onClick={() => navigate(taskPath(x.id))}>
            <i className="dsh-dot live" />
            <span className="dsh-row-m"><b>{oneLine(x.prompt)}</b><small>{who(x)} · {phase(x)}{x.tools > 0 ? ` · ${x.tools}` : ""}</small></span>
            <em>{elapsed(x.started_at, now)}</em>
          </button>
        ))}
        {runs.map((r) => (
          <button type="button" key={r.id} className="dsh-row" onClick={() => navigate(`${PATHS.coding}?run=${encodeURIComponent(r.id)}`)}>
            <TerminalIcon size={14} />
            <span className="dsh-row-m"><b>{oneLine(r.prompt)}</b><small>{r.remote ? `${r.agent}.${r.remote}` : r.agent}</small></span>
            <em>{elapsed(r.started, now)}</em>
          </button>
        ))}
      </div>
    </section>
  );
}

// ── Up next ────────────────────────────────────────────────────────────────

/** noted's next items and SuperLeo's own meetings and reminders, by time:
 *  the chat sidebar's list. */
function UpNext({ now, attention }: { now: number; attention: AttentionItem[] }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const next = useNotedUpcoming(6);
  const feed = attention.filter((i) => i.level === "soon");
  const minute = Math.floor(now / 60000);
  const rows = useMemo(() => {
    type R = { key: string; at: number; noted?: ScheduleItem; life?: AttentionItem };
    const out: R[] = [];
    next.items.forEach((it) => {
      const end = +new Date(it.endTime || it.startTime);
      if (!it.allDay && end < minute * 60000) return;
      out.push({ key: `n-${it.id}-${it.startTime}`, at: +new Date(it.startTime), noted: it });
    });
    feed.forEach((it, i) => out.push({ key: `l-${it.kind}-${it.ref}-${i}`, at: it.at ? +new Date(it.at) : Infinity, life: it }));
    return out.sort((a, b) => a.at - b.at).slice(0, 6);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [next.items, feed.map((f) => `${f.ref}@${f.at}`).join(), minute]);
  return (
    <section className="dsh-sec dsh-next" aria-label={t("Up next")}>
      <div className="dsh-sh">
        <h2>{t("Up next")}</h2>
        <button type="button" className="dsh-link" onClick={() => navigate(PATHS.schedule)}>{t("All")}<ChevronRightIcon size={13} /></button>
      </div>
      <div className="dsh-body">
        {rows.length === 0 && <div className="dsh-none">{next.ok ? t("Nothing scheduled") : t("Schedule not connected")}</div>}
        {rows.map(({ key, noted, life }) => noted ? (
          <button type="button" key={key} className="dsh-row dsh-when" onClick={() => navigate(`${PATHS.schedule}?id=${encodeURIComponent(noted.id)}`)}>
            <em>{upcomingWhen(noted, new Date(now))}</em>
            <span className="dsh-row-m"><b>{noted.title || t("Untitled")}</b></span>
            <ModeChip mode={notifyOf(noted)} compact />
          </button>
        ) : life ? (
          <button type="button" key={key} className="dsh-row dsh-when" onClick={() => navigate(PATHS.schedule)}>
            <em>{whenOf(life, new Date(now))}</em>
            <span className="dsh-row-m"><b>{life.title}</b></span>
          </button>
        ) : null)}
      </div>
    </section>
  );
}

// ── Recent conversations ───────────────────────────────────────────────────

const KIND: Record<string, { Icon: LucideIcon; label: string }> = {
  queen: { Icon: HexagonIcon, label: "Queen" },
  worker: { Icon: CpuIcon, label: "Worker" },
  agent: { Icon: BotIcon, label: "Agent" },
  coding: { Icon: TerminalIcon, label: "Coding" },
};

/** The time of the last word: the clock today, the day before that. */
function when(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  const now = new Date();
  if (d.toDateString() === now.toDateString()) return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
  if (d.getFullYear() === now.getFullYear()) return d.toLocaleDateString([], { month: "numeric", day: "numeric" });
  return d.toLocaleDateString();
}

/** The newest conversations across the queen, the workers, the agents and
 *  the coding CLIs; read again when a turn ends or a run moves. */
function Recent({ onOpenConversation }: { onOpenConversation: (session: string) => void }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const queen = useQueenName();
  const consoleId = useQueenSession();
  const [items, setItems] = useState<app.Conversation[] | null>(null);
  const load = useCallback(() => {
    Conversations(app.ConversationFilter.createFrom({ with: "", query: "", limit: 7 }))
      .then(async (l) => {
        const list = l?.items ?? [];
        const devices = list.some((c) => c.kind === "queen" && isLegacyConsole(c.session)) ? await pairedDeviceNames() : [];
        for (const c of list) if (c.kind === "queen" && isLegacyConsole(c.session)) c.title = legacyConsoleTitle(c.session, devices);
        setItems(list);
      })
      .catch(() => setItems((cur) => cur ?? []));
  }, []);
  useEffect(() => {
    load();
    const offs = ["chat:done", "chat:error", "cli:run"].map((n) => EventsOn(n, load));
    window.addEventListener("focus", load);
    return () => { offs.forEach((o) => typeof o === "function" && o()); window.removeEventListener("focus", load); };
  }, [load]);
  const open = (c: app.Conversation) => (c.kind === "queen" ? onOpenConversation(c.session) : navigate(`${PATHS.conversations}?open=${encodeURIComponent(c.id)}`));
  return (
    <section className="dsh-sec dsh-recent" aria-label={t("Recent conversations")}>
      <div className="dsh-sh">
        <h2>{t("Recent conversations")}</h2>
        <button type="button" className="dsh-link" onClick={() => navigate(PATHS.conversations)}>{t("All")}<ChevronRightIcon size={13} /></button>
      </div>
      <div className="dsh-body">
        {items && items.length === 0 && <div className="dsh-none">{t("No conversations")}</div>}
        {(items ?? []).map((c) => {
          const k = KIND[c.kind] ?? KIND.agent;
          const main = c.kind === "queen" && c.session === consoleId;
          return (
            <button type="button" key={c.id} className={`dsh-conv k-${c.kind}`} onClick={() => open(c)}>
              <span className="dsh-conv-ico"><k.Icon size={14} /></span>
              <span className="dsh-row-m">
                <span className="dsh-conv-top">
                  <b>{c.kind === "queen" ? queenTitle(queen) : c.name || t(k.label)}</b>
                  <span className="dsh-tag">{t(c.source === "terminal" ? "Terminal" : k.label)}</span>
                  <em>{c.state === "running" ? <i className="dsh-dot live" title={t("Working…")} /> : when(c.at)}</em>
                </span>
                <small>{main ? t("Main conversation") : withoutCallNotes(c.title) || t("Untitled")}</small>
              </span>
            </button>
          );
        })}
      </div>
    </section>
  );
}
