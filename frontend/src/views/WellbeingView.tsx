import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import * as echarts from "echarts";
import { EventsOn } from "../../wailsjs/runtime";
import { WellbeingDays, WellbeingStatus, WellbeingToday } from "../../wailsjs/go/app/App";
import { useI18n } from "../lib/i18n";
import "./wellbeing.css";

/**
 * Wellbeing: the person's health, one summary per day, as the iPhone reads it
 * from Apple Health, kept by the vita MCP server and read through SuperLeo.
 * Today's numbers first, then how one of them has gone, then what was written
 * about the day.
 */

type Day = Record<string, any>;
interface Today { day?: Day; is_today: boolean; last_report?: string; note?: string }
interface Status { connected: boolean; days?: number; first?: string; last?: string; last_report?: string }

const num = (v: unknown): number | undefined => (typeof v === "number" && isFinite(v) ? v : undefined);
const get = (d: Day | undefined, key: string): number | undefined => {
  let cur: any = d;
  for (const p of key.split(".")) cur = cur?.[p];
  return num(cur);
};
const fmt = (v: number | undefined, digits = 0) =>
  v === undefined ? "—" : v.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits });
const hm = (iso?: string) => {
  if (!iso) return "";
  const d = new Date(iso);
  return isNaN(d.getTime()) ? "" : d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
};
const ago = (iso?: string) => {
  if (!iso) return "";
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 90) return "just now";
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  if (s < 86400 * 2) return `${Math.round(s / 3600)} h ago`;
  return `${Math.round(s / 86400)} days ago`;
};

/** The metrics a trend can show, with their unit and decimals. */
const METRICS: { key: string; label: string; unit: string; digits: number }[] = [
  { key: "resting_hr", label: "Resting HR", unit: "bpm", digits: 0 },
  { key: "hrv_ms", label: "HRV", unit: "ms", digits: 0 },
  { key: "sleep_h", label: "Sleep", unit: "h", digits: 1 },
  { key: "steps", label: "Steps", unit: "", digits: 0 },
  { key: "recovery", label: "Recovery", unit: "", digits: 0 },
  { key: "weight_kg", label: "Weight", unit: "kg", digits: 1 },
];
const SPANS = [7, 30, 90];

function css(name: string, fallback: string) {
  const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return v || fallback;
}

function TrendChart({ points, unit, digits }: { points: { date: string; value: number }[]; unit: string; digits: number }) {
  const ref = useRef<HTMLDivElement>(null);
  const inst = useRef<echarts.ECharts | null>(null);
  useEffect(() => {
    if (!ref.current) return;
    inst.current = echarts.init(ref.current, undefined, { renderer: "canvas" });
    const ro = new ResizeObserver(() => inst.current?.resize());
    ro.observe(ref.current);
    return () => { ro.disconnect(); inst.current?.dispose(); inst.current = null; };
  }, []);
  useEffect(() => {
    const blue = "#2f6bff";
    const ink3 = css("--ink-3", "#8a93a6");
    const line = css("--line", "rgba(20,30,60,0.08)");
    inst.current?.setOption({
      backgroundColor: "transparent",
      animationDuration: 400,
      grid: { left: 44, right: 16, top: 16, bottom: 28 },
      tooltip: { trigger: "axis", valueFormatter: (v: number) => `${fmt(v, digits)}${unit ? " " + unit : ""}` },
      xAxis: { type: "category", data: points.map((p) => p.date.slice(5)), axisLine: { lineStyle: { color: line } }, axisTick: { show: false },
        axisLabel: { color: ink3, fontFamily: "Geist Mono Variable, monospace", fontSize: 11 } },
      yAxis: { type: "value", scale: true, splitLine: { lineStyle: { color: line } },
        axisLabel: { color: ink3, fontFamily: "Geist Mono Variable, monospace", fontSize: 11 } },
      series: [{
        type: "line", data: points.map((p) => p.value), smooth: 0.3, symbol: "circle", symbolSize: 5, showSymbol: points.length <= 31,
        lineStyle: { width: 2, color: blue }, itemStyle: { color: blue },
        areaStyle: { color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [{ offset: 0, color: "rgba(47,107,255,0.18)" }, { offset: 1, color: "rgba(47,107,255,0)" }]) },
      }],
    }, { notMerge: true });
  }, [points, unit, digits]);
  return <div ref={ref} className="wb-chart" />;
}

