import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import { BotIcon, CalendarClock, Check, ChevronRightIcon, CpuIcon, HeartPulse, HexagonIcon, ListTodo, TerminalIcon, TrendingUp, TriangleAlertIcon, type LucideIcon } from "lucide-react";
import { Conversations, NotedHoldings, NotedSchedule, ScheduledPrompts, WellbeingStatus, WellbeingToday } from "../../wailsjs/go/app/App";
import { app, agent } from "../../wailsjs/go/models";
import { EventsOn } from "../../wailsjs/runtime";
import { useI18n } from "../lib/i18n";
import { PATHS, attentionPath, conversationPath } from "../lib/routes";
import { attentionTitle, whenOf, type AttentionItem } from "../canvas/attention";
import { oneLine } from "../canvas/data";
import { useNotedUpcoming } from "../lib/useNotedUpcoming";
import {
  clock, dayOf, isAlarm, isTask, localDay, notifyLabel, notifyOf, projectColor, projectPercent, readProjects, readSchedule, readTasks,
  upcomingWhen, workedLine, type ProjectInfo, type ScheduleItem, type TaskInfo,
} from "../lib/schedule";
import { useFeatures } from "../lib/features";
import { queenTitle, useQueenName } from "../lib/queenName";
import { isLegacyConsole, legacyConsoleTitle, pairedDeviceNames, useQueenSession } from "../lib/queenConsole";
import { firstLine, parseTime, withoutCallNotes } from "../lib/format";
import { readTool } from "./ScheduleView";
import { recoveryTone, recoveryWord } from "./WellbeingView";
import { get, type Day } from "./wellbeingParts";
import { money, sPct, totalsOf, type Holding } from "./FinanceView";

// The person's day on the dashboard: four figures, the day in time order,
// what needs them, the latest conversations and the day's automations.

/** The board's own words where they differ from the rest of the app. */
function useBt() {
  const { t } = useI18n();
  return (k: string) => { const v = t(`board·${k}`); return v.startsWith("board·") ? t(k) : v; };
}

const hm = (t: number) => new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
const at = (it: ScheduleItem) => +new Date(it.startTime);
const endOf = (it: ScheduleItem) => +new Date(it.endTime || it.startTime);

/** A waiting terminal, as CodingSessions sends it. */
export type WaitingTerm = { machine?: string; cli: string; session?: string; project?: string; title?: string; state: string; at: string; replyable: boolean };

// ── Loaders ────────────────────────────────────────────────────────────────

export interface TodayData {
  ok: boolean;
  loaded: boolean;
  items: ScheduleItem[];
  tasks: TaskInfo[];
  projects: ProjectInfo[];
  open: number;
  /** The first open to-do with no time. */
  firstOpen?: string;
}

/** Today in noted: the day's items (NotedSchedule), the open to-dos and the
 *  projects; read again when the schedule changes, the window comes back or
 *  the day turns. */
export function useTodayNoted(now: number): TodayData {
  const day = localDay(new Date(now));
  const [s, setS] = useState<TodayData>({ ok: false, loaded: false, items: [], tasks: [], projects: [], open: 0 });
  useEffect(() => {
    let alive = true;
    const load = async () => {
      const [sch, tk, pj] = await Promise.all([
        NotedSchedule(day, 1).then((r) => readSchedule(r)).catch(() => null),
        readTool("list_tasks", { state: "open", limit: 200 }),
        readTool("list_projects", {}),
      ]);
      if (!alive) return;
      setS({
        ok: !!sch, loaded: true,
        items: sch?.items ?? [], open: sch?.unscheduled.length ?? 0, firstOpen: sch?.unscheduled[0]?.title,
        tasks: tk ? readTasks(tk) : [], projects: pj ? readProjects(pj) : [],
      });
    };
    void load();
    const off = EventsOn("noted:alarms", () => void load());
    window.addEventListener("focus", load);
    return () => { alive = false; if (typeof off === "function") off(); window.removeEventListener("focus", load); };
  }, [day]);
  return s;
}

interface Wellbeing { recovery?: number; sleep?: number; stress?: number }

