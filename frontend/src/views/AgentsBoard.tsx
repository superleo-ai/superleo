import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import { Check, ChevronRightIcon, Gauge, HexagonIcon, LayoutGrid, Play, PlusIcon, Waves } from "lucide-react";
import { CodingSessions, Conversations } from "../../wailsjs/go/app/App";
import { app } from "../../wailsjs/go/models";
import { EventsOn } from "../../wailsjs/runtime";
import { useI18n } from "../lib/i18n";
import { PATHS, attentionPath, conversationPath, taskPath } from "../lib/routes";
import { attentionTitle, whenOf, type AttentionItem } from "../canvas/attention";
import { elapsed, oneLine, useCodingRuns, useHive, useHosts, type Host, type HostAgent, type HiveTask } from "../canvas/data";
import type { CodingRun } from "../canvas/tiles";
import { fmtK, usePulse } from "../components/Reactor";
import { useEngineUsage } from "../components/HiveMeter";
import { useNotedUpcoming } from "../lib/useNotedUpcoming";
import { dayOf, localDay, notifyLabel, notifyOf, timeLabel, upcomingWhen } from "../lib/schedule";
import { cliName, isCore, isStandby } from "../world/hosts";
import { focusInWorld } from "../world/bus";
import { clientKind } from "../lib/hivelink";
import { queenTitle, useQueenName } from "../lib/queenName";
import { isLegacyConsole, legacyConsoleTitle, pairedDeviceNames } from "../lib/queenConsole";
import { withoutCallNotes } from "../lib/format";
import { askToCompose } from "../lib/compose";
import { useDeskWorldOn } from "../lib/deskWorld";
import "./agentsboard.css";

/** Which of the dashboard's two views is shown: the agents board (the
 *  default) or the reactor with its charts. Kept per window. */
export type DashView = "board" | "reactor";
const VIEW_KEY = "superleo-dashboard-view";
export function useDashView(): [DashView, (v: DashView) => void] {
  const [view, setView] = useState<DashView>(() => {
    try { return localStorage.getItem(VIEW_KEY) === "reactor" ? "reactor" : "board"; } catch { return "board"; }
  });
  const set = useCallback((v: DashView) => {
    setView(v);
    try { localStorage.setItem(VIEW_KEY, v); } catch { /* the choice lasts this visit */ }
  }, []);
  return [view, set];
}

/** The icon that swaps the two views, in either view's header. */
export function ViewSwitch({ view, onSwitch, className }: { view: DashView; onSwitch: (v: DashView) => void; className?: string }) {
  const { t } = useI18n();
  const to: DashView = view === "board" ? "reactor" : "board";
  const label = t(to === "reactor" ? "Reactor view" : "Agents view");
  return (
    <button type="button" className={className ?? "ab-iconbtn"} title={label} aria-label={label} data-testid="dash-view-switch" onClick={() => onSwitch(to)}>
      {to === "reactor" ? <Gauge size={16} /> : <LayoutGrid size={16} />}
    </button>
  );
}

// ── Figures ────────────────────────────────────────────────────────────────

/** A run as CLIRuns sends it: more than the tiles' CodingRun declares. */
type Run = CodingRun & { ended?: string; cwd?: string; queen?: boolean; costUsd?: number };
/** A conversation somebody started in a terminal (CodingSessions). */
type Term = { machine?: string; cli: string; session?: string; run?: string; source: string; project?: string; title?: string; state: string; at: string; replyable: boolean };

interface Figures {
  n30: number; done: number; failed: number; cancelled: number;
  /** done over finished, or null with nothing finished. */
  rate: number | null;
  /** The mean finished run, in seconds. */
  avg: number | null;
  /** Runs a day, the last fourteen days, today last. */
  days: number[];
  last: Run | null;
  running: Run[];
}

const DAY = 864e5;
const keyOf = (r: Run) => (r.agent.includes(".") || !r.remote ? r.agent : `${r.agent}.${r.remote}`);
const ms = (v?: string) => (v ? new Date(v).getTime() : NaN);
const startOfDay = (t: number) => { const d = new Date(t); d.setHours(0, 0, 0, 0); return d.getTime(); };

