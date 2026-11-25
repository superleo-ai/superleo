import { useEffect, useMemo, useRef } from "react";
import { CheckIcon, ChevronLeftIcon, ChevronRightIcon } from "lucide-react";
import { useI18n } from "../lib/i18n";
import {
  addDays, addMonths, clock, daysLeftLabel, dayOf, isAlarm, isTask, monthCells, monthOf, monthTitle, notifyOf, placeDay,
  ProjectInfo, projectPercent, ScheduleItem, TaskInfo, UnscheduledTask, weekdayLabel, weekdayLetters, WEEKDAYS, workedLine,
} from "../lib/schedule";

// The left column (month, 进行中, 项目) and the week's hour grid of 日程安排.

/** A month to pick a day from: Monday first, a dot per item, today and the chosen day marked. */
export function MonthCal({ month, sel, today, counts, week, onMonth, onPick }: {
  month: string; sel: string; today: string; counts: Map<string, number>; week: boolean;
  onMonth: (m: string) => void; onPick: (day: string) => void;
}) {
  const { t } = useI18n();
  const cells = monthCells(month);
  // Six rows unless the month fits in five.
  const rows = cells.slice(35).every((d) => monthOf(d) !== month) ? 5 : 6;
  const atToday = sel === today && monthOf(today) === month;
  return (
    <section className="sch-mcal" aria-label={monthTitle(month)}>
      <header>
        <b>{monthTitle(month)}</b>
        <button className={`sch-today${atToday ? " at" : ""}`} onClick={() => onPick(today)} disabled={atToday} aria-hidden={atToday}>{t("Today")}</button>
        <button className="sch-nav" title={t("Previous month")} aria-label={t("Previous month")} onClick={() => onMonth(addMonths(month, -1))}><ChevronLeftIcon size={16} /></button>
        <button className="sch-nav" title={t("Next month")} aria-label={t("Next month")} onClick={() => onMonth(addMonths(month, 1))}><ChevronRightIcon size={16} /></button>
      </header>
      <div className="sch-mgrid" role="grid">
        {weekdayLetters().map((w, i) => <span key={i} className={`wh${i > 4 ? " we" : ""}`}>{w}</span>)}
        {Array.from({ length: rows }, (_, r) => {
          const days = cells.slice(r * 7, r * 7 + 7);
          const inWeek = week && days.includes(sel);
          return (
            <div key={r} className={`wrow${inWeek ? " on" : ""}`} role="row">
              {days.map((d) => {
                const n = counts.get(d) ?? 0;
                const cls = [monthOf(d) !== month && "out", d === today && "today", d === sel && "sel", d < today && "past"].filter(Boolean).join(" ");
                return (
                  <button key={d} role="gridcell" aria-selected={d === sel} className={cls} onClick={() => onPick(d)}
                    aria-label={new Date(d + "T12:00").toLocaleDateString(undefined, { dateStyle: "full" })}>
                    <b>{Number(d.slice(8))}</b>
                    <i aria-hidden>{Array.from({ length: Math.min(3, n) }, (_, k) => <em key={k} />)}</i>
                  </button>
                );
              })}
            </div>
          );
        })}
      </div>
    </section>
  );
}

function Bar({ value, color }: { value: number; color: string }) {
  return <span className="sch-pbar" aria-hidden><i style={{ width: `${Math.max(0, Math.min(100, value))}%`, background: color }} /></span>;
}

/** 进行中: open to-dos with progress or work logged. */
export function InProgress({ tasks, today, colorOf, onPick }: {
  tasks: TaskInfo[]; today: string; colorOf: (projectId?: string) => string; onPick: (t: TaskInfo) => void;
}) {
  const { t } = useI18n();
  if (!tasks.length) return null;
  return (
    <section className="sch-side-sec">
      <h3>{t("In progress")}</h3>
      <ol className="sch-card">
        {tasks.map((k) => {
          const c = colorOf(k.projectId);
          const line = workedLine(k, today);
          return (
            <li key={k.id}>
              <button className="sch-prow" onClick={() => onPick(k)}>
                <span className="top"><i className="dot" style={{ background: c }} /><b>{k.title || t("Untitled")}</b><span className="pc">{k.progress}%</span></span>
                <Bar value={k.progress} color={c} />
                {line && <small>{line}</small>}
              </button>
            </li>
          );
        })}
      </ol>
    </section>
  );
}

