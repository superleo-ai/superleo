import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  Bike, Brain, Droplet, Dumbbell, Mountain, Waves, Footprints, GlassWater, HeartPulse, Info, Moon, Pill as PillIcon, Scale, Sofa, Sparkles,
  Stethoscope, Thermometer, TrendingDown, TrendingUp, TriangleAlert, Utensils, Wind,
} from "lucide-react";
import { EventsOn } from "../../wailsjs/runtime";
import { WellbeingDays, WellbeingStatus, WellbeingToday } from "../../wailsjs/go/app/App";
import { useI18n } from "../lib/i18n";
import "./wellbeing.css";
import { SkeletonCards } from "../components/Skeleton";
import {
  Big, Chart, Day, Diverge, Figure, FIGURE_POINTS, Gauge, Pill, Point, Ring, Spark, Stack, Tone,
  base, clamp, dayOf, fmt, get, hm, mean, median, seriesOf, signed, toDate, useWidth,
} from "./wellbeingParts";

/**
 * Wellbeing: the person's health, one summary per day, as the iPhone reads it
 * from Apple Health, kept by the vita server and read through SuperLeo. Laid
 * out after Vita's wide Home: readiness and its reasons, sleep, heart and
 * activity on the left; the body figure, the day, the plan, what Vita noticed
 * and the body's other readings on the right. A section with nothing to show
 * is left out.
 */

interface Today { day?: Day; is_today: boolean; last_report?: string; note?: string }
interface Status { connected: boolean; days?: number; first?: string; last?: string; last_report?: string }
type Sleep = { asleep_h?: number; deep_h?: number; rem_h?: number; bedtime?: string; wake?: string };
type Workout = { title?: string; start?: string; minutes?: number; distance_km?: number; kcal?: number };
type T = (k: string, v?: Record<string, string | number>) => string;

const SPANS = [7, 30, 90];
const workoutIcon = (title?: string) => {
  const s = (title ?? "").toLowerCase();
  return /swim/.test(s) ? Waves : /cycl|bike|ride/.test(s) ? Bike : /strength|weight|core|functional/.test(s) ? Dumbbell
    : /hike|walk/.test(s) ? Mountain : /yoga|mind|medit|pilates/.test(s) ? Brain : Footprints;
};
const STEP_GOAL = 10_000; // Vita's default goal; the phone does not report its own.

const ago = (t: T, iso?: string) => {
  if (!iso) return "";
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 90) return t("just now");
  if (s < 3600) return t("{count} min ago", { count: Math.round(s / 60) });
  if (s < 86400 * 2) return t("{count} h ago", { count: Math.round(s / 3600) });
  return t("{count} d ago", { count: Math.round(s / 86400) });
};

// Vita's judgements (Scoring.swift, MetricTiles.swift), word for word.
export const recoveryTone = (v?: number): Tone => (v === undefined ? "muted" : v >= 67 ? "good" : v >= 34 ? "fair" : "low");
export const recoveryWord = (v?: number) => (v === undefined ? "No data" : v >= 67 ? "Recovered" : v >= 34 ? "Steady" : "Strained");
const stressTone = (v?: number): Tone => (v === undefined ? "muted" : v < 35 ? "good" : v < 65 ? "fair" : "low");
const stressWord = (v?: number) => (v === undefined ? "No data" : v < 35 ? "Low" : v < 65 ? "Moderate" : "High");
const sleepJudge = (h?: number): [string, Tone] => (h === undefined ? ["No data", "muted"] : h >= 7 ? ["Good", "good"] : h >= 6 ? ["Fair", "fair"] : ["Short", "fair"]);
const hrvJudge = (v?: number, b?: number): [string, Tone] => {
  if (v === undefined || !b) return ["No baseline", "muted"];
  const r = v / b;
  return r >= 1.05 ? ["Optimal", "good"] : r >= 0.9 ? ["On baseline", "good"] : ["Below baseline", "fair"];
};

/** The newest value of a key: today's, else the latest day that has it. */
function latest(d: Day | undefined, days: Day[], key: string): { value: number; date: string } | undefined {
  const v = get(d, key);
  if (v !== undefined) return { value: v, date: String(d?.date) };
  for (let i = days.length - 1; i >= 0; i--) {
    const x = get(days[i], key);
    if (x !== undefined) return { value: x, date: String(days[i].date) };
  }
  return undefined;
}

/** Minutes after noon, so a bedtime either side of midnight averages right. */
const clockMin = (iso?: string) => {
  const d = toDate(iso);
  if (!d) return undefined;
  const m = d.getHours() * 60 + d.getMinutes();
  return m < 12 * 60 ? m + 24 * 60 : m;
};
const clockStr = (m?: number) => (m === undefined ? "––" : `${String(Math.floor(m / 60) % 24).padStart(2, "0")}:${String(Math.round(m % 60)).padStart(2, "0")}`);

