import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { CheckIcon, ChevronDownIcon, FolderIcon, MapPinIcon, PlusIcon, RepeatIcon, XIcon } from "lucide-react";
import { CallTool, NotedSchedule } from "../../wailsjs/go/app/App";
import { app } from "../../wailsjs/go/models";
import { EventsOn } from "../../wailsjs/runtime";
import { useI18n } from "../lib/i18n";
import { toast } from "../lib/toasts";
import {
  addDays, clock, completeCall, createCall, dayHeading, dayOf, deleteCall, Draft, draftOf, editCalls, Freq, groupByDay, HOLIDAY_POLICIES,
  holidayLabel, isAlarm, isPaused, isTask, leadLabel, localDay, mondayOf, monthCells, monthOf, NEW_KINDS, newDraft, NewKind, newKindLabel,
  notifyOf, projectColor, ProjectInfo, readProjects, readSchedule, readTasks, repeatLabel, repeatShort, repeats, ringSummary, ScheduleItem,
  sourceOf, TaskInfo, taskEditCalls, timeEditable, timeLabel, ToolCall, UnscheduledTask, weekdayLabel, WEEKDAYS,
} from "../lib/schedule";
import { ModeChip, ModePicker } from "../components/ModeChip";
import { InProgress, MonthCal, ProjectList, WeekGrid } from "./ScheduleParts";
import "./schedule.css";
import { SkeletonRows } from "../components/Skeleton";

const STEP = 7;
const MAX_DAYS = 62;
const LEADS = [0, 5, 10, 15, 30, 60, 120, 1440];
const VIEW_KEY = "superleo-schedule-view";

/** One noted write through CallTool; its error, if any, thrown. */
export async function runCall(c: ToolCall) {
  const res = await CallTool("noted", c.tool, c.args, app.ActionOptions.createFrom({ label: c.label, confirmed: !!c.confirmed, sessionId: "" }));
  if (!res?.ok) throw new Error(res?.error || res?.code || "failed");
  return res;
}

/** One noted read through CallTool: its structured answer, or null. */
async function readTool(tool: string, args: Record<string, unknown>): Promise<unknown> {
  try {
    const res = await CallTool("noted", tool, args, app.ActionOptions.createFrom({ label: tool, confirmed: false, sessionId: "" }));
    return res?.ok ? res.structured ?? null : null;
  } catch {
    return null;
  }
}

function useToday() {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const t = window.setInterval(() => setNow(new Date()), 30000);
    return () => window.clearInterval(t);
  }, []);
  return now;
}

type Picked =
  | { type: "item"; item: ScheduleItem }
  | { type: "task"; task: UnscheduledTask }
  | { type: "new"; kind: NewKind; day: string; time?: string };

type View = "week" | "day";

const savedView = (): View => {
  try { return localStorage.getItem(VIEW_KEY) === "day" ? "day" : "week"; } catch { return "week"; }
};

/** What the page shows of a project: the project and its colour. */
export interface ProjectTag { project: ProjectInfo; color: string }

/**
 * 日程安排: the one page for events, alarms, reminders and to-dos. A month
 * and the work under way on the left; the chosen week as an hour grid, or
 * day by day, on the right. Each item says how it reaches you (闹钟 / 提醒 /
 * 通知 / 不提醒), and opens to be changed.
 */