function figuresOf(runs: Run[], now: number): Figures {
  const since = now - 30 * DAY;
  const today = startOfDay(now);
  const f: Figures = { n30: 0, done: 0, failed: 0, cancelled: 0, rate: null, avg: null, days: new Array(14).fill(0), last: null, running: [] };
  let durs = 0, nd = 0;
  for (const r of runs) {
    const at = ms(r.started);
    if (!f.last || at > ms(f.last.started)) f.last = r;
    if (r.state === "running") f.running.push(r);
    if (!(at >= since)) continue;
    f.n30++;
    if (r.state === "done") f.done++;
    else if (r.state === "failed") f.failed++;
    else if (r.state === "cancelled") f.cancelled++;
    const end = ms(r.ended);
    if (r.state !== "running" && end > at) { durs += (end - at) / 1000; nd++; }
    const back = Math.floor((today - startOfDay(at)) / DAY);
    if (back >= 0 && back < 14) f.days[13 - back]++;
  }
  const fin = f.done + f.failed + f.cancelled;
  f.rate = fin ? f.done / fin : null;
  f.avg = nd ? durs / nd : null;
  return f;
}

/** 43s, 6m 28s, 80m, 3h 5m. */
function fmtDur(s: number | null): string {
  if (s === null) return "—";
  if (s < 60) return `${Math.round(s)}s`;
  if (s < 600) return `${Math.floor(s / 60)}m ${Math.round(s % 60)}s`;
  const m = Math.round(s / 60);
  return m < 100 ? `${m}m` : `${Math.floor(m / 60)}h ${m % 60}m`;
}
const fmtPct = (r: number | null) => (r === null ? "—" : `${(r * 100).toFixed(1)}%`);
/** 97.5M as a figure and its unit. */
function bigK(n: number): [string, string] {
  if (n >= 1e9) return [(n / 1e9).toFixed(1), "B"];
  if (n >= 1e6) return [(n / 1e6).toFixed(1), "M"];
  if (n >= 1e3) return [(n / 1e3).toFixed(1), "k"];
  return [String(Math.round(n)), ""];
}
const hm = (t: number) => new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
const md = (t: number) => { const d = new Date(t); return `${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`; };
const base = (p?: string) => (p ?? "").split("/").filter(Boolean).pop() ?? "";

// ── Who works ──────────────────────────────────────────────────────────────

type Kind = "queen" | "cli" | "agent" | "worker";
type Status = "working" | "idle" | "live" | "offline" | "low";
interface Card {
  key: string;
  kind: Kind;
  name: string;
  where: string;
  status: Status;
  fig: Figures | null;
  host?: Host;
  agent?: HostAgent;
  terms: number;
}

const AVATAR: Record<string, string> = { claude: "cl", codex: "cx", hermes: "he", opencode: "oc", openclaw: "ow", pi: "π", gemini: "ge", copilot: "co", kimi: "ki", goose: "go", agy: "ag", qwen: "qw", cursor: "cu" };
const avatar = (name: string) => AVATAR[name.split(".")[0].toLowerCase()] ?? name.replace(/^superleo-/, "").slice(0, 2).toLowerCase();
const isBusy = (s?: string) => s === "working" || s === "waiting" || s === "running";
const isGone = (s?: string) => s === "lost" || s === "down" || s === "failed" || s === "standby-unready";

function useTerminals(): Term[] {
  const [terms, setTerms] = useState<Term[]>([]);
  useEffect(() => {
    let alive = true;
    let timer = 0;
    const load = () => CodingSessions()
      .then((l) => { if (alive) setTerms(((l ?? []) as unknown as Term[]).filter((x) => x.source === "terminal")); })
      .catch(() => {});
    // Read again when a terminal's state moves; no clock of its own.
    const soon = () => { if (!timer) timer = window.setTimeout(() => { timer = 0; load(); }, 600); };
    load();
    const offs = ["cc:activity", "cli:activity", "cli:run"].map((n) => EventsOn(n, soon));
    return () => { alive = false; window.clearTimeout(timer); offs.forEach((o) => typeof o === "function" && o()); };
  }, []);
  return terms;
}