export default function WellbeingView() {
  const { t, language } = useI18n();
  const [today, setToday] = useState<Today | null>(null);
  const [status, setStatus] = useState<Status | null>(null);
  const [days, setDays] = useState<Day[]>([]);
  const [span, setSpan] = useState(30);
  const [err, setErr] = useState("");

  const load = useCallback(async () => {
    try {
      const st = (await WellbeingStatus()) as Status;
      setStatus(st);
      if (!st.connected || !st.days) { setErr(""); return; }
      const [td, ds] = await Promise.all([WellbeingToday(), WellbeingDays(span)]);
      setToday(td as Today); setDays(((ds as { days?: Day[] })?.days) ?? []); setErr("");
    } catch (e) {
      setErr(String(e));
    }
  }, [span]);

  useEffect(() => { load(); }, [load]);
  useEffect(() => EventsOn("wellbeing:changed", () => load()), [load]);

  const loc = language === "en" ? undefined : language;
  const short = useCallback((ymd: string) => dayOf(ymd).toLocaleDateString(loc, { month: "numeric", day: "numeric" }), [loc]);

  if (!status && !err) return (
    <div className="wb-view" aria-busy="true">
      <header className="wb-head"><h1>{t("Wellbeing")}</h1></header>
      <SkeletonCards n={3} h={110} />
    </div>
  );
  if (status && (!status.connected || !status.days)) {
    return (
      <div className="wb-view">
        <header className="wb-head"><h1>{t("Wellbeing")}</h1></header>
        <div className="wb-empty">
          <div className="wb-empty-title">{status.connected ? t("No health data yet") : t("Vita is not connected")}</div>
          <ol className="wb-steps">
            {!status.connected && <li>{t("Settings › MCP › add vita")}</li>}
            <li>{t("iPhone: SuperLeo › Wellbeing › Turn on")}</li>
            <li>{t("Or Vita: Profile › Share with SuperAI")}</li>
          </ol>
        </div>
      </div>
    );
  }

  const d = today?.day;
  const p = { d, days, t, short, loc, isToday: !!today?.is_today };
  const dateLabel = d?.date ? dayOf(String(d.date)).toLocaleDateString(loc, { weekday: "short", month: "short", day: "numeric" }) : "";

  return (
    <div className="wb-view">
      <header className="wb-head">
        <h1>{t("Wellbeing")}</h1>
        <div className="wb-meta">
          {dateLabel && <span className="wb-chip">{today?.is_today ? `${t("Today")} · ${dateLabel}` : dateLabel}</span>}
          {today?.last_report && <span className="wb-chip ghost">{t("Reported")} {ago(t, today.last_report)}</span>}
        </div>
        <div className="wb-seg" role="tablist" aria-label={t("Range")}>
          {SPANS.map((n) => (
            <button key={n} role="tab" aria-selected={span === n} className={span === n ? "on" : ""} onClick={() => setSpan(n)}>{n}{t("d")}</button>
          ))}
        </div>
      </header>
      {err && <div className="wb-error">{err}</div>}

      <div className="wb-cols">
        <div className="wb-col main">
          <Readiness {...p} />
          <SleepCard {...p} />
          <HeartCard {...p} />
          <ActivityCard {...p} />
          <NutritionCard {...p} />
        </div>
        <div className="wb-col side">
          <BodyMap {...p} />
          <PlanCard {...p} />
          <DayTimeline {...p} />
          <InsightsCard {...p} />
          <BodyCard {...p} />
        </div>
      </div>
      <footer className="wb-foot">{t("Not medical advice")}</footer>
    </div>
  );
}

type P = { d?: Day; days: Day[]; t: T; short: (ymd: string) => string; loc?: string; isToday: boolean };

function Card({ title, aside, className, children }: { title: string; aside?: React.ReactNode; className?: string; children: React.ReactNode }) {
  return (
    <section className={`wb-card ${className ?? ""}`}>
      <header className="wb-card-head"><h2>{title}</h2>{aside && <div className="wb-card-aside">{aside}</div>}</header>
      {children}
    </section>
  );
}

function Stat({ label, value, unit, sub }: { label: string; value: string; unit?: string; sub?: React.ReactNode }) {
  return (
    <div className="wb-stat">
      <span className="wb-k">{label}</span>
      <Big value={value} unit={unit} size="s" />
      {sub && <span className="wb-s">{sub}</span>}
    </div>
  );
}

// ── Readiness: the recovery score, why, and stress ──────────────────────