export default function ScheduleView() {
  const { t } = useI18n();
  const now = useToday();
  const today = localDay(now);
  const [params, setParams] = useSearchParams();
  const [sel, setSel] = useState(() => (/^\d{4}-\d\d-\d\d$/.test(params.get("date") ?? "") ? params.get("date")! : today));
  const [month, setMonth] = useState(() => monthOf(sel));
  const [view, setViewState] = useState<View>(savedView);
  const [days, setDays] = useState(STEP);
  const [data, setData] = useState<{ items: ScheduleItem[]; unscheduled: UnscheduledTask[] } | null>(null);
  const [cal, setCal] = useState<ScheduleItem[]>([]);
  const [tasks, setTasks] = useState<TaskInfo[]>([]);
  const [projects, setProjects] = useState<ProjectInfo[]>([]);
  const [err, setErr] = useState("");
  const [loading, setLoading] = useState(true);
  const [picked, setPicked] = useState<Picked | null>(null);
  const [adding, setAdding] = useState(false);
  const want = useRef(params.get("id") ?? "");

  const setView = (v: View) => {
    setViewState(v);
    try { localStorage.setItem(VIEW_KEY, v); } catch { /* private browser */ }
  };

  const mainFrom = view === "week" ? mondayOf(sel) : sel;
  const mainDays = view === "week" ? 7 : days;
  const gridFrom = monthCells(month)[0];

  const load = useCallback(async () => {
    setLoading(true);
    const side = Promise.all([
      NotedSchedule(gridFrom, 42).then((r) => readSchedule(r).items).catch(() => null),
      readTool("list_tasks", { state: "open", limit: 200 }),
      readTool("list_projects", {}),
    ]).then(([c, tk, pj]) => {
      if (c) setCal(c);
      if (tk) setTasks(readTasks(tk));
      if (pj) setProjects(readProjects(pj));
    });
    try {
      const raw = await NotedSchedule(mainFrom, mainDays);
      setData(readSchedule(raw));
      setErr("");
    } catch (e: any) {
      const msg = String(e?.message ?? e);
      setErr(/503|not configured/i.test(msg) ? t("noted is not connected, so there is no schedule to show.") : msg);
    } finally {
      setLoading(false);
    }
    await side;
  }, [mainFrom, mainDays, gridFrom, t]);

  useEffect(() => { void load(); }, [load]);
  // noted's version moves on any schedule change, the queen's included.
  useEffect(() => {
    const off = EventsOn("noted:alarms", () => void load());
    const poll = window.setInterval(() => void load(), 60000);
    window.addEventListener("focus", load);
    return () => { if (typeof off === "function") off(); window.clearInterval(poll); window.removeEventListener("focus", load); };
  }, [load]);

  // Arrived from the home's 接下来的安排 with an item: open it.
  useEffect(() => {
    if (!data || !want.current) return;
    const it = data.items.find((x) => x.id === want.current);
    want.current = "";
    if (it) setPicked({ type: "item", item: it });
    if (params.get("id") || params.get("date")) setParams({}, { replace: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data]);

  const grouped = useMemo(() => groupByDay(data?.items ?? [], mainFrom, mainDays), [data, mainFrom, mainDays]);
  const total = data?.items.length ?? 0;

  const counts = useMemo(() => {
    const m = new Map<string, number>();
    for (const it of cal) { const d = dayOf(it); if (d && !it.completed) m.set(d, (m.get(d) ?? 0) + 1); }
    return m;
  }, [cal]);
  const taskById = useMemo(() => new Map(tasks.map((k) => [k.id, k])), [tasks]);
  const tagById = useMemo(() => new Map<string, ProjectTag>(projects.map((p, i) => [p.id, { project: p, color: projectColor(p, i) }])), [projects]);
  const colorOf = useCallback((id?: string) => (id && tagById.get(id)?.color) || "var(--s-acc)", [tagById]);
  const working = useMemo(() => tasks
    .filter((k) => !k.completed && (k.progress > 0 || k.workSessions > 0 || k.workedMinutes > 0))
    .sort((a, b) => (b.lastWorkedTime ?? "").localeCompare(a.lastWorkedTime ?? "") || b.progress - a.progress), [tasks]);

  const done = (msg?: string) => {
    if (msg) toast.success(msg);
    setPicked(null);
    void load();
  };
  const complete = (id: string, title: string) =>
    runCall(completeCall(id, title)).then(() => done(t("Done: {title}", { title }))).catch((x) => toast.error(String(x?.message ?? x)));

  const pick = (day: string) => {
    if (!/^\d{4}-\d\d-\d\d$/.test(day)) return;
    setSel(day);
    setMonth(monthOf(day));
    setDays(STEP);
  };

  // A to-do from 进行中: its place on the schedule when it has one, else the to-do.
  const pickTask = (k: TaskInfo) => {
    const it = data?.items.find((x) => isTask(x) && (x.taskId || x.id) === k.id) ?? cal.find((x) => isTask(x) && (x.taskId || x.id) === k.id);
    if (it) setPicked({ type: "item", item: it });
    else setPicked({ type: "task", task: { id: k.id, title: k.title, notes: k.notes, tags: k.tags, notify: k.notify, remindBeforeMinutes: k.remindBeforeMinutes } });
  };

  const tagOf = (it: ScheduleItem): { tag?: ProjectTag; task?: TaskInfo } => {
    const task = isTask(it) ? taskById.get(it.taskId || it.id) : undefined;
    const pid = it.projectId || task?.projectId;
    return { tag: pid ? tagById.get(pid) : undefined, task };
  };

  const pickedKey = picked?.type === "item" ? `${picked.item.id}@${picked.item.startTime}` : picked?.type === "task" ? "task:" + picked.task.id : "";
  const newDay = sel < today ? today : sel;

  return (
    <div className="view sch-view">
      <div className="view-header with-action sch-head">
        <div className="view-title">{t("Schedule")}</div>
        <div className="vh-actions sch-actions">
          <div className="sch-seg sch-views" role="tablist" aria-label={t("Calendar")}>
            {(["day", "week"] as View[]).map((v) => (
              <button key={v} role="tab" aria-selected={view === v} className={view === v ? "on" : ""} onClick={() => setView(v)}>{t(v === "day" ? "Day" : "Week")}</button>
            ))}
          </div>
          <div className="sch-new">
            <button className="sch-btn pri" aria-expanded={adding} onClick={() => setAdding((v) => !v)}>
              <PlusIcon size={16} />{t("New")}<ChevronDownIcon size={14} />
            </button>
            {adding && (
              <div className="sch-menu" role="menu" onMouseLeave={() => setAdding(false)}>
                {NEW_KINDS.map((k) => (
                  <button key={k} role="menuitem" onClick={() => { setAdding(false); setPicked({ type: "new", kind: k, day: newDay }); }}>
                    {newKindLabel(k)}
                  </button>
                ))}
              </div>
            )}
          </div>
        </div>
      </div>

      <div className={`sch-body${picked ? " with-panel" : ""}`}>
        <aside className="sch-side">
          <MonthCal month={month} sel={sel} today={today} counts={counts} week={view === "week"} onMonth={setMonth} onPick={pick} />
          <InProgress tasks={working} today={today} colorOf={colorOf} onPick={pickTask} />
          <ProjectList projects={projects} colorOf={colorOf} today={today} />
        </aside>

        <div className={`sch-pane ${view}`}>
          {err && <div className="sch-error" role="alert">{err} <button className="sch-link" onClick={() => void load()}>{t("Retry")}</button></div>}
          {!err && !data && loading && <div className="sch-loading"><SkeletonRows n={6} /></div>}
          {data && view === "week" && (
            <WeekGrid start={mainFrom} items={data.items} unscheduled={data.unscheduled} today={today} sel={sel} now={now} pickedKey={pickedKey}
              tasks={taskById} onSelect={pick}
              onPick={(it) => setPicked({ type: "item", item: it })}
              onPickTask={(task) => setPicked({ type: "task", task })}
              onNew={(day, time) => setPicked({ type: "new", kind: "event", day, time })}
              onDone={complete} />
          )}
          {data && view === "day" && (
            <div className="sch-scroll">
              <div className="sch-col">
                {grouped.map((day) => (
                  <DaySection key={day.date} date={day.date} items={day.items} today={today} now={now} pickedKey={pickedKey} tagOf={tagOf}
                    onPick={(it) => setPicked({ type: "item", item: it })}
                    onAdd={() => setPicked({ type: "new", kind: "event", day: day.date })} />
                ))}
                <div className="sch-more">
                  {days < MAX_DAYS
                    ? <button className="sch-btn" onClick={() => setDays((d) => Math.min(MAX_DAYS, d + STEP))}>{t("{n} more days", { n: STEP })}</button>
                    : <button className="sch-btn" onClick={() => pick(addDays(sel, days))}>{t("Further on")}</button>}
                  <span>{t("{count} items through {date}", { count: total, date: dayHeading(addDays(sel, days - 1), today).date })}</span>
                </div>
                {data.unscheduled.length > 0 && (
                  <section className="sch-day sch-unsched">
                    <header className="sch-dayhead"><b>{t("To-dos with no time")}</b><em>{data.unscheduled.length}</em></header>
                    <ol className="sch-rows">
                      {data.unscheduled.map((task) => {
                        const info = taskById.get(task.id);
                        const tag = info?.projectId ? tagById.get(info.projectId) : undefined;
                        return (
                          <li key={task.id} className={`sch-row task${pickedKey === "task:" + task.id ? " on" : ""}`} tabIndex={0}
                            onClick={() => setPicked({ type: "task", task })}
                            onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); setPicked({ type: "task", task }); } }}>
                            <span className="sch-time" />
                            <button className="sch-check" title={t("Mark done")} aria-label={t("Mark done")}
                              onClick={(e) => { e.stopPropagation(); void complete(task.id, task.title); }}>
                              <CheckIcon size={11} strokeWidth={3} />
                            </button>
                            <span className="sch-main">
                              <b>{task.title}</b>
                              {(task.notes || tag || (info?.progress ?? 0) > 0) && (
                                <small>
                                  {(info?.progress ?? 0) > 0 && <span className="sch-pct">{info!.progress}%</span>}
                                  {tag && <ProjTag tag={tag} />}
                                  {task.notes && <span className="sch-loc">{task.notes}</span>}
                                </small>
                              )}
                            </span>
                            {notifyOf(task) !== "none" && <ModeChip mode={notifyOf(task)} compact />}
                          </li>
                        );
                      })}
                    </ol>
                  </section>
                )}
              </div>
            </div>
          )}
        </div>
        {picked && (
          <aside className="sch-panel" key={pickedKey || (picked.type === "new" ? "new:" + picked.kind + picked.day + (picked.time ?? "") : "")}>
            {picked.type === "item" && <ItemEditor item={picked.item} onClose={() => setPicked(null)} onDone={done} />}
            {picked.type === "task" && <TaskEditor task={picked.task} today={today} onClose={() => setPicked(null)} onDone={done} />}
            {picked.type === "new" && <NewEditor kind={picked.kind} day={picked.day} time={picked.time} onKind={(k) => setPicked({ ...picked, kind: k })} onClose={() => setPicked(null)} onDone={done} />}
          </aside>
        )}
      </div>
    </div>
  );
}