/** 项目: each with its progress, days left and next step. */
export function ProjectList({ projects, colorOf, today }: { projects: ProjectInfo[]; colorOf: (id?: string) => string; today: string }) {
  const { t, language } = useI18n();
  if (!projects.length) return null;
  return (
    <section className="sch-side-sec">
      <h3>{t("Projects")}</h3>
      <ol className="sch-card">
        {projects.map((p) => {
          const c = colorOf(p.id);
          const pr = p.progress ?? {};
          const meta = [
            pr.tasksTotal ? t("{done}/{total} to-dos", { done: pr.tasksDone ?? 0, total: pr.tasksTotal }) : "",
            daysLeftLabel(pr.daysLeft),
          ].filter(Boolean).join(" · ");
          const nextDay = pr.nextTime ? dayOf({ startTime: pr.nextTime, allDay: false }) : "";
          const nextWhen = nextDay ? (nextDay === today ? t("Today") : new Date(nextDay + "T12:00").toLocaleDateString(language, { month: "short", day: "numeric" })) : "";
          return (
            <li key={p.id} className="sch-prow proj">
              <span className="top"><i className="dot" style={{ background: c }} /><b>{p.title || t("Untitled")}</b>{(pr.tasksOverdue ?? 0) > 0 && <span className="late">{t("{n} overdue", { n: pr.tasksOverdue! })}</span>}</span>
              <Bar value={projectPercent(p)} color={c} />
              {meta && <small>{meta}</small>}
              {pr.nextTitle && <span className="next"><ChevronRightIcon size={13} /><span>{pr.nextTitle}{nextWhen && <> · {nextWhen}</>}</span></span>}
            </li>
          );
        })}
      </ol>
    </section>
  );
}

const HOUR = 46;

/** The week as an hour grid: Monday to Sunday, each item a block coloured by
 *  how it reaches you, now as a line, all-day items and to-dos with no time
 *  in a strip on top. Opens at 07:00. */