function Readiness({ d, days, t, short }: P) {
  const rec = get(d, "recovery"), stress = get(d, "stress");
  const hrv = get(d, "hrv_ms"), hrvB = get(d, "hrv_baseline_ms");
  const rhr = get(d, "resting_hr"), rhrB = get(d, "resting_hr_baseline");
  const sleepH = get(d, "sleep_h") ?? get(d, "sleep.asleep_h");
  const heartAge = get(d, "heart_age"), vo2 = get(d, "vo2max");
  const trend = useMemo(() => seriesOf(days, "recovery"), [days]);
  if (rec === undefined && stress === undefined && hrv === undefined) return null;

  // Recovery as Vita makes it: HRV against its baseline carries it, resting
  // heart rate and the night's sleep move it.
  const drivers: { key: string; label: string; value: string; delta: string; pts: number; tone: Tone }[] = [];
  if (hrv !== undefined && hrvB) {
    const pct = ((hrv - hrvB) / hrvB) * 100;
    drivers.push({ key: "hrv", label: "HRV", value: `${fmt(hrv)} ms`, delta: t("{d}% vs {b} ms", { d: signed(pct), b: fmt(hrvB) }), pts: 66 * (hrv / hrvB) - 66, tone: pct >= -5 ? "good" : "fair" });
  }
  if (rhr !== undefined && rhrB !== undefined) {
    const dd = rhr - rhrB;
    drivers.push({ key: "rhr", label: t("Resting HR"), value: `${fmt(rhr)} bpm`, delta: t("{d} vs {b} bpm", { d: signed(dd), b: fmt(rhrB) }), pts: -clamp(dd, -10, 10) * 1.5, tone: dd <= 2 ? "good" : "fair" });
  }
  if (sleepH !== undefined) {
    const dd = sleepH - 7;
    drivers.push({ key: "sleep", label: t("Sleep"), value: `${fmt(sleepH, 1)} h`, delta: t("{d} h vs 7 h", { d: signed(dd, 1) }), pts: clamp(dd * 4, -8, 8), tone: dd >= 0 ? "good" : dd >= -1 ? "fair" : "low" });
  }

  return (
    <Card title={t("Readiness")} className="wb-ready" aside={rec !== undefined && <Pill tone={recoveryTone(rec)} dot>{t(recoveryWord(rec))}</Pill>}>
      <div className="wb-ready-grid">
        <div className="wb-ready-ring">
          <Ring value={rec} tone={recoveryTone(rec)} />
          <span className="wb-k">{t("Recovery")}</span>
        </div>
        {drivers.length > 0 && (
          <ul className="wb-drivers">
            {drivers.map((x) => (
              <li key={x.key}>
                <span className="wb-k">{x.label}</span>
                <b className="wb-mono">{x.value}</b>
                <span className={`wb-delta t-${x.tone}`}>{x.delta}</span>
                <Diverge value={x.pts} />
              </li>
            ))}
          </ul>
        )}
        <div className="wb-ready-side">
          {stress !== undefined && (
            <div className="wb-stress">
              <Gauge value={stress} tone={stressTone(stress)} />
              <div><Big value={fmt(stress)} size="s" /><span className="wb-k">{t("Stress")}</span></div>
              <Pill tone={stressTone(stress)}>{t(stressWord(stress))}</Pill>
            </div>
          )}
          <div className="wb-ready-mini">
            {heartAge !== undefined && <Stat label={t("Heart age")} value={fmt(heartAge)} unit={t("yrs")} />}
            {vo2 !== undefined && <Stat label="VO₂max" value={fmt(vo2, 1)} />}
          </div>
        </div>
      </div>
      {trend.length >= 2 && (
        <div className="wb-ready-trend">
          <span className="wb-k">{t("Recovery")} · {t("Average")} <b className="wb-mono">{fmt(mean(trend.map((x) => x.value)))}</b></span>
          <Chart height={86} deps={[trend, short]} build={(c) => {
            const o = base(c, (v) => fmt(v), trend.map((x) => x.date), short);
            return { ...o, grid: { left: 28, right: 8, top: 8, bottom: 20 },
              yAxis: { ...(o.yAxis as object), min: 0, max: 100, splitNumber: 2 },
              series: [{ type: "line", data: trend.map((x) => x.value), smooth: 0.3, symbol: "circle", symbolSize: 6, showSymbol: trend.length <= 14,
                lineStyle: { width: 2, color: c.green }, itemStyle: { color: c.green, borderColor: c.pop, borderWidth: 2 },
                areaStyle: { color: c.green, opacity: 0.08 } }] } as any;
          }} />
        </div>
      )}
    </Card>
  );
}

// ── Sleep ───────────────────────────────────────────────────────────────