function ProjTag({ tag }: { tag: ProjectTag }) {
  return <span className="sch-proj"><FolderIcon size={11} style={{ color: tag.color }} />{tag.project.title}</span>;
}

function DaySection({ date, items, today, now, pickedKey, tagOf, onPick, onAdd }: {
  date: string; items: ScheduleItem[]; today: string; now: Date; pickedKey: string;
  tagOf: (it: ScheduleItem) => { tag?: ProjectTag; task?: TaskInfo };
  onPick: (it: ScheduleItem) => void; onAdd: () => void;
}) {
  const { t } = useI18n();
  const head = dayHeading(date, today);
  const isToday = date === today;
  // Where now falls in today's list: before the first timed item still to come.
  const nowAt = isToday ? items.findIndex((it) => !it.allDay && +new Date(it.startTime) >= +now) : -2;
  const rows: ReactNode[] = [];
  items.forEach((it, i) => {
    if (i === nowAt) rows.push(<NowLine key="now" now={now} />);
    const past = isToday && !it.allDay && +new Date(it.endTime || it.startTime) < +now;
    rows.push(<Row key={`${it.id}@${it.startTime}`} it={it} {...tagOf(it)} past={past || date < today} on={pickedKey === `${it.id}@${it.startTime}`} onPick={() => onPick(it)} />);
  });
  if (isToday && items.length > 0 && nowAt === -1) rows.push(<NowLine key="now" now={now} />);
  return (
    <section className={`sch-day${isToday ? " today" : ""}${items.length === 0 ? " empty" : ""}`}>
      <header className="sch-dayhead">
        {head.rel && <b>{head.rel}</b>}
        <span className={head.rel ? "" : "plain"}>{head.date}</span>
        {items.length === 0 && <em>{t("Nothing planned")}</em>}
        <button className="sch-dayadd" title={t("New on this day")} aria-label={t("New on this day")} onClick={onAdd}><PlusIcon size={14} /></button>
      </header>
      {items.length > 0 && <ol className="sch-rows">{rows}</ol>}
    </section>
  );
}