/** The queen first, then the coding CLIs, the workers and the agents
 *  served from elsewhere: every host's agents, and every CLI a run names. */
function useCards(hive: ReturnType<typeof useHive>, hosts: Host[] | null, runs: Run[], terms: Term[], now: number, queenBusy: boolean): Card[] {
  const { t } = useI18n();
  const queen = useQueenName();
  const day = Math.floor(now / 60000);
  return useMemo(() => {
    const byKey = new Map<string, Run[]>();
    for (const r of runs) { const k = keyOf(r); byKey.set(k, [...(byKey.get(k) ?? []), r]); }
    const list = (hosts ?? []).filter((h) => h.kind !== "ha");
    const coreHost = list.find((h) => h.agents.some(isCore));
    const placeOf = (h?: Host) => (!h ? t("this Mac") : h === coreHost && h.kind === "mac" ? t("this Mac") : h.name);
    const termsOf = (key: string) => {
      const [cli, machine] = key.split(".");
      return terms.filter((x) => x.cli === cli && (machine ? x.machine === machine : !x.machine || x.machine === coreHost?.name)).length;
    };
    const cliStatus = (fig: Figures | null, state?: string, hostGone?: boolean): Status => {
      if (isBusy(state) || (fig?.running.length ?? 0) > 0) return "working";
      if (hostGone || isGone(state)) return "offline";
      if (fig && fig.rate !== null && fig.rate < 0.5 && fig.done + fig.failed + fig.cancelled >= 3) return "low";
      return "idle";
    };

    const engine = hive.engine || "";
    const cards: Card[] = [];
    const seen = new Set<string>();

    // The queen: the engine that answers for her, with its runs.
    const qRuns = engine ? byKey.get(engine) ?? [] : runs.filter((r) => r.queen);
    const qFig = qRuns.length ? figuresOf(qRuns, now) : null;
    const [qCli, qMachine] = engine.split(".");
    cards.push({
      key: "queen", kind: "queen", name: engine ? qCli : queenTitle(queen),
      where: `${queenTitle(queen)} · ${qMachine || placeOf(coreHost)}`,
      status: queenBusy || (qFig?.running.length ?? 0) > 0 ? "working" : "idle",
      fig: qFig, host: coreHost, terms: 0,
    });
    if (engine) seen.add(engine);

    const workers: Card[] = [];
    const remote: Card[] = [];
    for (const h of list) {
      for (const a of h.agents) {
        if (isStandby(a)) continue;
        const state = a.activity || a.state;
        if (a.kind === "cli") {
          const key = h === coreHost || !a.via ? cliName(a) : `${cliName(a)}.${a.via}`;
          if (seen.has(key)) continue;
          seen.add(key);
          const rs = byKey.get(key) ?? [];
          const fig = rs.length ? figuresOf(rs, now) : null;
          const n = termsOf(key);
          workers.push({
            key, kind: "cli", name: key, host: h, agent: a, fig, terms: n,
            where: [t("CLI"), placeOf(h), n ? t(n === 1 ? "1 terminal" : "{n} terminals", { n }) : ""].filter(Boolean).join(" · "),
            status: cliStatus(fig, state, h.state === "lost"),
          });
        } else if (a.kind === "agent") {
          const key = `agent:${a.name}@${a.via || h.name}`;
          if (seen.has(key) || seen.has(`agent:${a.name}`)) continue;
          seen.add(key); seen.add(`agent:${a.name}`);
          remote.push({
            key, kind: "agent", name: a.name, host: h, agent: a, fig: null, terms: 0,
            where: `${t("Agent")} · ${h.name}`,
            status: isBusy(a.activity) ? "working" : h.state === "lost" || isGone(a.state) || a.state === "configured" ? "offline" : "live",
          });
        } else if (a.kind === "superleo" && a.role === "worker") {
          const key = `worker:${a.name}`;
          if (seen.has(key)) continue;
          seen.add(key);
          const parent = h.parent ? list.find((x) => x.id === h.parent) : undefined;
          const busy = hive.tasks.some((x) => x.worker === a.name && x.state === "running");
          workers.push({
            key, kind: "worker", name: a.name.replace(/^superleo-/, ""), host: parent ?? h, agent: a, fig: null, terms: 0,
            where: `${t("Worker")} · ${(parent ?? h).name}`,
            status: busy ? "working" : isGone(a.state) || h.state === "lost" ? "offline" : "idle",
          });
        }
      }
    }
    // An older core without hosts: the hive's members are its workers.
    if (hosts === null) {
      for (const m of hive.members) {
        const busy = hive.tasks.some((x) => x.worker === m.name && x.state === "running");
        workers.push({ key: `worker:${m.name}`, kind: "worker", name: m.name.replace(/^superleo-/, ""), where: t("Worker"), fig: null, terms: 0, status: busy ? "working" : m.state === "lost" ? "offline" : "idle" });
      }
    }
    // Any CLI the runs name that no host listed.
    for (const [key, rs] of byKey) {
      if (seen.has(key)) continue;
      seen.add(key);
      const fig = figuresOf(rs, now);
      const n = termsOf(key);
      const [, machine] = key.split(".");
      workers.push({
        key, kind: "cli", name: key, fig, terms: n,
        where: [t("CLI"), machine || placeOf(coreHost), n ? t(n === 1 ? "1 terminal" : "{n} terminals", { n }) : ""].filter(Boolean).join(" · "),
        status: cliStatus(fig),
      });
    }
    // Most runs first among the CLIs; the workers after them by name.
    workers.sort((a, b) => (b.fig?.n30 ?? -1) - (a.fig?.n30 ?? -1) || a.name.localeCompare(b.name));
    return [...cards, ...workers, ...remote];
    // `day` re-reads the windows once a minute; `now` ticks every second.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hive.engine, hive.members, hive.tasks, hosts, runs, terms, queen, queenBusy, day, t]);
}

// ── The board ──────────────────────────────────────────────────────────────

interface Inv { skills: number; mcp: number; mcpTools: number }
export interface BoardStats {
  usage?: { totalTokens: number; modelTurns: number; today: number };
  graph: { nodes?: number; edges?: number } | null;
  inv: Inv;
}

type Tab = "all" | "working" | "idle" | "remote";

export default function AgentsBoard({ hive, now, attention, onOpenConversation, pulse, hosts, runs: rawRuns, stats, onSwitch }: {
  hive: ReturnType<typeof useHive>;
  now: number;
  attention: AttentionItem[];
  onOpenConversation: (session: string) => void;
  pulse: ReturnType<typeof usePulse>;
  hosts: ReturnType<typeof useHosts>["hosts"];
  runs: ReturnType<typeof useCodingRuns>;
  stats: BoardStats;
  onSwitch: (v: DashView) => void;
}) {
  const { t, language } = useI18n();
  const bt = useBoardT();
  const navigate = useNavigate();
  const world = useDeskWorldOn();
  const queen = useQueenName();
  const plan = useEngineUsage();
  const terms = useTerminals();
  const runs = rawRuns as Run[];
  const [tab, setTab] = useState<Tab>("all");

  const cards = useCards(hive, hosts, runs, terms, now, pulse.live);
  const all = useMemo(() => figuresOf(runs, now), [runs, Math.floor(now / 60000)]); // eslint-disable-line react-hooks/exhaustive-deps

  // The week so far against the whole week before, Monday first.
  const weeks = useMemo(() => {
    const d = new Date(startOfDay(now));
    const monday = d.getTime() - ((d.getDay() + 6) % 7) * DAY;
    let w = 0, pw = 0;
    for (const r of runs) {
      const at = ms(r.started);
      if (at >= monday) w++;
      else if (at >= monday - 7 * DAY) pw++;
    }
    return { w, pw };
  }, [runs, Math.floor(now / 60000)]); // eslint-disable-line react-hooks/exhaustive-deps

  const working = cards.filter((c) => c.status === "working").length;
  const remote = cards.filter((c) => c.kind === "agent").length;
  const online = cards.filter((c) => c.status !== "offline").length;
  const machines = (hosts ?? []).filter((h) => h.kind !== "ha" && !h.parent).length;
  const counts: Record<Tab, number> = { all: cards.length, working, idle: cards.filter((c) => c.status !== "working" && c.kind !== "agent").length, remote };
  const shown = cards.filter((c) => tab === "all" || (tab === "working" ? c.status === "working" : tab === "remote" ? c.kind === "agent" : c.status !== "working" && c.kind !== "agent"));

  const askQueen = () => {
    if (world) window.dispatchEvent(new CustomEvent("superleo:window", { detail: PATHS.home }));
    else navigate(PATHS.home);
    askToCompose();
  };
  const show = (kind: string, id: string) => {
    if (clientKind() === "desktop") window.dispatchEvent(new CustomEvent("superleo:world-focus", { detail: { kind, id } }));
    else focusInWorld(kind, id);
  };
  const openRun = (id?: string) => navigate(id ? `${PATHS.coding}?run=${encodeURIComponent(id)}` : PATHS.coding);

  const fin = all.done + all.failed + all.cancelled;
  const change = weeks.pw > 0 ? Math.round(((weeks.w - weeks.pw) / weeks.pw) * 100) : null;
  const win = plan.mode === "subscription" && plan.cli ? (plan.windows ?? [])[0] : undefined;
  const [tokN, tokU] = bigK(stats.usage?.totalTokens ?? 0);
  const peak = Math.max(1, ...all.days);
  const date = new Date(now).toLocaleDateString(language === "en" ? "en-GB" : language, { weekday: "short", day: "numeric", month: "short" });

  return (
    <div className="ab" data-testid="dashboard" data-view="board">
      <header className="ab-head">
        <h1>{t("Dashboard")}</h1>
        {working > 0 && <span className="ab-pill live">{t("{n} working", { n: working })}</span>}
        <span className="ab-when">{date} · {hm(now)}</span>
        <span className="ab-sp" />
        <ViewSwitch view="board" onSwitch={onSwitch} />
        <button type="button" className="ab-btn" onClick={askQueen}><HexagonIcon size={14} />{queen.name !== "queen" ? t("Ask {name}", { name: queenTitle(queen) }) : t("Ask the queen")}</button>
        <button type="button" className="ab-btn ink" onClick={() => navigate(PATHS.coding)}><PlusIcon size={15} />{bt("New run")}</button>
      </header>

      <div className="ab-stats">
        <Stat icon={<HexagonIcon size={14} />} label={t("Online")} chip={<span className="ab-chip mute">{t(machines === 1 ? "1 machine" : "{n} machines", { n: machines })}</span>}
          value={<>{online}<small> / {cards.length}</small></>}
          sub={t("queen {name} · {w} workers · {r} remote", { name: cards[0]?.name ?? "—", w: cards.filter((c) => c.kind === "cli" || c.kind === "worker").length, r: remote })} />
        <Stat icon={<Play size={13} />} label={t("Runs · 30d")}
          chip={change !== null ? <span className={`ab-chip ${change >= 0 ? "up" : "dn"}`}>{change >= 0 ? "↗" : "↘"} {Math.abs(change)}% {t("wk")}</span> : null}
          value={<>{all.n30}</>}
          sub={t("{w} this week · {pw} the week before", { w: weeks.w, pw: weeks.pw })}
          extra={
            <div className="ab-ticks" aria-hidden>
              {all.days.map((n, i) => <b key={i} className={n === peak && n > 0 || i === 13 && n > 0 ? "h" : ""} style={{ height: `${n ? Math.max(8, (n / peak) * 100) : 3}%` }} />)}
            </div>
          } />
        <Stat icon={<Check size={14} />} label={t("Success")} chip={<span className="ab-chip up">{t("{n} done", { n: all.done })}</span>}
          value={fin ? <>{Math.round((all.done / fin) * 100)}<small>%</small></> : <>—</>}
          sub={t("{f} failed · {c} cancelled", { f: all.failed, c: all.cancelled })} />
        <Stat icon={<Waves size={14} />} label={t("Tokens")}
          chip={win ? <span className="ab-chip mute">{t("{cli} plan {n}%", { cli: plan.cli ?? "", n: Math.round(win.percent) })}</span> : null}
          value={<>{tokN}<small>{tokU}</small></>}
          sub={t("all time · {n} turns today", { n: fmtK(stats.usage?.modelTurns ?? 0) })} />
      </div>

      <div className="ab-main">
        <section className="ab-agents" aria-label={t("Agents")}>
          <div className="ab-sec-h">
            <h2>{t("Agents")}</h2>
            <div className="ab-tabs" role="tablist">
              {(["all", "working", "idle", "remote"] as Tab[]).map((k) => (
                <button key={k} type="button" role="tab" aria-selected={tab === k} className={tab === k ? "on" : ""} onClick={() => setTab(k)}>
                  {t(k === "all" ? "All" : k === "working" ? "Working" : k === "idle" ? "Idle" : "Remote")}<em>{counts[k]}</em>
                </button>
              ))}
            </div>
          </div>
          <div className="ab-grid">
            {shown.map((c) => (
              <AgentCard key={c.key} c={c} now={now}
                onRuns={() => openRun((c.fig?.running[0] ?? c.fig?.last)?.id)}
                onTalk={() => (c.kind === "queen" ? askQueen() : c.kind === "worker" ? show("worker", c.agent?.name ?? c.name) : show("agent", `${c.agent?.name ?? c.name}.${c.agent?.via || c.host?.name || ""}`))} />
            ))}
          </div>
        </section>

        <aside className="ab-side">
          <NowBox now={now} runs={runs} tasks={hive.tasks} terms={terms} needs={attention.filter((i) => i.level === "needs")} />
          <NextBox now={now} />
          <RecentBox onOpenConversation={onOpenConversation} />
        </aside>
      </div>

      <div className="ab-eng" aria-label={t("Engine")}>
        <Eng label={t("Tokens today")} value={fmtK(stats.usage?.today ?? 0)} />
        <Eng label={t("Tool calls")} value={fmtK(pulse.calls)} />
        <Eng label="MCP" value={`${stats.inv.mcp} · ${stats.inv.mcpTools}`} />
        <Eng label={t("Skills")} value={String(stats.inv.skills)} />
        <Eng label={t("Graph")} value={`${fmtK(stats.graph?.nodes ?? 0)} · ${fmtK(stats.graph?.edges ?? 0)}`} />
        <Eng label={t("CPU · heap")} value={`${pulse.cpu.toFixed(0)}% · ${(pulse.heap / 1048576).toFixed(0)}MB`} />
        <button type="button" className="ab-link strong" onClick={() => onSwitch("reactor")}>{t("Reactor")}<ChevronRightIcon size={13} /></button>
      </div>
    </div>
  );
}

function Stat({ icon, label, chip, value, sub, extra }: { icon: ReactNode; label: string; chip: ReactNode; value: ReactNode; sub: string; extra?: ReactNode }) {
  return (
    <div className="ab-st">
      <div className="ab-st-k"><span className="ab-ico">{icon}</span>{label}{chip}</div>
      <div className="ab-st-v">{value}</div>
      <div className="ab-st-sub">{sub}</div>
      {extra}
    </div>
  );
}

function Eng({ label, value }: { label: string; value: string }) {
  return <div className="ab-e"><div className="ab-e-l">{label}</div><div className="ab-e-n">{value}</div></div>;
}

const STATUS: Record<Status, { cls: string; label: string }> = {
  working: { cls: "s-work", label: "Working" },
  idle: { cls: "s-idle", label: "Idle" },
  live: { cls: "s-live", label: "Live" },
  offline: { cls: "s-off", label: "Offline" },
  low: { cls: "s-warn", label: "Low success" },
};

function AgentCard({ c, now, onRuns, onTalk }: { c: Card; now: number; onRuns: () => void; onTalk: () => void }) {
  const { t } = useI18n();
  const f = c.fig;
  const st = STATUS[c.status];
  const peak = Math.max(1, ...(f?.days ?? [0]));
  const lastAt = f?.last ? ms(f.last.started) : NaN;
  const today = startOfDay(now);
  const running = f?.running[0];
  const foot = running
    ? `${base(running.cwd) || running.agent} · ${t("now")}`
    : f?.last ? `${t("last")} ${lastAt >= today ? `${hm(lastAt)} ${t("today")}` : md(lastAt)}`
      : c.kind === "agent" ? t("remote") : c.host?.name ?? "";
  const facts: [string, string, boolean?][] = f
    ? [[t("Runs · 30d"), String(f.n30)], [t("Success"), fmtPct(f.rate), f.rate !== null && f.rate < 0.5], [t("Avg run"), fmtDur(f.avg)]]
    : [[t("Machine"), c.host?.name ?? "—"], c.kind === "worker" ? [t("Engine"), "—"] : [t("Via"), c.agent?.via?.replace(/^superleo-/, "") || "—"], [t("State"), t(c.agent?.activity || c.agent?.state || "—")]];
  const runsLink = c.kind === "cli" || (c.kind === "queen" && f);
  return (
    <div className={`ab-ag${c.kind === "queen" ? " queen" : ""}`} data-agent={c.name}>
      <div className="ab-ag-t">
        <span className={`ab-av${c.kind === "queen" ? " q" : ""}`}>{c.kind === "queen" ? "Q" : avatar(c.name)}</span>
        <div className="ab-ag-id"><div className="ab-nm">{c.name}</div><div className="ab-wh">{c.where}</div></div>
        <span className={`ab-st-chip ${st.cls}`}>{c.status === "working" && <i />}{t(st.label)}</span>
      </div>
      <div className="ab-spark" aria-hidden>
        {(f?.days ?? new Array(14).fill(0)).map((n, i) => <b key={i} className={n ? "" : "z"} style={n ? { height: `${Math.max(8, (n / peak) * 100)}%` } : undefined} />)}
        <span>14d</span>
      </div>
      <div className="ab-fig">
        {facts.map(([l, n, bad]) => <div key={l}><div className="ab-fig-l">{l}</div><div className={`ab-fig-n${bad ? " bad" : ""}${n.length > 7 ? " small" : ""}`} title={n}>{n}</div></div>)}
      </div>
      <div className="ab-ft">
        <span>{foot}</span>
        {runsLink
          ? <button type="button" className="ab-link strong" onClick={onRuns}>{t(running ? "Open" : "Runs")}<ChevronRightIcon size={13} /></button>
          : <button type="button" className="ab-link strong" onClick={onTalk}>{t(c.kind === "worker" ? "Open" : "Talk")}<ChevronRightIcon size={13} /></button>}
      </div>
    </div>
  );
}

// ── Now, up next, recent ───────────────────────────────────────────────────

/** Strings this board says differently from the rest of the app. */
function useBoardT() {
  const { t } = useI18n();
  return (k: string) => { const v = t(`board·${k}`); return v.startsWith("board·") ? t(k) : v; };
}

function NowBox({ now, runs, tasks, terms, needs }: { now: number; runs: Run[]; tasks: HiveTask[]; terms: Term[]; needs: AttentionItem[] }) {
  const { t } = useI18n();
  const bt = useBoardT();
  const navigate = useNavigate();
  const running = runs.filter((r) => r.state === "running");
  const hiveRunning = tasks.filter((x) => x.state === "running");
  const live = terms.filter((x) => x.state === "working" || x.state === "waiting");
  if (running.length + hiveRunning.length + live.length + needs.length === 0) return null;
  return (
    <div className="ab-box" data-testid="dash-now">
      <h3>{t("Now")}</h3>
      {needs.map((it, i) => (
        <button type="button" key={`n-${it.ref}-${i}`} className="ab-row" onClick={() => navigate(attentionPath(it) === PATHS.hive ? PATHS.tasks : attentionPath(it))}>
          <span className="ab-dot" />
          <span className="ab-x">{attentionTitle(it)}<small>{whenOf(it, new Date(now))}</small></span>
        </button>
      ))}
      {running.map((r) => (
        <button type="button" key={r.id} className="ab-row" onClick={() => navigate(`${PATHS.coding}?run=${encodeURIComponent(r.id)}`)}>
          <span className="ab-dot" />
          <span className="ab-x">{keyOf(r)} · {base(r.cwd) || oneLine(r.prompt)}<small>{t("Run")} · {elapsed(r.started, now)}</small></span>
        </button>
      ))}
      {hiveRunning.map((x) => (
        <button type="button" key={x.id} className="ab-row" onClick={() => navigate(taskPath(x.id))}>
          <span className="ab-dot" />
          <span className="ab-x">{oneLine(x.prompt)}<small>{x.worker.replace(/^superleo-/, "")} · {elapsed(x.started_at, now)}</small></span>
        </button>
      ))}
      {live.map((x) => (
        <button type="button" key={`${x.machine}|${x.cli}|${x.session}`} className="ab-row" onClick={() => navigate(PATHS.coding)}>
          <span className={`ab-dot${x.state === "waiting" ? " wait" : ""}`} />
          <span className="ab-x">{x.cli} · {x.project || oneLine(x.title ?? "") || "—"}
            <small>{t("Terminal")} · {x.state === "waiting" ? t("waiting for you") : `${x.machine || t("this Mac")} · ${elapsed(x.at, now)}`}</small>
          </span>
          {x.state === "waiting" && x.replyable && <span className="ab-tag">{bt("Reply")}</span>}
        </button>
      ))}
    </div>
  );
}

function NextBox({ now }: { now: number }) {
  const { t } = useI18n();
  const bt = useBoardT();
  const navigate = useNavigate();
  const next = useNotedUpcoming(6);
  const minute = Math.floor(now / 60000);
  const items = useMemo(() => next.items.filter((it) => it.allDay || +new Date(it.endTime || it.startTime) >= minute * 60000).slice(0, 3), [next.items, minute]);
  const today = localDay(new Date(now));
  return (
    <div className="ab-box">
      <h3>{bt("Up next")}<button type="button" className="ab-link" onClick={() => navigate(PATHS.schedule)}>{t("Schedule")}<ChevronRightIcon size={12} /></button></h3>
      {items.length === 0 && <div className="ab-none">{next.ok ? t("Nothing scheduled") : "—"}</div>}
      {items.map((it) => {
        const mode = notifyOf(it);
        return (
          <button type="button" key={`${it.id}-${it.startTime}`} className="ab-row" onClick={() => navigate(`${PATHS.schedule}?id=${encodeURIComponent(it.id)}`)}>
            <span className="ab-tm">{dayOf(it) === today ? timeLabel(it) : upcomingWhen(it, new Date(now))}</span>
            <span className="ab-x">{it.title || t("Untitled")}</span>
            {(mode === "alarm" || mode === "reminder") && <span className={`ab-tag${mode === "alarm" ? " al" : ""}`}>{notifyLabel(mode)}</span>}
          </button>
        );
      })}
    </div>
  );
}

function RecentBox({ onOpenConversation }: { onOpenConversation: (session: string) => void }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const queen = useQueenName();
  const [items, setItems] = useState<app.Conversation[] | null>(null);
  const load = useCallback(() => {
    Conversations(app.ConversationFilter.createFrom({ with: "", query: "", limit: 3 }))
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
  const open = (c: app.Conversation) => (c.kind === "queen" ? onOpenConversation(c.session) : navigate(conversationPath(c.id)));
  const who = (c: app.Conversation) => (c.kind === "queen" ? queenTitle(queen) : c.name || t(c.kind === "coding" ? "Coding" : c.kind === "worker" ? "Worker" : "Agent"));
  const date = (iso: string) => {
    const d = new Date(iso);
    if (isNaN(d.getTime())) return "";
    return d.toDateString() === new Date().toDateString() ? hm(d.getTime()) : `${d.getMonth() + 1}/${d.getDate()}`;
  };
  return (
    <div className="ab-box">
      <h3>{t("Recent")}<button type="button" className="ab-link" onClick={() => navigate(conversationPath())}>{t("All")}<ChevronRightIcon size={12} /></button></h3>
      {items && items.length === 0 && <div className="ab-none">{t("No conversations")}</div>}
      {(items ?? []).slice(0, 3).map((c) => (
        <button type="button" key={c.id} className="ab-row" onClick={() => open(c)}>
          <span className="ab-x">{withoutCallNotes(c.title) || t("Untitled")}<small>{who(c)} · {c.state === "running" ? t("now") : date(c.at)}</small></span>
        </button>
      ))}
    </div>
  );
}