/** Vita's readiness and last night's sleep, when it is on and connected. */
function useWellbeing(on: boolean): Wellbeing | null {
  const [w, setW] = useState<Wellbeing | null>(null);
  useEffect(() => {
    if (!on) { setW(null); return; }
    let alive = true;
    const load = async () => {
      try {
        const st = (await WellbeingStatus()) as { connected?: boolean; days?: number };
        if (!st?.connected || !st.days) { if (alive) setW(null); return; }
        const td = (await WellbeingToday()) as { day?: Day };
        const d = td?.day;
        if (alive) setW(d ? { recovery: get(d, "recovery"), sleep: get(d, "sleep_h") ?? get(d, "sleep.asleep_h"), stress: get(d, "stress") } : null);
      } catch { if (alive) setW(null); }
    };
    void load();
    const off = EventsOn("wellbeing:changed", () => void load());
    return () => { alive = false; if (typeof off === "function") off(); };
  }, [on]);
  return w;
}

/** The holdings, when finance is on and answers. */
function useHoldings(on: boolean): Holding[] | null {
  const [h, setH] = useState<Holding[] | null>(null);
  useEffect(() => {
    if (!on) { setH(null); return; }
    let alive = true;
    NotedHoldings()
      .then((r) => { if (alive) setH((((r as { items?: Holding[] })?.items) ?? []).filter((x) => !x.archived)); })
      .catch(() => { if (alive) setH(null); });
    return () => { alive = false; };
  }, [on]);
  return h;
}

// ── The figures ────────────────────────────────────────────────────────────

function Card({ icon, label, chip, value, sub, onClick }: { icon: ReactNode; label: string; chip?: ReactNode; value: ReactNode; sub: ReactNode; onClick?: () => void }) {
  return (
    <button type="button" className="ab-st ab-st-btn" onClick={onClick}>
      <div className="ab-st-k"><span className="ab-ico">{icon}</span>{label}{chip}</div>
      <div className="ab-st-v">{value}</div>
      <div className="ab-st-sub">{sub}</div>
    </button>
  );
}

export function TodayCards({ now, noted }: { now: number; noted: TodayData }) {
  const { t, language } = useI18n();
  const navigate = useNavigate();
  const bt = useBt();
  const features = useFeatures();
  const next = useNotedUpcoming(8);
  const well = useWellbeing(!!features?.wellbeing);
  const holdings = useHoldings(!!features?.finance);
  const loc = language === "en" ? undefined : language;
  const today = localDay(new Date(now));
  const minute = Math.floor(now / 60000) * 60000;

  const todos = noted.items.filter((it) => isTask(it) && dayOf(it) === today);
  const done = todos.filter((it) => it.completed).length;
  const left = todos.filter((it) => !it.completed).sort((a, b) => at(a) - at(b));
  const nextTodo = left.find((it) => endOf(it) >= minute) ?? left[0];

  const events = noted.items.filter((it) => !isTask(it) && dayOf(it) === today).length;
  const nextEvent = next.items.find((it) => !isTask(it) && (it.allDay || endOf(it) >= minute));
  const mode = nextEvent ? notifyOf(nextEvent) : "none";

  const totals = useMemo(() => totalsOf(holdings ?? []).filter((s) => s.value > 0), [holdings]);
  const main = totals[0];

  return (
    <div className="ab-stats">
      {noted.ok && (
        <Card icon={<ListTodo size={14} />} label={t("To-dos")} onClick={() => navigate(PATHS.schedule)}
          chip={todos.length > 0 ? <span className={`ab-chip ${done === todos.length ? "up" : "mute"}`}>{t("{n} done", { n: done })}</span> : null}
          value={<>{todos.length}<small> {t("today")}</small></>}
          sub={nextTodo ? <>{nextTodo.allDay ? "" : `${clock(nextTodo.startTime)} · `}{nextTodo.title || t("Untitled")}</> : noted.open ? [t("{n} open", { n: noted.open }), noted.firstOpen].filter(Boolean).join(" · ") : "—"} />
      )}
      {next.ok && (
        <Card icon={<CalendarClock size={14} />} label={bt("Schedule")} onClick={() => navigate(nextEvent ? `${PATHS.schedule}?id=${encodeURIComponent(nextEvent.id)}` : PATHS.schedule)}
          chip={(mode === "alarm" || mode === "reminder") ? <span className={`ab-chip ${mode === "alarm" ? "warn" : "mute"}`}>{notifyLabel(mode)}</span> : null}
          value={nextEvent ? <>{dayOf(nextEvent) === today ? (nextEvent.allDay ? t("All day") : clock(nextEvent.startTime)) : upcomingWhen(nextEvent, new Date(now))}</> : <>—</>}
          sub={nextEvent ? nextEvent.title || t("Untitled") : t("{n} today", { n: events })} />
      )}
      {well && well.recovery !== undefined && (
        <Card icon={<HeartPulse size={14} />} label={t("Wellbeing")} onClick={() => navigate(PATHS.wellbeing)}
          chip={<span className={`ab-chip ${recoveryTone(well.recovery) === "good" ? "up" : recoveryTone(well.recovery) === "low" ? "dn" : "warn"}`}>{t(recoveryWord(well.recovery))}</span>}
          value={<>{Math.round(well.recovery)}<small> {t("readiness")}</small></>}
          sub={[well.sleep !== undefined ? `${t("Sleep")} ${well.sleep.toFixed(1)} h` : "", well.stress !== undefined ? `${t("Stress")} ${Math.round(well.stress)}` : ""].filter(Boolean).join(" · ") || "—"} />
      )}
      {main && (() => {
        const prev = main.value - main.day;
        const f = prev > 0 ? main.day / prev : undefined;
        const dir = main.day > 1e-9 ? "up" : main.day < -1e-9 ? "dn" : "mute";
        return (
          <Card icon={<TrendingUp size={14} />} label={t("Investments")} onClick={() => navigate(PATHS.finance)}
            chip={<span className="ab-chip mute">{main.currency}{totals.length > 1 ? ` +${totals.length - 1}` : ""}</span>}
            value={<span className={`ab-v-${dir}`}>{sPct(f)}</span>}
            sub={`${money(main.value, main.currency, loc, 0)} · ${main.day >= 0 ? "+" : "−"}${money(Math.abs(main.day), main.currency, loc, 0)} ${t("today")}`} />
        );
      })()}
    </div>
  );
}