function NowLine({ now }: { now: Date }) {
  return <li className="sch-now" aria-hidden><span>{clock(now.toISOString())}</span><i /></li>;
}

function Row({ it, tag, task: info, past, on, onPick }: { it: ScheduleItem; tag?: ProjectTag; task?: TaskInfo; past: boolean; on: boolean; onPick: () => void }) {
  const { t } = useI18n();
  const mode = notifyOf(it);
  const rings = ringSummary(it.ringTimes);
  const rep = repeatShort(it);
  const lead = leadLabel(it);
  const task = isTask(it);
  const pct = info && info.progress > 0 ? info.progress : 0;
  const end = !it.allDay && !isAlarm(it) && !task && it.endTime && clock(it.endTime) !== clock(it.startTime) ? clock(it.endTime) : "";
  const cls = ["sch-row", past && "past", isPaused(it) && "paused", it.completed && "done", on && "on", it.allDay && "allday", task && "task"].filter(Boolean).join(" ");
  const hasMeta = rings || rep || lead || it.location || isPaused(it) || pct || tag;
  return (
    <li className={cls} onClick={onPick} tabIndex={0} onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onPick(); } }}
      title={it.ringTimes && it.ringTimes.length > 1 ? it.ringTimes.join(" ") : undefined}>
      <span className={`sch-time${it.allDay ? " allday" : ""}`}><b>{timeLabel(it)}</b>{end && <small>{end}</small>}</span>
      {task ? (
        <button className={`sch-check${it.completed ? " on" : ""}`} title={t("Mark done")} aria-label={t("Mark done")} disabled={it.completed}
          onClick={(e) => { e.stopPropagation(); runCall(completeCall(it.id, it.title)).then(() => toast.success(t("Done: {title}", { title: it.title }))).catch((x) => toast.error(String(x?.message ?? x))); }}>
          <CheckIcon size={11} strokeWidth={3} />
        </button>
      ) : <span className={`sch-bar m-${mode}`} aria-hidden />}
      <span className="sch-main">
        <b>{it.title || t("Untitled")}</b>
        {hasMeta && (
          <small>
            {isPaused(it) && <span className="sch-off">{t("Off")}</span>}
            {pct > 0 && <span className="sch-pct">{pct}%</span>}
            {rings && <span>{rings}</span>}
            {rep && <span className="sch-rep"><RepeatIcon size={11} />{rep}</span>}
            {lead && <span>{lead}</span>}
            {it.location && <span className="sch-loc"><MapPinIcon size={11} />{it.location}</span>}
            {tag && <ProjTag tag={tag} />}
          </small>
        )}
      </span>
      {mode !== "none" && <ModeChip mode={mode} compact />}
    </li>
  );
}