function SleepCard({ d, days, t, short }: P) {
  const night = d?.sleep as Sleep | undefined;
  const asleep = get(d, "sleep_h") ?? night?.asleep_h;
  const nights = useMemo(() => seriesOf(days, "sleep_h"), [days]);
  if (asleep === undefined && nights.length === 0) return null;
  const [word, tone] = sleepJudge(asleep);

  const bed = toDate(night?.bedtime), wake = toDate(night?.wake);
  const inBed = bed && wake ? (wake.getTime() - bed.getTime()) / 3600e3 : undefined;
  const deep = night?.deep_h, rem = night?.rem_h;
  // The phone sends deep and REM; what else was asleep is core, the rest of the time in bed awake.
  const core = asleep !== undefined && deep !== undefined && rem !== undefined ? Math.max(0, asleep - deep - rem) : undefined;
  const awake = inBed !== undefined && asleep !== undefined ? Math.max(0, inBed - asleep) : undefined;
  const stages = core !== undefined ? [
    { key: "deep", label: t("Deep"), h: deep!, cls: "s-deep" },
    { key: "core", label: t("Core"), h: core, cls: "s-core" },
    { key: "rem", label: "REM", h: rem!, cls: "s-rem" },
    ...(awake !== undefined ? [{ key: "awake", label: t("Time awake"), h: awake, cls: "s-awake" }] : []),
  ] : [];
  const whole = stages.reduce((s, x) => s + x.h, 0) || 1;

  const vals = nights.map((x) => x.value);
  const avg = mean(vals);
  const wk = vals.length >= 14 ? [mean(vals.slice(-7))!, mean(vals.slice(-14, -7))!] : undefined;
  const beds = days.map((x) => clockMin((x.sleep as Sleep | undefined)?.bedtime)).filter((x): x is number => x !== undefined);
  const wakes = days.map((x) => clockMin((x.sleep as Sleep | undefined)?.wake)).filter((x): x is number => x !== undefined);

  return (
    <Card title={t("Sleep")} aside={asleep !== undefined && <Pill tone={tone} dot>{t(word)}</Pill>}>
      <div className="wb-row4">
        <Stat label={t("Asleep")} value={fmt(asleep, 1)} unit="h" />
        {bed && <Stat label={t("Bedtime")} value={hm(bed)} />}
        {wake && <Stat label={t("Wake")} value={hm(wake)} />}
        {inBed !== undefined && <Stat label={t("In bed")} value={fmt(inBed, 1)} unit="h" sub={asleep !== undefined && inBed > 0 ? `${fmt((asleep / inBed) * 100)}% ${t("efficiency")}` : undefined} />}
      </div>
      {stages.length > 0 && (
        <div className="wb-stages">
          <Stack height={16} parts={stages.map((s) => ({ key: s.key, value: s.h, cls: s.cls, title: `${s.label} ${fmt(s.h, 1)} h` }))} />
          <ul className="wb-legend">
            {stages.map((s) => (
              <li key={s.key}><i className={s.cls} /><span>{s.label}</span><b className="wb-mono">{fmt(s.h, 1)} h</b><em>{fmt((s.h / whole) * 100)}%</em></li>
            ))}
          </ul>
        </div>
      )}
      {nights.length >= 2 && (
        <div className="wb-subchart">
          <div className="wb-subhead">
            <span className="wb-k">{t("Nights")}</span>
            <span className="wb-sums">
              <span>{t("Average")} <b>{fmt(avg, 1)} h</b></span>
              {wk && <span>{t("This week")} <b className={wk[0] >= wk[1] ? "up" : "down"}>{signed(((wk[0] - wk[1]) / wk[1]) * 100)}%</b></span>}
              {beds.length >= 2 && <span>{t("Usual bedtime")} <b>{clockStr(mean(beds))}</b></span>}
              {wakes.length >= 2 && <span>{t("Usual wake")} <b>{clockStr(mean(wakes))}</b></span>}
            </span>
          </div>
          <Chart height={130} deps={[nights, short]} build={(c) => {
            const o = base(c, (v) => `${fmt(v, 1)} h`, nights.map((x) => x.date), short);
            return { ...o, yAxis: { ...(o.yAxis as object), scale: false, min: 0, splitNumber: 2 },
              series: [{ type: "bar", data: nights.map((x) => ({ value: x.value, itemStyle: { color: x.value >= 7 ? c.blue : c.silhouette } })),
                barMaxWidth: 14, itemStyle: { borderRadius: [4, 4, 0, 0] },
                markLine: { silent: true, symbol: "none", label: { show: true, position: "insideEndTop", formatter: "7 h", color: c.ink3, fontSize: 10 },
                  lineStyle: { color: c.ink3, type: "dashed", width: 1 }, data: [{ yAxis: 7 }] } }] } as any;
          }} />
        </div>
      )}
    </Card>
  );
}

// ── Heart ───────────────────────────────────────────────────────────────

function HeartHalf({ label, unit, value, baseline, points, judge, color, lowerIsBetter, short, t }: {
  label: string; unit: string; value?: number; baseline?: number; points: Point[]; judge: [string, Tone]; color: "red" | "blue";
  lowerIsBetter?: boolean; short: (s: string) => string; t: T;
}) {
  const delta = value !== undefined && baseline !== undefined ? value - baseline : undefined;
  return (
    <div className="wb-half">
      <div className="wb-half-top">
        <div>
          <span className="wb-k">{label}</span>
          <Big value={fmt(value)} unit={unit} />
        </div>
        <div className="wb-half-right">
          <Pill tone={judge[1]}>{t(judge[0])}</Pill>
          {delta !== undefined && (
            <span className="wb-s">{lowerIsBetter ? (delta <= 0 ? <TrendingDown size={12} /> : <TrendingUp size={12} />) : delta >= 0 ? <TrendingUp size={12} /> : <TrendingDown size={12} />}
              {t("{d} vs baseline {b}", { d: signed(delta), b: fmt(baseline) })}</span>
          )}
        </div>
      </div>
      {points.length >= 2 && (
        <Chart height={120} deps={[points, baseline, short]} build={(c) => {
          const o = base(c, (v) => `${fmt(v)} ${unit}`, points.map((x) => x.date), short);
          const col = c[color];
          return { ...o, series: [{ type: "line", data: points.map((x) => x.value), smooth: 0.3, symbol: "circle", symbolSize: 6, showSymbol: points.length <= 14,
            lineStyle: { width: 2, color: col }, itemStyle: { color: col, borderColor: c.pop, borderWidth: 2 },
            markLine: baseline === undefined ? undefined : { silent: true, symbol: "none", lineStyle: { color: c.ink3, type: "dashed", width: 1 },
              label: { show: true, position: "insideEndTop", formatter: t("baseline"), color: c.ink3, fontSize: 10 }, data: [{ yAxis: baseline }] } }] } as any;
        }} />
      )}
    </div>
  );
}