// ── The day ────────────────────────────────────────────────────────────────

/** Today in time order with a line at now; then the to-dos under way and the projects. */
export function DayColumn({ now, noted }: { now: number; noted: TodayData }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const today = localDay(new Date(now));
  const items = useMemo(() => noted.items.filter((it) => dayOf(it) === today)
    .sort((a, b) => Number(!a.allDay) - Number(!b.allDay) || at(a) - at(b)), [noted.items, today]);
  const working = useMemo(() => noted.tasks
    .filter((k) => !k.completed && (k.progress > 0 || k.workSessions > 0 || k.workedMinutes > 0))
    .sort((a, b) => (b.lastWorkedTime ?? "").localeCompare(a.lastWorkedTime ?? "") || b.progress - a.progress).slice(0, 4), [noted.tasks]);
  const colorOf = useMemo(() => {
    const m = new Map(noted.projects.map((p, i) => [p.id, projectColor(p, i)]));
    return (id?: string) => (id && m.get(id)) || "var(--ab-ink)";
  }, [noted.projects]);
  const projects = noted.projects.filter((p) => (p.progress?.tasksTotal ?? 0) > 0 || p.pinned).slice(0, 4);

  const nowAt = items.findIndex((it) => !it.allDay && at(it) > now);
  const line = nowAt === -1 ? items.length : nowAt;
  const open = (it: ScheduleItem) => navigate(`${PATHS.schedule}?id=${encodeURIComponent(it.id)}`);

  return (
    <section className="ab-box ab-day" aria-label={t("Today")}>
      <h3>{t("Today")}<em>{items.length}</em><button type="button" className="ab-link" onClick={() => navigate(PATHS.schedule)}>{t("Schedule")}<ChevronRightIcon size={12} /></button></h3>
      {!noted.loaded && <div className="ab-none">…</div>}
      {noted.loaded && !noted.ok && <div className="ab-none">{t("Schedule not connected")}</div>}
      {noted.ok && items.length === 0 && <div className="ab-none">{t("Nothing scheduled")}</div>}
      {items.length > 0 && (
        <ol className="ab-tl">
          {items.map((it, i) => {
            const past = !it.allDay && endOf(it) < now;
            const m = notifyOf(it);
            const kind = isTask(it) ? "task" : isAlarm(it) ? "alarm" : "event";
            return [
              i === line && <li key="now" className="ab-tl-now" aria-label={t("now")}><span>{hm(now)}</span><i /></li>,
              <li key={`${it.id}-${it.startTime}`}>
                <button type="button" className={`ab-tl-row k-${kind}${past ? " past" : ""}${it.completed ? " done" : ""}`} onClick={() => open(it)}>
                  <span className="ab-tm">{it.allDay ? t("All day") : clock(it.startTime)}</span>
                  <span className="ab-tl-mk" aria-hidden>{kind === "task" ? (it.completed ? <Check size={10} strokeWidth={3} /> : null) : null}</span>
                  <span className="ab-x">{it.title || t("Untitled")}
                    {(it.endTime && !it.allDay && kind === "event") || it.location ? <small>{[it.endTime && !it.allDay && kind === "event" ? `${clock(it.startTime)}–${clock(it.endTime)}` : "", it.location ?? ""].filter(Boolean).join(" · ")}</small> : null}
                  </span>
                  {kind === "task" && <span className="ab-tag">{t("To-do")}</span>}
                  {(m === "alarm" || m === "reminder") && <span className={`ab-tag${m === "alarm" ? " al" : ""}`}>{notifyLabel(m)}</span>}
                </button>
              </li>,
            ];
          })}
          {line === items.length && <li key="now" className="ab-tl-now" aria-label={t("now")}><span>{hm(now)}</span><i /></li>}
        </ol>
      )}

      {working.length > 0 && (
        <div className="ab-sub">
          <h4>{t("In progress")}</h4>
          {working.map((k) => {
            const w = workedLine(k, today);
            return (
              <button type="button" key={k.id} className="ab-prog" onClick={() => navigate(PATHS.schedule)}>
                <span className="ab-prog-t"><i style={{ background: colorOf(k.projectId) }} /><b>{k.title || t("Untitled")}</b><em>{k.progress}%</em></span>
                <span className="ab-bar"><i style={{ width: `${k.progress}%`, background: colorOf(k.projectId) }} /></span>
                {w && <small>{w}</small>}
              </button>
            );
          })}
        </div>
      )}
      {projects.length > 0 && (
        <div className="ab-sub">
          <h4>{t("Projects")}</h4>
          {projects.map((p) => {
            const pc = projectPercent(p);
            const pr = p.progress ?? {};
            return (
              <button type="button" key={p.id} className="ab-prog" onClick={() => navigate(PATHS.schedule)}>
                <span className="ab-prog-t"><i style={{ background: colorOf(p.id) }} /><b>{p.title || t("Untitled")}</b>
                  {(pr.tasksOverdue ?? 0) > 0 && <span className="ab-late">{t("{n} overdue", { n: pr.tasksOverdue! })}</span>}
                  <em>{pr.tasksTotal ? `${pr.tasksDone ?? 0}/${pr.tasksTotal}` : `${Math.round(pc)}%`}</em></span>
                <span className="ab-bar"><i style={{ width: `${pc}%`, background: colorOf(p.id) }} /></span>
                {pr.nextTitle && <small>{pr.nextTitle}</small>}
              </button>
            );
          })}
        </div>
      )}
    </section>
  );
}