// ── The panel ────────────────────────────────────────────────────────────

function Field({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) {
  return <label className="sch-field"><span>{label}</span>{children}{hint && <small>{hint}</small>}</label>;
}

function PanelHead({ kind, title, onClose }: { kind: string; title?: string; onClose: () => void }) {
  const { t } = useI18n();
  return (
    <header className="sch-phead">
      <div><em>{kind}</em>{title && <b>{title}</b>}</div>
      <button className="sch-x" onClick={onClose} title={t("Close")} aria-label={t("Close")}><XIcon size={16} /></button>
    </header>
  );
}

function useRunner(onDone: (msg?: string) => void) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const run = async (calls: ToolCall[], msg = t("Saved")) => {
    if (!calls.length) { onDone(); return; }
    setBusy(true); setErr("");
    try {
      for (const c of calls) await runCall(c);
      onDone(msg);
    } catch (e: any) {
      setErr(String(e?.message ?? e));
    } finally {
      setBusy(false);
    }
  };
  return { busy, err, run };
}

const leadText = (n: number, t: (k: string, v?: Record<string, string | number>) => string) =>
  n === 0 ? t("At the time") : n % 1440 === 0 ? t("{n} d before", { n: n / 1440 }) : n % 60 === 0 ? t("{n} h before", { n: n / 60 }) : t("{n} min before", { n });

function LeadPicker({ value, onChange }: { value: number; onChange: (n: number) => void }) {
  const { t } = useI18n();
  const opts = LEADS.includes(value) ? LEADS : [...LEADS, value].sort((a, b) => a - b);
  return (
    <select className="sch-input" value={value} onChange={(e) => onChange(Number(e.target.value))}>
      {opts.map((n) => <option key={n} value={n}>{leadText(n, t)}</option>)}
    </select>
  );
}

const FREQS: { key: Freq; label: string }[] = [
  { key: "once", label: "Once" }, { key: "daily", label: "Daily" }, { key: "weekly", label: "Weekly" }, { key: "monthly", label: "Monthly" },
];
const UNITS: Record<string, string> = { daily: "days", weekly: "weeks", monthly: "months", hourly: "hours", minutely: "minutes" };