export function WeekGrid({ start, items, unscheduled, today, sel, now, pickedKey, tasks, onSelect, onPick, onPickTask, onNew, onDone }: {
  start: string; items: ScheduleItem[]; unscheduled: UnscheduledTask[]; today: string; sel: string; now: Date; pickedKey: string;
  tasks: Map<string, TaskInfo>;
  onSelect: (day: string) => void; onPick: (it: ScheduleItem) => void; onPickTask: (t: UnscheduledTask) => void;
  onNew: (day: string, time: string) => void; onDone: (id: string, title: string) => void;
}) {
  const { t } = useI18n();
  const days = useMemo(() => Array.from({ length: 7 }, (_, i) => addDays(start, i)), [start]);
  const scroller = useRef<HTMLDivElement>(null);
  const byDay = useMemo(() => {
    const m = new Map<string, ScheduleItem[]>(days.map((d) => [d, []]));
    for (const it of items) {
      let d = dayOf(it);
      if (d && d < days[0]) d = days[0];
      m.get(d)?.push(it);
    }
    return m;
  }, [items, days]);
  const allDay = days.map((d) => (byDay.get(d) ?? []).filter((it) => it.allDay));
  const hasStrip = allDay.some((l) => l.length) || unscheduled.length > 0;

  // Opens at 07:00 each time the week changes.
  useEffect(() => {
    const el = scroller.current;
    if (el) el.scrollTop = 7 * HOUR - 6;
  }, [start]);

  const nowMin = now.getHours() * 60 + now.getMinutes();
  const todayCol = days.indexOf(today);

  return (
    <div className="sch-week" ref={scroller}>
      <div className="wk-sticky">
        <div className="wk-head">
          <span />
          {days.map((d, i) => (
            <button key={d} className={[d === today && "today", d === sel && "sel", i > 4 && "we"].filter(Boolean).join(" ")} onClick={() => onSelect(d)}>
              <small>{weekdayLabel(WEEKDAYS[i])}</small><b>{Number(d.slice(8))}</b>
            </button>
          ))}
        </div>
        {hasStrip && (
          <div className="wk-strip">
            <span className="lab">{t("All day")}</span>
            {allDay.map((list, i) => (
              <div key={days[i]} className="wk-adcol">
                {list.map((it) => (
                  <button key={`${it.id}@${it.startTime}`} className={`wk-ad m-${notifyOf(it)}${pickedKey === `${it.id}@${it.startTime}` ? " on" : ""}`} onClick={() => onPick(it)} title={it.title}>
                    {it.title || t("Untitled")}
                  </button>
                ))}
              </div>
            ))}
            {unscheduled.length > 0 && (
              <>
                <span className="lab todo">{t("To-dos")}</span>
                <div className="wk-todos">
                  {unscheduled.map((k) => {
                    const info = tasks.get(k.id);
                    return (
                      <span key={k.id} className={`wk-todo${pickedKey === "task:" + k.id ? " on" : ""}`}>
                        <button className="ck" title={t("Mark done")} aria-label={t("Mark done")} onClick={() => onDone(k.id, k.title)}><CheckIcon size={10} strokeWidth={3} /></button>
                        <button className="tt" onClick={() => onPickTask(k)}>{k.title || t("Untitled")}{info && info.progress > 0 && <em>{info.progress}%</em>}</button>
                      </span>
                    );
                  })}
                </div>
              </>
            )}
          </div>
        )}
      </div>
      <div className="wk-grid" style={{ height: 24 * HOUR }}>
        {Array.from({ length: 24 }, (_, h) => (
          <div key={h} className="wk-hl" style={{ top: h * HOUR }}>{h > 0 && <span>{String(h).padStart(2, "0")}:00</span>}</div>
        ))}
        <div className="wk-cols">
          {days.map((d, i) => (
            <div key={d} className={`wk-col${d === today ? " today" : ""}${i > 4 ? " we" : ""}`}
              onClick={(e) => {
                if (e.target !== e.currentTarget) return;
                const y = e.nativeEvent.offsetY;
                const h = Math.max(0, Math.min(23, Math.floor(y / HOUR)));
                onNew(d, `${String(h).padStart(2, "0")}:${y % HOUR >= HOUR / 2 ? "30" : "00"}`);
              }}>
              {placeDay(byDay.get(d) ?? [], d).map((p) => {
                const it = p.it;
                const key = `${it.id}@${it.startTime}`;
                const task = isTask(it);
                const info = task ? tasks.get(it.taskId || it.id) : undefined;
                const h = ((p.end - p.top) / 60) * HOUR;
                const past = d < today || (d === today && (it.endTime && !task && !isAlarm(it) ? p.end : p.top) < nowMin);
                const short = h < 34;
                // As many title lines as the block holds under its time.
                const lines = Math.max(1, Math.floor((h - 8 - 14) / 15.5));
                const cls = ["wk-ev", `m-${notifyOf(it)}`, task && "todo", isAlarm(it) && "alarm", past && "past", it.completed && "done", it.enabled === false && "paused", pickedKey === key && "on", short && "short", short && p.lanes > 1 && "narrow"].filter(Boolean).join(" ");
                return (
                  <button key={key} className={cls} onClick={() => onPick(it)} title={`${clock(it.startTime)} ${it.title}`}
                    style={{ top: (p.top / 60) * HOUR + 1, height: Math.max(20, h - 2), left: `calc(${(p.lane / p.lanes) * 100}% + 2px)`, width: `calc(${100 / p.lanes}% - 4px)` }}>
                    <b style={short ? undefined : { WebkitLineClamp: lines }}>{it.title || t("Untitled")}</b>
                    <span>{clock(it.startTime)}{info && info.progress > 0 ? ` · ${info.progress}%` : ""}{it.location && h >= 52 ? ` · ${it.location}` : ""}</span>
                  </button>
                );
              })}
            </div>
          ))}
        </div>
        {todayCol >= 0 && (
          <div className="wk-now" style={{ top: (nowMin / 60) * HOUR }} aria-hidden>
            <span>{clock(now.toISOString())}</span>
            <i style={{ left: `calc(${todayCol} * 100% / 7)`, width: "calc(100% / 7)" }} />
          </div>
        )}
      </div>
    </div>
  );
}