// ── Needs you ──────────────────────────────────────────────────────────────

export function NeedsYou({ now, needs, waiting }: { now: number; needs: AttentionItem[]; waiting: WaitingTerm[] }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const bt = useBt();
  const n = needs.length + waiting.length;
  return (
    <section className={`ab-box ab-needs${n ? " on" : ""}`} data-testid="dash-now" aria-label={bt("Needs you")}>
      <h3>{bt("Needs you")}<em>{n}</em></h3>
      {n === 0 && <div className="ab-none">{t("Nothing waiting")}</div>}
      {needs.map((it, i) => (
        <button type="button" key={`n-${it.kind}-${it.ref}-${i}`} className="ab-row" onClick={() => { const p = attentionPath(it); navigate(p === PATHS.hive ? PATHS.tasks : p); }}>
          <span className="ab-need-ico"><TriangleAlertIcon size={13} /></span>
          <span className="ab-x">{attentionTitle(it)}<small>{[it.detail ? oneLine(it.detail) : "", whenOf(it, new Date(now))].filter(Boolean).join(" · ")}</small></span>
          {it.kind === "approval" && <span className="ab-tag sig">{t("Approve")}</span>}
        </button>
      ))}
      {waiting.map((x) => (
        <button type="button" key={`${x.machine}|${x.cli}|${x.session}`} className="ab-row" onClick={() => navigate(PATHS.coding)}>
          <span className="ab-need-ico"><TerminalIcon size={13} /></span>
          <span className="ab-x">{x.cli} · {x.project || oneLine(x.title ?? "") || "—"}<small>{t("Terminal")} · {x.machine || t("this Mac")}</small></span>
          {x.replyable && <span className="ab-tag sig">{bt("Reply")}</span>}
        </button>
      ))}
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

function when(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  const now = new Date();
  if (d.toDateString() === now.toDateString()) return hm(d.getTime());
  return d.getFullYear() === now.getFullYear() ? `${d.getMonth() + 1}/${d.getDate()}` : d.toLocaleDateString();
}

export function RecentConversations({ onOpenConversation }: { onOpenConversation: (session: string) => void }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const queen = useQueenName();
  const consoleId = useQueenSession();
  const [items, setItems] = useState<app.Conversation[] | null>(null);
  const load = useCallback(() => {
    Conversations(app.ConversationFilter.createFrom({ with: "", query: "", limit: 6 }))
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
  return (
    <section className="ab-box" aria-label={t("Recent conversations")}>
      <h3>{t("Recent conversations")}<button type="button" className="ab-link" onClick={() => navigate(conversationPath())}>{t("All")}<ChevronRightIcon size={12} /></button></h3>
      {items && items.length === 0 && <div className="ab-none">{t("No conversations")}</div>}
      {(items ?? []).slice(0, 6).map((c) => {
        const k = KIND[c.kind] ?? KIND.agent;
        const main = c.kind === "queen" && c.session === consoleId;
        return (
          <button type="button" key={c.id} className={`ab-row ab-conv k-${c.kind}`} onClick={() => open(c)}>
            <span className="ab-conv-ico"><k.Icon size={13} /></span>
            <span className="ab-x">{main ? t("Main conversation") : withoutCallNotes(c.title) || t("Untitled")}
              <small>{c.kind === "queen" ? queenTitle(queen) : c.name || t(k.label)}{c.source === "terminal" ? ` · ${t("Terminal")}` : ""}</small>
            </span>
            <span className="ab-when-r">{c.state === "running" ? <i className="ab-dot" title={t("Working…")} /> : when(c.at)}</span>
          </button>
        );
      })}
    </section>
  );
}

// ── Automations ────────────────────────────────────────────────────────────

/** The scheduled prompts that ran today or will run before midnight. */
export function TodayAutomations({ now }: { now: number }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const [list, setList] = useState<agent.ScheduledPrompt[] | null>(null);
  useEffect(() => {
    let alive = true;
    const load = () => ScheduledPrompts().then((r) => { if (alive) setList(Array.isArray(r) ? r : []); }).catch(() => { if (alive) setList((c) => c ?? []); });
    load();
    const offs = ["schedule:run", "schedule:changed"].map((n) => EventsOn(n, load));
    window.addEventListener("focus", load);
    return () => { alive = false; offs.forEach((o) => typeof o === "function" && o()); window.removeEventListener("focus", load); };
  }, []);
  const day = localDay(new Date(now));
  const rows = useMemo(() => (list ?? []).flatMap((s) => {
    const last = parseTime(s.last_run), next = parseTime(s.next_run);
    const ran = last && localDay(last) === day ? last : null;
    const coming = s.enabled && next && localDay(next) === day ? next : null;
    if (!s.running && !ran && !coming) return [];
    const sortAt = s.running ? now : ran && (!coming || ran.getTime() > now) ? ran.getTime() : coming!.getTime();
    return [{ s, ran, coming, sortAt }];
  }).sort((a, b) => a.sortAt - b.sortAt), [list, day, now]);
  if (list && list.length === 0) return null;
  return (
    <section className="ab-box" aria-label={t("Automations")}>
      <h3>{t("Automations")}<em>{rows.length}</em><button type="button" className="ab-link" onClick={() => navigate(PATHS.routines)}>{t("All")}<ChevronRightIcon size={12} /></button></h3>
      {list && rows.length === 0 && <div className="ab-none">{t("None today")}</div>}
      {rows.map(({ s, ran, coming }) => (
        <button type="button" key={s.id} className="ab-row" onClick={() => navigate(PATHS.routines)}>
          <span className="ab-tm">{s.running ? hm(now) : ran && !coming ? hm(ran.getTime()) : coming ? hm(coming.getTime()) : "—"}</span>
          <span className="ab-x">{s.note || firstLine(s.prompt, 80)}<small className="ab-mono">{s.schedule}</small></span>
          {s.running ? <span className="ab-tag sig"><i className="ab-dot sm" />{t("Running")}</span>
            : ran && !coming ? <span className="ab-tag ok">{t("Ran")}</span>
              : <span className="ab-tag">{ran ? t("Ran {at}", { at: hm(ran.getTime()) }) : t("Next")}</span>}
        </button>
      ))}
    </section>
  );
}