/** 重复 and 节假日: a repeat rule the way a person says it. */
function RepeatFields({ d, set, alarm, extra }: { d: Draft; set: (p: Partial<Draft>) => void; alarm: boolean; extra?: Freq }) {
  const { t, language } = useI18n();
  const freqs = extra && !FREQS.some((f) => f.key === extra) ? [...FREQS, { key: extra, label: extra === "hourly" ? "Hourly" : "Every few minutes" }] : FREQS;
  return (
    <>
      <Field label={t("Repeat")}>
        <div className="sch-seg">
          {freqs.map((f) => (
            <button key={f.key} type="button" className={d.freq === f.key ? "on" : ""} onClick={() => set({ freq: f.key })}>{t(f.label)}</button>
          ))}
        </div>
      </Field>
      {d.freq !== "once" && (
        <div className="sch-inline">
          <span>{t("Every")}</span>
          <input className="sch-input num" type="number" min={1} max={99} value={d.interval} onChange={(e) => set({ interval: Math.max(1, Number(e.target.value) || 1) })} />
          <span>{t(UNITS[d.freq] ?? "days")}</span>
          {d.freq === "monthly" && (
            <>
              {language !== "zh-CN" && <span>on day</span>}
              <input className="sch-input num" type="number" min={1} max={31} value={d.byMonthDay} onChange={(e) => set({ byMonthDay: Math.min(31, Math.max(1, Number(e.target.value) || 1)) })} />
              {language === "zh-CN" && <span>号</span>}
            </>
          )}
        </div>
      )}
      {d.freq === "weekly" && (
        <div className="sch-days" role="group" aria-label={t("Days of the week")}>
          {WEEKDAYS.map((w) => {
            const on = d.byDay.includes(w);
            return <button key={w} type="button" className={on ? "on" : ""} aria-pressed={on}
              onClick={() => set({ byDay: on ? d.byDay.filter((x) => x !== w) : WEEKDAYS.filter((x) => x === w || d.byDay.includes(x)) })}>{weekdayLabel(w)}</button>;
          })}
        </div>
      )}
      {d.freq !== "once" && (alarm || d.freq === "daily" || d.freq === "weekly") && (
        <Field label={t("Holidays")}>
          <select className="sch-input" value={d.holidayPolicy} onChange={(e) => set({ holidayPolicy: e.target.value })}>
            {HOLIDAY_POLICIES.map((p) => <option key={p} value={p}>{holidayLabel(p)}</option>)}
          </select>
        </Field>
      )}
    </>
  );
}

function DeleteButton({ onDelete, busy, note }: { onDelete: () => void; busy: boolean; note?: string }) {
  const { t } = useI18n();
  const [sure, setSure] = useState(false);
  if (!sure) return <button type="button" className="sch-btn ghost danger" onClick={() => setSure(true)} disabled={busy}>{t("Delete")}</button>;
  return (
    <span className="sch-sure">
      <span>{note ?? t("Delete it for good?")}</span>
      <button type="button" className="sch-btn danger" onClick={onDelete} disabled={busy}>{t("Delete it")}</button>
      <button type="button" className="sch-btn ghost" onClick={() => setSure(false)}>{t("Keep")}</button>
    </span>
  );
}

const KIND_NAMES: Record<string, string> = { event: "Event", task: "To-do", alarm: "Alarm" };