/** A 0–100 score as a thin ring. */
function Ring({ value, label, invert }: { value?: number; label: string; invert?: boolean }) {
  const r = 34, c = 2 * Math.PI * r;
  const v = value === undefined ? 0 : Math.max(0, Math.min(100, value));
  const tone = value === undefined ? "" : (invert ? 100 - v : v) >= 67 ? "good" : (invert ? 100 - v : v) >= 34 ? "mid" : "low";
  return (
    <div className={`wb-ring ${tone}`}>
      <svg viewBox="0 0 80 80" width="80" height="80" aria-hidden>
        <circle cx="40" cy="40" r={r} className="wb-ring-track" />
        <circle cx="40" cy="40" r={r} className="wb-ring-fill" strokeDasharray={`${(v / 100) * c} ${c}`} transform="rotate(-90 40 40)" />
      </svg>
      <div className="wb-ring-val">{fmt(value)}</div>
      <div className="wb-label">{label}</div>
    </div>
  );
}

function Stat({ label, value, unit, sub }: { label: string; value: string; unit?: string; sub?: string }) {
  return (
    <div className="wb-stat">
      <div className="wb-label">{label}</div>
      <div className="wb-value">{value}{unit && value !== "—" && <span className="wb-unit">{unit}</span>}</div>
      {sub && <div className="wb-sub">{sub}</div>}
    </div>
  );
}

/** Against the person's own baseline, as "+3 vs 55". */
function vsBase(v?: number, base?: number) {
  if (v === undefined || base === undefined) return undefined;
  const d = Math.round(v - base);
  return `${d > 0 ? "+" : ""}${d} vs ${Math.round(base)}`;
}