function HeartCard({ d, days, t, short }: P) {
  const rhrPts = useMemo(() => seriesOf(days, "resting_hr"), [days]);
  const hrvPts = useMemo(() => seriesOf(days, "hrv_ms"), [days]);
  const rhr = get(d, "resting_hr"), hrv = get(d, "hrv_ms");
  if (rhr === undefined && hrv === undefined && rhrPts.length + hrvPts.length === 0) return null;
  const rhrB = get(d, "resting_hr_baseline") ?? median(rhrPts.map((x) => x.value));
  const hrvB = get(d, "hrv_baseline_ms") ?? median(hrvPts.map((x) => x.value));
  const rhrJudge: [string, Tone] = rhr === undefined || rhrB === undefined ? ["No baseline", "muted"] : rhr - rhrB <= 2 ? ["On baseline", "good"] : ["Above baseline", "fair"];
  return (
    <Card title={t("Heart")}>
      <div className="wb-halves">
        <HeartHalf label={t("Resting HR")} unit="bpm" value={rhr} baseline={rhrB} points={rhrPts} judge={rhrJudge} color="red" lowerIsBetter short={short} t={t} />
        <HeartHalf label="HRV" unit="ms" value={hrv} baseline={hrvB} points={hrvPts} judge={hrvJudge(hrv, hrvB)} color="blue" short={short} t={t} />
      </div>
    </Card>
  );
}

// ── Activity ────────────────────────────────────────────────────────────