function ItemEditor({ item, onClose, onDone }: { item: ScheduleItem; onClose: () => void; onDone: (msg?: string) => void }) {
  const { t } = useI18n();
  const [d, setD] = useState<Draft>(() => draftOf(item));
  const set = (p: Partial<Draft>) => setD((x) => ({ ...x, ...p }));
  const { busy, err, run } = useRunner(onDone);
  const alarm = isAlarm(item);
  const task = isTask(item);
  const calls = editCalls(item, d);
  const extraFreq = draftOf(item).freq;
  const rings = ringSummary(item.ringTimes);
  return (
    <form className="sch-form" onSubmit={(e) => { e.preventDefault(); void run(calls); }}>
      <PanelHead kind={t(KIND_NAMES[sourceOf(item)] ?? "Event")} onClose={onClose} />
      <Field label={t("Title")}>
        <input className="sch-input" value={d.title} onChange={(e) => set({ title: e.target.value })} autoFocus />
      </Field>
      {timeEditable(item) ? (
        <div className="sch-two">
          <Field label={alarm && repeats(item) ? t("Starts") : t("Date")}>
            <input className="sch-input" type="date" value={d.date} onChange={(e) => set({ date: e.target.value })} />
          </Field>
          {!d.allDay && !(alarm && item.schedule?.recurrence?.byTime?.length) && (
            <Field label={t("Time")}>
              <input className="sch-input" type="time" value={d.time} onChange={(e) => set({ time: e.target.value })} />
            </Field>
          )}
        </div>
      ) : (
        <Field label={t("Time")} hint={t("A repeating event is moved by telling the queen.")}>
          <div className="sch-static"><RepeatIcon size={14} />{timeLabel(item)} · {repeatLabel(item)}</div>
        </Field>
      )}
      {rings && <div className="sch-note">{t("Rings today")}: {item.ringTimes!.join("  ")}</div>}
      <Field label={t("How it reaches you")}>
        <ModePicker value={d.notify} onChange={(n) => set({ notify: n })} />
      </Field>
      {d.notify !== "none" && !(alarm && d.notify === "alarm") && (
        <Field label={t("Lead")}>
          <LeadPicker value={d.lead} onChange={(n) => set({ lead: n })} />
        </Field>
      )}
      {alarm && (
        <label className="sch-switch">
          <input type="checkbox" checked={d.enabled} onChange={(e) => set({ enabled: e.target.checked })} />
          <i aria-hidden />
          <span>{d.enabled ? t("On") : t("Off")}</span>
          <small>{t("An alarm that is off keeps its settings and does not ring.")}</small>
        </label>
      )}
      {alarm && <RepeatFields d={d} set={set} alarm extra={extraFreq} />}
      {sourceOf(item) === "event" && (
        <Field label={t("Location")}>
          <div className="sch-withicon"><MapPinIcon size={14} /><input className="sch-input" value={d.location} onChange={(e) => set({ location: e.target.value })} /></div>
        </Field>
      )}
      {item.description && <p className="sch-desc">{item.description}</p>}
      {err && <div className="sch-error" role="alert">{err}</div>}
      <footer className="sch-pfoot">
        <DeleteButton busy={busy} note={repeats(item) ? t("Every repeat goes with it. Delete?") : undefined}
          onDelete={() => void run([deleteCall(item)], t("Deleted {title}", { title: item.title }))} />
        <span className="sch-sp" />
        {task && !item.completed && (
          <button type="button" className="sch-btn ghost" disabled={busy} onClick={() => void run([completeCall(item.id, item.title)], t("Done: {title}", { title: item.title }))}>
            <CheckIcon size={14} />{t("Mark done")}
          </button>
        )}
        <button type="submit" className="sch-btn pri" disabled={busy || !calls.length || !d.title.trim()}>{busy ? t("Saving…") : t("Save")}</button>
      </footer>
    </form>
  );
}

function TaskEditor({ task, today, onClose, onDone }: { task: UnscheduledTask; today: string; onClose: () => void; onDone: (msg?: string) => void }) {
  const { t } = useI18n();
  const [d, setD] = useState({ title: task.title, notify: notifyOf(task), date: today, time: "" });
  const { busy, err, run } = useRunner(onDone);
  const calls = taskEditCalls(task, d);
  return (
    <form className="sch-form" onSubmit={(e) => { e.preventDefault(); void run(calls); }}>
      <PanelHead kind={t("To-do")} onClose={onClose} />
      <Field label={t("Title")}><input className="sch-input" value={d.title} onChange={(e) => setD({ ...d, title: e.target.value })} autoFocus /></Field>
      <div className="sch-two">
        <Field label={t("Date")}><input className="sch-input" type="date" value={d.date} onChange={(e) => setD({ ...d, date: e.target.value })} /></Field>
        <Field label={t("Time")}><input className="sch-input" type="time" value={d.time} onChange={(e) => setD({ ...d, time: e.target.value })} /></Field>
      </div>
      <div className="sch-note">{t("Give it a time to put it on the schedule.")}</div>
      <Field label={t("How it reaches you")}><ModePicker value={d.notify} onChange={(n) => setD({ ...d, notify: n })} /></Field>
      {err && <div className="sch-error" role="alert">{err}</div>}
      <footer className="sch-pfoot">
        <span className="sch-sp" />
        <button type="button" className="sch-btn ghost" disabled={busy} onClick={() => void run([completeCall(task.id, task.title)], t("Done: {title}", { title: task.title }))}><CheckIcon size={14} />{t("Mark done")}</button>
        <button type="submit" className="sch-btn pri" disabled={busy || !calls.length || !d.title.trim()}>{busy ? t("Saving…") : t("Save")}</button>
      </footer>
    </form>
  );
}