export default function WellbeingView() {
  const { t } = useI18n();
  const [today, setToday] = useState<Today | null>(null);
  const [status, setStatus] = useState<Status | null>(null);
  const [days, setDays] = useState<Day[]>([]);
  const [metric, setMetric] = useState(METRICS[0].key);
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

  const d = today?.day;
  const m = METRICS.find((x) => x.key === metric)!;
  const points = useMemo(
    () => days.flatMap((x) => { const v = get(x, metric); return v === undefined ? [] : [{ date: String(x.date), value: v }]; }),
    [days, metric],
  );
  const avg = points.length ? points.reduce((s, p) => s + p.value, 0) / points.length : undefined;

  if (!status && !err) return <div className="wb-view" />;
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

  const insights: { title: string; detail?: string; tone?: string }[] = Array.isArray(d?.insights) ? d!.insights : [];
  const suggestions: { title: string; reason?: string; time?: string; kind?: string }[] = Array.isArray(d?.suggestions) ? d!.suggestions : [];
  const sleep = d?.sleep as { asleep_h?: number; deep_h?: number; rem_h?: number; bedtime?: string; wake?: string } | undefined;

  return (
    <div className="wb-view">
      <header className="wb-head">
        <h1>{t("Wellbeing")}</h1>
        <div className="wb-meta">
          {d?.date && <span>{today?.is_today ? t("Today") : String(d.date)}</span>}
          {today?.last_report && <span>{t("Reported")} {t(ago(today.last_report))}</span>}
          {status && <span>{status.days} {t("days")}</span>}
        </div>
      </header>
      {err && <div className="wb-error">{err}</div>}

      <section className="wb-scores">
        <Ring value={get(d, "recovery")} label={t("Recovery")} />
        <Ring value={get(d, "stress")} label={t("Stress")} invert />
        <Stat label={t("Sleep")} value={fmt(get(d, "sleep_h") ?? sleep?.asleep_h, 1)} unit="h"
          sub={sleep?.deep_h !== undefined ? `${t("Deep")} ${fmt(sleep.deep_h, 1)} · REM ${fmt(sleep.rem_h, 1)}${sleep.bedtime ? ` · ${hm(sleep.bedtime)}–${hm(sleep.wake)}` : ""}` : undefined} />
        <Stat label={t("Resting HR")} value={fmt(get(d, "resting_hr"))} unit="bpm" sub={vsBase(get(d, "resting_hr"), get(d, "resting_hr_baseline"))} />
        <Stat label="HRV" value={fmt(get(d, "hrv_ms"))} unit="ms" sub={vsBase(get(d, "hrv_ms"), get(d, "hrv_baseline_ms"))} />
        <Stat label={t("Steps")} value={fmt(get(d, "steps"))} sub={get(d, "active_kcal") !== undefined ? `${fmt(get(d, "active_kcal"))} kcal · ${fmt(get(d, "exercise_min"))} min` : undefined} />
        {get(d, "spo2_pct") !== undefined && <Stat label={t("Blood oxygen")} value={fmt(get(d, "spo2_pct"), 1)} unit="%" />}
        {get(d, "weight_kg") !== undefined && <Stat label={t("Weight")} value={fmt(get(d, "weight_kg"), 1)} unit="kg" />}
      </section>

      <section className="wb-card">
        <div className="wb-card-head">
          <div className="wb-tabs" role="tablist">
            {METRICS.map((x) => (
              <button key={x.key} role="tab" aria-selected={metric === x.key} className={metric === x.key ? "on" : ""} onClick={() => setMetric(x.key)}>{t(x.label)}</button>
            ))}
          </div>
          <div className="wb-tabs" role="tablist">
            {SPANS.map((n) => (
              <button key={n} role="tab" aria-selected={span === n} className={span === n ? "on" : ""} onClick={() => setSpan(n)}>{n}{t("d")}</button>
            ))}
          </div>
        </div>
        {points.length ? (
          <>
            <div className="wb-trend-sum">
              <span>{t("Average")} <b>{fmt(avg, m.digits)}</b>{m.unit && ` ${m.unit}`}</span>
              <span>{t("Low")} <b>{fmt(Math.min(...points.map((p) => p.value)), m.digits)}</b></span>
              <span>{t("High")} <b>{fmt(Math.max(...points.map((p) => p.value)), m.digits)}</b></span>
            </div>
            <TrendChart points={points} unit={m.unit} digits={m.digits} />
          </>
        ) : (
          <div className="wb-none">{t("Nothing reported for this yet")}</div>
        )}
      </section>

      {(insights.length > 0 || suggestions.length > 0) && (
        <section className="wb-notes">
          {insights.length > 0 && (
            <div className="wb-card">
              <div className="wb-card-title">{t("Insights")}</div>
              <ul className="wb-list">
                {insights.map((i, n) => (
                  <li key={n} className={`wb-tone-${i.tone ?? "neutral"}`}><b>{i.title}</b>{i.detail && <span>{i.detail}</span>}</li>
                ))}
              </ul>
            </div>
          )}
          {suggestions.length > 0 && (
            <div className="wb-card">
              <div className="wb-card-title">{t("Suggestions")}</div>
              <ul className="wb-list">
                {suggestions.map((s, n) => (
                  <li key={n}><b>{s.time ? `${s.time} · ` : ""}{s.title}</b>{s.reason && <span>{s.reason}</span>}</li>
                ))}
              </ul>
            </div>
          )}
        </section>
      )}
      <footer className="wb-foot">{t("Not medical advice")}</footer>
    </div>
  );
}
