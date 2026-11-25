import { useCallback, useEffect, useMemo, useState } from "react";
import { Route, Routes, useNavigate, useParams } from "react-router-dom";
import { BotIcon, ChevronRightIcon, CpuIcon, HexagonIcon, TerminalIcon, TriangleAlertIcon, type LucideIcon } from "lucide-react";
import { Conversations, HiveRetire, HiveSpawn } from "../../wailsjs/go/app/App";
import { app } from "../../wailsjs/go/models";
import { EventsOn } from "../../wailsjs/runtime";
import HiveTaskPage, { type FullTask } from "./HiveTaskPage";
import { useI18n } from "../lib/i18n";
import { PATHS, attentionPath, taskPath } from "../lib/routes";
import { attentionTitle, whenOf, type AttentionItem } from "../canvas/attention";
import { elapsed, oneLine, useCodingRuns, useHive, useHosts, type Host, type HostAgent, type HiveTask } from "../canvas/data";
import { usePulse, fmtK } from "../components/Reactor";
import { useUsage } from "../components/StatsBlock";
import { useEngineUsage, useHiveMeter } from "../components/HiveMeter";
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
 * The dashboard: SuperLeo's figures, the hive's machines and what runs on
 * them, what needs the person, what is next, and the latest conversations,
 * on one screen. Each task of the hive keeps its own page under it
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
  const navigate = useNavigate();
  const pulse = usePulse();
  const topo = useHosts();
  const runs = useCodingRuns();

  const hosts = (topo.hosts ?? []).filter((h) => h.kind !== "ha");
  const hostsLive = hosts.filter((h) => h.state === "live").length;
  const needs = attention.filter((i) => i.level === "needs");
  const hiveRunning = hive.tasks.filter((x) => x.state === "running");
  const codingRunning = runs.filter((r) => r.state === "running");

  return (
    <div className="dsh" data-testid="dashboard">
      <header className="dsh-head">
        <h1>{t("Dashboard")}</h1>
        <span className={pulse.live ? "dsh-state on" : "dsh-state"}><i />{t(pulse.live ? "Working now" : "Idle")}</span>
        <button type="button" className="dsh-link" onClick={() => navigate(PATHS.stats)}>{t("Stats")}<ChevronRightIcon size={13} /></button>
      </header>

      <Figures pulse={pulse} hostsLive={hostsLive} hostsAll={hosts.length} running={hiveRunning.length + codingRunning.length} needs={needs.length} />

      <div className="dsh-grid">
        <HiveSection hive={hive} hosts={topo.hosts} live={hostsLive} />
        <UpNext now={now} attention={attention} />
        <RunningNow now={now} needs={needs} tasks={hiveRunning as Task[]} runs={codingRunning} />
        <Recent onOpenConversation={onOpenConversation} />
      </div>
    </div>
  );
}

// ── Figures ────────────────────────────────────────────────────────────────

/** The strip of figures: the ledger's tokens today, this process's turns,
 *  calls and failures (pushed, pulse:frame), the plan's usage when the queen
 *  thinks on a subscription, and the hive's state. */
function Figures({ pulse, hostsLive, hostsAll, running, needs }: { pulse: ReturnType<typeof usePulse>; hostsLive: number; hostsAll: number; running: number; needs: number }) {
  const { t } = useI18n();
  const usage = useUsage();
  const plan = useEngineUsage();
  const meter = useHiveMeter();
  const onPlan = plan.mode === "subscription" && !!plan.cli;
  const wins = (plan.windows ?? []).slice(0, 2);
  const resets = (at?: number) => (at ? new Date(at * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false }) : "");
  return (
    <div className="dsh-figs" role="list">
      <Fig label={t("tokens today")} value={usage ? fmtK(usage.today) : "—"} />
      {meter.members.length > 1 && <Fig label={t("hive tokens")} value={fmtK(meter.total.tokens)} />}
      <Fig label={t("turns")} value={fmtK(pulse.rounds)} />
      <Fig label={t("tool calls")} value={fmtK(pulse.calls)} />
      <Fig label={t("failures")} value={fmtK(pulse.fails)} bad={pulse.fails > 0} />
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