function NewEditor({ kind, day, time, onKind, onClose, onDone }: { kind: NewKind; day: string; time?: string; onKind: (k: NewKind) => void; onClose: () => void; onDone: (msg?: string) => void }) {
  const { t } = useI18n();
  const fresh = (k: NewKind) => { const n = newDraft(k, day); return time ? { ...n, time } : n; };
  const [d, setD] = useState<Draft>(() => fresh(kind));
  const [timed, setTimed] = useState(kind !== "todo");
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { setD((x) => ({ ...fresh(kind), title: x.title, date: x.date })); setTimed(kind !== "todo"); }, [kind, day, time]);
  const set = (p: Partial<Draft>) => setD((x) => ({ ...x, ...p }));
  const { busy, err, run } = useRunner(onDone);
  const alarmLike = kind === "alarm" || kind === "reminder";
  const draft = kind === "todo" && !timed ? { ...d, time: "" } : d;
  const placeholder: Record<NewKind, string> = { event: "e.g. Dentist", alarm: "e.g. Get up", reminder: "e.g. Take the medicine", todo: "e.g. Buy milk" };
  return (
    <form className="sch-form" onSubmit={(e) => { e.preventDefault(); if (d.title.trim()) void run([createCall(kind, draft)], t("Added {title}", { title: d.title.trim() })); }}>
      <PanelHead kind={t("New")} onClose={onClose} />
      <div className="sch-seg kinds" role="radiogroup" aria-label={t("Kind")}>
        {NEW_KINDS.map((k) => <button key={k} type="button" role="radio" aria-checked={k === kind} className={k === kind ? "on" : ""} onClick={() => onKind(k)}>{newKindLabel(k)}</button>)}
      </div>
      <Field label={t("Title")}>
        <input className="sch-input" value={d.title} placeholder={t(placeholder[kind])} onChange={(e) => set({ title: e.target.value })} autoFocus />
      </Field>
      {kind === "todo" && (
        <label className="sch-switch">
          <input type="checkbox" checked={timed} onChange={(e) => setTimed(e.target.checked)} />
          <i aria-hidden /><span>{t("At a time")}</span>
          <small>{t("Without a time it waits under To-dos with no time.")}</small>
        </label>
      )}
      {(kind !== "todo" || timed) && (
        <div className="sch-two">
          <Field label={alarmLike && d.freq !== "once" ? t("Starts") : t("Date")}>
            <input className="sch-input" type="date" value={d.date} onChange={(e) => set({ date: e.target.value })} />
          </Field>
          {!d.allDay && (
            <Field label={t("Time")}>
              <input className="sch-input" type="time" value={d.time} onChange={(e) => set({ time: e.target.value })} />
            </Field>
          )}
        </div>
      )}
      {kind === "event" && (
        <label className="sch-check-row"><input type="checkbox" checked={d.allDay} onChange={(e) => set({ allDay: e.target.checked, notify: e.target.checked ? "none" : "reminder" })} />{t("All day")}</label>
      )}
      {(kind === "event" || (kind === "todo" && timed)) && (
        <Field label={t("How it reaches you")}>
          <ModePicker value={d.notify} onChange={(n) => set({ notify: n })} />
        </Field>
      )}
      {(kind === "event" || (kind === "todo" && timed)) && d.notify !== "none" && (
        <Field label={t("Lead")}><LeadPicker value={d.lead} onChange={(n) => set({ lead: n })} /></Field>
      )}
      {alarmLike && <div className="sch-note">{kind === "alarm" ? t("An alarm rings on the phone like an alarm clock, through silent mode, until stopped.") : t("A reminder is a notification on the phone at the time, with snooze.")}</div>}
      {(alarmLike || kind === "event") && <RepeatFields d={d} set={set} alarm={alarmLike} />}
      {kind === "event" && (
        <Field label={t("Location")}>
          <div className="sch-withicon"><MapPinIcon size={14} /><input className="sch-input" value={d.location} onChange={(e) => set({ location: e.target.value })} /></div>
        </Field>
      )}
      {err && <div className="sch-error" role="alert">{err}</div>}
      <footer className="sch-pfoot">
        <span className="sch-sp" />
        <button type="button" className="sch-btn ghost" onClick={onClose}>{t("Cancel")}</button>
        <button type="submit" className="sch-btn pri" disabled={busy || !d.title.trim()}>{busy ? t("Saving…") : t("Add")}</button>
      </footer>
    </form>
  );
}