function ActivityCard({ d, days, t, loc }: P) {
  const steps = get(d, "steps"), kcal = get(d, "active_kcal"), ex = get(d, "exercise_min");
  // The seven calendar days ending on the day shown, gaps and all.
  const byDate = new Map(days.map((x) => [String(x.date), x]));
  const end = dayOf(String(d?.date ?? days[days.length - 1]?.date ?? ""));
  const week = isNaN(end.getTime()) ? [] : Array.from({ length: 7 }, (_, i) => {
    const t0 = new Date(end); t0.setDate(end.getDate() - 6 + i);
    const date = `${t0.getFullYear()}-${String(t0.getMonth() + 1).padStart(2, "0")}-${String(t0.getDate()).padStart(2, "0")}`;
    return { date, steps: get(byDate.get(date), "steps") };
  });
  const workouts = useMemo(() => days.flatMap((x) => (Array.isArray(x.workouts) ? (x.workouts as Workout[]) : []))
    .filter((w) => w.title).sort((a, b) => String(b.start).localeCompare(String(a.start))).slice(0, 5), [days]);
  if (steps === undefined && week.every((x) => x.steps === undefined) && workouts.length === 0) return null;
  const top = Math.max(STEP_GOAL, ...week.map((x) => x.steps ?? 0));
  const weekAvg = mean(week.flatMap((x) => (x.steps === undefined ? [] : [x.steps])));
  const frac = steps !== undefined ? steps / STEP_GOAL : 0;

  return (
    <Card title={t("Activity")} aside={steps !== undefined && <Pill tone={frac >= 1 ? "good" : "muted"}>{t("{n}% of goal", { n: fmt(frac * 100) })}</Pill>}>
      <div className="wb-act">
        <div className="wb-act-left">
          <span className="wb-k">{t("Steps")}</span>
          <Big value={fmt(steps)} size="l" />
          <div className="wb-progress"><i style={{ width: `${clamp(frac, 0, 1) * 100}%` }} /></div>
          <div className="wb-row2">
            {kcal !== undefined && <Stat label={t("Active energy")} value={fmt(kcal)} unit="kcal" />}
            {ex !== undefined && <Stat label={t("Exercise")} value={fmt(ex)} unit="min" />}
          </div>
        </div>
        {week.some((x) => x.steps !== undefined) && (
          <div className="wb-week">
            <div className="wb-subhead"><span className="wb-k">{t("Last 7 days")}</span><span className="wb-sums"><span>{t("Goal")} <b>{fmt(STEP_GOAL)}</b></span>{weekAvg !== undefined && <span>{t("Average")} <b>{fmt(weekAvg)}</b></span>}</span></div>
            <div className="wb-bars">
              <span className="wb-goal-zone"><span className="wb-goal" style={{ bottom: `${(STEP_GOAL / top) * 100}%` }} /></span>
              {week.map((x, i) => (
                <div key={x.date} className="wb-bar" title={`${x.date} · ${fmt(x.steps)}`}>
                  <span className="wb-bar-v">{x.steps === undefined ? "" : x.steps >= 1000 ? `${fmt(x.steps / 1000, 1)}k` : fmt(x.steps)}</span>
                  <div className="wb-bar-track"><i className={i === week.length - 1 ? "last" : (x.steps ?? 0) >= STEP_GOAL ? "met" : ""} style={{ height: `${((x.steps ?? 0) / top) * 100}%` }} /></div>
                  <span className="wb-bar-d">{dayOf(x.date).toLocaleDateString(loc, { weekday: "narrow" })}</span>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
      {workouts.length > 0 && (
        <div className="wb-workouts">
          <span className="wb-k">{t("Workouts")}</span>
          <ul>
            {workouts.map((w, i) => (
              <li key={i}>
                <span className="wb-ico t-info">{React.createElement(workoutIcon(w.title), { size: 14 })}</span>
                <b>{w.title}</b>
                <span className="wb-s">{toDate(w.start)?.toLocaleDateString(loc, { month: "short", day: "numeric" })} {hm(w.start)}</span>
                <span className="wb-w-nums wb-mono">
                  {w.minutes !== undefined && <span>{fmt(w.minutes)} min</span>}
                  {w.distance_km ? <span>{fmt(w.distance_km, 1)} km</span> : null}
                  {w.kcal !== undefined && <span>{fmt(w.kcal)} kcal</span>}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </Card>
  );
}

// ── Nutrition ───────────────────────────────────────────────────────────

function NutritionCard({ d, t }: P) {
  const n = d?.nutrition as Record<string, number> | undefined;
  if (!n || typeof n !== "object") return null;
  const g = (k: string) => (typeof n[k] === "number" ? n[k] : undefined);
  const macros = [
    { key: "protein", label: t("Protein"), g: g("protein_g"), cls: "m-p" },
    { key: "carbs", label: t("Carbs"), g: g("carbs_g"), cls: "m-c" },
    { key: "fat", label: t("Fat"), g: g("fat_g"), cls: "m-f" },
  ].filter((m) => m.g !== undefined && m.g > 0);
  return (
    <Card title={t("Nutrition")}>
      <div className="wb-row4">
        {g("kcal") !== undefined && <Stat label={t("Energy")} value={fmt(g("kcal"))} unit="kcal" />}
        {g("water_ml") !== undefined && <Stat label={t("Water")} value={fmt(g("water_ml"))} unit="mL" />}
        {g("caffeine_mg") !== undefined && <Stat label={t("Caffeine")} value={fmt(g("caffeine_mg"))} unit="mg" />}
      </div>
      {macros.length > 0 && (
        <div className="wb-stages">
          <Stack height={10} parts={macros.map((m) => ({ key: m.key, value: m.g!, cls: m.cls, title: `${m.label} ${fmt(m.g)} g` }))} />
          <ul className="wb-legend">
            {macros.map((m) => <li key={m.key}><i className={m.cls} /><span>{m.label}</span><b className="wb-mono">{fmt(m.g)} g</b></li>)}
          </ul>
        </div>
      )}
    </Card>
  );
}

// ── The body figure with its three readings ─────────────────────────────

function BodyMap(p: P) {
  const { d } = p;
  if (get(d, "respiratory_rate") === undefined && get(d, "heart_rate_latest.bpm") === undefined && get(d, "resting_hr") === undefined) return null;
  return <BodyMapCard {...p} />;
}

function BodyMapCard({ d, t, isToday }: P) {
  const [ref, w] = useWidth<HTMLElement>();
  const breath = get(d, "respiratory_rate");
  const hr = get(d, "heart_rate_latest.bpm"), hrAt = toDate(d?.heart_rate_latest?.at);
  const rhr = get(d, "resting_hr"), rhrB = get(d, "resting_hr_baseline");
  const temp = get(d, "wrist_temp_delta_c");

  const fresh = hrAt && isToday && Date.now() - hrAt.getTime() < 10 * 60e3;
  const breathTone: [string, Tone] = breath === undefined ? ["No reading", "muted"] : breath > 20 || breath < 10 ? ["Outside range", "fair"] : ["Normal", "good"];
  const hrTone: [string, Tone] = hr === undefined ? ["No reading", "muted"] : !isToday || (hrAt && Date.now() - hrAt.getTime() > 6 * 3600e3) ? ["Last reading", "muted"] : hr > 100 || hr < 45 ? ["Elevated", "fair"] : ["Normal", "good"];
  const third = temp !== undefined
    ? { label: `${t("Wrist temp")} · ${t("vs baseline")}`, value: signed(temp, 1), unit: "°C", st: (Math.abs(temp) >= 0.5 ? ["Above baseline", "fair"] : ["Normal", "good"]) as [string, Tone] }
    : { label: t("Resting HR"), value: fmt(rhr), unit: "bpm", st: (rhr === undefined ? ["No reading", "muted"] : rhrB !== undefined ? [t("{d} vs baseline", { d: signed(rhr - rhrB) }), rhr - rhrB <= 2 ? "good" : "fair"] : ["Normal", "good"]) as [string, Tone] };

  const ox = w - 240 - 4;
  const pts = FIGURE_POINTS;
  const routes: { pts: [number, number][]; cls: string }[] = w > 0 ? [
    { pts: [[150, 50], [ox + 62, 50], [ox + pts.lungs.x, pts.lungs.y]], cls: "c-blue" },
    { pts: [[150, 175], [ox + 40, 175], [ox + pts.heart.x, pts.heart.y]], cls: "c-orange" },
    { pts: [[150, 298], [ox + pts.wrist.x, pts.wrist.y]], cls: "c-green" },
  ] : [];
  const Callout = ({ label, value, unit, st, style }: { label: string; value: string; unit: string; st: [string, Tone]; style: React.CSSProperties }) => (
    <div className="wb-callout" style={style}>
      <span className="wb-k">{label}</span>
      <Big value={value} unit={unit} size="m" />
      <span className={`wb-st t-${st[1]}`}>{t(st[0])}</span>
    </div>
  );
  return (
    <section className="wb-card wb-map" ref={ref}>
      {w > 0 && <div className="wb-fig-at" style={{ left: ox }}><Figure /></div>}
      <svg className="wb-leaders" width={w} height={350} aria-hidden>
        {routes.map((r, i) => <polyline key={i} points={r.pts.map((p) => p.join(",")).join(" ")} />)}
        {routes.map((r, i) => { const [x, y] = r.pts[r.pts.length - 1]; return <g key={`d${i}`} className={r.cls}><circle cx={x} cy={y} r={8} className="ring" /><circle cx={x} cy={y} r={3.5} className="dot" /></g>; })}
      </svg>
      <Callout label={`${t("Breathing")} · ${t("last night")}`} value={fmt(breath, 1)} unit="rpm" st={breathTone} style={{ top: 22 }} />
      <Callout label={t("Heart")} value={fmt(hr)} unit="bpm" st={hrTone} style={{ top: 146 }} />
      <Callout label={third.label} value={third.value} unit={third.unit} st={third.st} style={{ bottom: 18 }} />
      <span className="wb-live">
        <Pill tone={fresh ? "good" : "muted"} dot>{hrAt ? (fresh ? t("LIVE") : hm(hrAt)) : t("NO DATA")}</Pill>
      </span>
    </section>
  );
}

// ── Today's plan (Vita's suggestions) ───────────────────────────────────

const KIND: Record<string, { Icon: typeof Moon; tone: Tone }> = {
  move: { Icon: Footprints, tone: "info" }, hydrate: { Icon: GlassWater, tone: "info" },
  rest: { Icon: Sofa, tone: "good" }, sleep: { Icon: Moon, tone: "good" }, mind: { Icon: Brain, tone: "good" },
  food: { Icon: Utensils, tone: "fair" }, meds: { Icon: PillIcon, tone: "fair" }, care: { Icon: Stethoscope, tone: "fair" },
};

function PlanCard({ d, t }: P) {
  const list: { title: string; reason?: string; time?: string; kind?: string }[] = Array.isArray(d?.suggestions) ? d!.suggestions : [];
  if (!list.length) return null;
  return (
    <Card title={t("Today's plan")}>
      <ul className="wb-items">
        {list.map((s, i) => {
          const k = KIND[s.kind ?? ""] ?? { Icon: Sparkles, tone: "info" as Tone };
          return (
            <li key={i}>
              <span className={`wb-ico t-${k.tone}`}><k.Icon size={16} /></span>
              <div><b>{s.title}</b>{s.reason && <span>{s.reason}</span>}</div>
              {s.time && <em className="wb-time">{s.time}</em>}
            </li>
          );
        })}
      </ul>
    </Card>
  );
}

// ── Insights ────────────────────────────────────────────────────────────

function InsightsCard({ d, t }: P) {
  const list: { title: string; detail?: string; tone?: string }[] = Array.isArray(d?.insights) ? d!.insights : [];
  if (!list.length) return null;
  const look = (tone?: string): { Icon: typeof Moon; tone: Tone } =>
    tone === "good" ? { Icon: TrendingUp, tone: "good" } : tone === "caution" ? { Icon: TriangleAlert, tone: "fair" } : { Icon: Info, tone: "info" };
  return (
    <Card title={t("Insights")}>
      <ul className="wb-items">
        {list.map((i, n) => {
          const l = look(i.tone);
          return (
            <li key={n}>
              <span className={`wb-ico t-${l.tone}`}><l.Icon size={16} /></span>
              <div><b>{i.title}</b>{i.detail && <span>{i.detail}</span>}</div>
            </li>
          );
        })}
      </ul>
    </Card>
  );
}

// ── The day, on a 24-hour track ─────────────────────────────────────────

function DayTimeline({ d, t, isToday }: P) {
  if (!d?.date) return null;
  const start = new Date(`${d.date}T00:00:00`).getTime(), H = 3600e3;
  const pos = (ms: number) => clamp((ms - start) / (24 * H), 0, 1) * 100;
  type Ev = { key: string; title: string; from: number; to?: number; cls: string; right?: string; Icon: typeof Moon };
  const ev: Ev[] = [];
  const night = d.sleep as Sleep | undefined;
  const bed = toDate(night?.bedtime), wake = toDate(night?.wake);
  if (bed && wake) ev.push({ key: "sleep", title: t("Sleep"), from: bed.getTime(), to: wake.getTime(), cls: "c-sleep", right: night?.asleep_h !== undefined ? `${fmt(night.asleep_h, 1)} h` : undefined, Icon: Moon });
  for (const [i, w] of ((Array.isArray(d.workouts) ? d.workouts : []) as Workout[]).entries()) {
    const s = toDate(w.start);
    if (!s) continue;
    ev.push({ key: `w${i}`, title: w.title ?? t("Workout"), from: s.getTime(), to: s.getTime() + (w.minutes ?? 0) * 60e3, cls: "c-work",
      right: [w.distance_km ? `${fmt(w.distance_km, 1)} km` : "", w.kcal !== undefined ? `${fmt(w.kcal)} kcal` : ""].filter(Boolean).join(" · "), Icon: Footprints });
  }
  const hrAt = toDate(d.heart_rate_latest?.at);
  if (hrAt && get(d, "heart_rate_latest.bpm") !== undefined) ev.push({ key: "hr", title: t("Heart rate"), from: hrAt.getTime(), cls: "c-heart", right: `${fmt(get(d, "heart_rate_latest.bpm"))} bpm`, Icon: HeartPulse });
  const readAt = toDate(d.read_at);
  if (readAt) ev.push({ key: "read", title: t("Read from Apple Health"), from: readAt.getTime(), cls: "c-read", Icon: Droplet });
  if (!ev.length) return null;
  ev.sort((a, b) => a.from - b.from);
  const now = Date.now();
  const showNow = isToday && now >= start && now < start + 24 * H;

  return (
    <Card title={isToday ? t("Today's timeline") : t("Timeline")}>
      <div className="wb-track">
        <div className="wb-track-bar">
          {ev.map((e) => {
            const a = pos(e.from), b = e.to !== undefined ? pos(e.to) : a;
            if (e.to !== undefined && e.to <= start) return null;
            return e.to !== undefined && b - a > 0.4
              ? <i key={e.key} className={`seg ${e.cls}`} style={{ left: `${a}%`, width: `${b - a}%` }} title={e.title} />
              : <i key={e.key} className={`pin ${e.cls}`} style={{ left: `${a}%` }} title={e.title} />;
          })}
          {showNow && <span className="wb-now" style={{ left: `${pos(now)}%` }}><em>{hm(new Date(now))}</em></span>}
        </div>
        <div className="wb-ticks">{[0, 6, 12, 18, 24].map((h) => <span key={h}>{String(h).padStart(2, "0")}:00</span>)}</div>
      </div>
      <ul className="wb-evs">
        {ev.map((e) => (
          <li key={e.key}>
            <i className={`dot ${e.cls}`} />
            <div><b>{e.title}</b><span className="wb-s">{hm(new Date(e.from))}{e.to !== undefined && e.to !== e.from ? ` – ${hm(new Date(e.to))}` : ""}</span></div>
            {e.right && <span className="wb-mono wb-s">{e.right}</span>}
          </li>
        ))}
      </ul>
    </Card>
  );
}

// ── Body: everything else the phone measured ────────────────────────────

function BodyCard({ d, days, t, loc }: P) {
  const when = (date: string) => (date === String(d?.date) ? "" : dayOf(date).toLocaleDateString(loc, { month: "short", day: "numeric" }));
  const rows: { key: string; Icon: typeof Moon; label: string; value: string; unit?: string; st?: [string, Tone]; sub?: string; spark?: number[] }[] = [];
  const spark = (k: string) => seriesOf(days, k).map((x) => x.value).slice(-14);

  const spo2 = latest(d, days, "spo2_pct");
  if (spo2) rows.push({ key: "spo2", Icon: Droplet, label: t("Blood oxygen"), value: fmt(spo2.value, spo2.value % 1 ? 1 : 0), unit: "%", st: spo2.value >= 95 ? ["Normal", "good"] : ["Low", "fair"], sub: when(spo2.date), spark: spark("spo2_pct") });
  const resp = get(d, "respiratory_rate");
  if (resp !== undefined) rows.push({ key: "resp", Icon: Wind, label: t("Respiratory rate"), value: fmt(resp, 1), unit: "rpm", st: resp > 20 || resp < 10 ? ["Outside range", "fair"] : ["Normal", "good"], spark: spark("respiratory_rate") });
  const temp = latest(d, days, "wrist_temp_c");
  if (temp) {
    const dt = get(d, "wrist_temp_delta_c");
    rows.push({ key: "temp", Icon: Thermometer, label: t("Wrist temperature"), value: fmt(temp.value, 1), unit: "°C", sub: dt !== undefined ? t("{d} vs baseline", { d: `${signed(dt, 1)} °C` }) : when(temp.date), spark: spark("wrist_temp_c") });
  }
  const wt = latest(d, days, "weight_kg");
  if (wt) {
    const bmi = latest(d, days, "bmi"), fat = latest(d, days, "body_fat_pct");
    const sub = [fat ? `${t("Body fat")} ${fmt(fat.value, 1)}%` : "", when(wt.date)].filter(Boolean).join(" · ");
    rows.push({ key: "weight", Icon: Scale, label: t("Weight"), value: fmt(wt.value, 1), unit: "kg", st: bmi ? [`BMI ${fmt(bmi.value, 1)}`, bmi.value >= 18.5 && bmi.value < 25 ? "good" : "fair"] : undefined, sub, spark: spark("weight_kg") });
  }
  const sys = latest(d, days, "systolic"), dia = latest(d, days, "diastolic");
  if (sys && dia) rows.push({ key: "bp", Icon: HeartPulse, label: t("Blood pressure"), value: `${fmt(sys.value)}/${fmt(dia.value)}`, unit: "mmHg",
    st: sys.value >= 140 || dia.value >= 90 ? ["High", "low"] : sys.value >= 130 || dia.value >= 85 ? ["Raised", "fair"] : ["Normal", "good"], sub: when(sys.date), spark: spark("systolic") });
  const glu = latest(d, days, "glucose_mmol");
  if (glu) rows.push({ key: "glu", Icon: Droplet, label: t("Blood glucose"), value: fmt(glu.value, 1), unit: "mmol/L",
    st: glu.value < 3.9 ? ["Low", "fair"] : glu.value <= 7.8 ? ["In range", "good"] : ["High", "fair"], sub: when(glu.date), spark: spark("glucose_mmol") });
  if (!rows.length) return null;

  return (
    <Card title={t("Body")}>
      <ul className="wb-vitals">
        {rows.map((r) => (
          <li key={r.key}>
            <span className="wb-ico t-plain"><r.Icon size={16} /></span>
            <div className="wb-v-name">
              <b>{r.label}</b>
              <span className="wb-s">{r.st && <span className={`wb-st t-${r.st[1]}`}>{t(r.st[0])}</span>}{r.st && r.sub ? " · " : ""}{r.sub}</span>
            </div>
            <span className="wb-v-spark">{r.spark && r.spark.length >= 3 && <Spark points={r.spark} tone="muted" w={72} h={24} />}</span>
            <Big value={r.value} unit={r.unit} size="s" />
          </li>
        ))}
      </ul>
    </Card>
  );
}
