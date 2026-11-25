import React, { useEffect, useLayoutEffect, useRef, useState } from "react";
import * as echarts from "echarts";
import { useDaylight } from "../lib/useDaylight";

/**
 * The pieces the Wellbeing page is built from, after Vita's: the body figure
 * with its three readings, rings and the half gauge, the sleep-stage bar, the
 * day track, and small charts that take their colours from the page's tokens.
 */

export type Day = Record<string, any>;
export type Tone = "good" | "fair" | "low" | "muted" | "info";

export const num = (v: unknown): number | undefined => (typeof v === "number" && isFinite(v) ? v : undefined);
export const get = (d: Day | undefined, key: string): number | undefined => {
  let cur: any = d;
  for (const p of key.split(".")) cur = cur?.[p];
  return num(cur);
};
export const fmt = (v: number | undefined, digits = 0) =>
  v === undefined ? "––" : v.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits });
export const signed = (v: number, digits = 0) => `${v > 0 ? "+" : v < 0 ? "−" : ""}${fmt(Math.abs(v), digits)}`;
export const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v));
export const toDate = (iso?: string) => {
  if (!iso) return undefined;
  const d = new Date(iso);
  return isNaN(d.getTime()) ? undefined : d;
};
export const hm = (iso?: string | Date) => {
  const d = iso instanceof Date ? iso : toDate(iso);
  return d ? `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}` : "";
};
/** "2026-11-18" as a local date, at noon so no zone moves it a day. */
export const dayOf = (ymd: string) => new Date(`${ymd}T12:00:00`);
export const mean = (xs: number[]) => (xs.length ? xs.reduce((s, x) => s + x, 0) / xs.length : undefined);
export const median = (xs: number[]) => {
  if (!xs.length) return undefined;
  const s = [...xs].sort((a, b) => a - b), m = s.length >> 1;
  return s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2;
};
export type Point = { date: string; value: number };
export const seriesOf = (days: Day[], key: string): Point[] =>
  days.flatMap((x) => { const v = get(x, key); return v === undefined ? [] : [{ date: String(x.date), value: v }]; });

/** The token's current value, read where it is used so the theme applies. */
export function cssv(el: Element | null, name: string, fallback = "") {
  if (!el) return fallback;
  return getComputedStyle(el).getPropertyValue(name).trim() || fallback;
}

export function Pill({ tone, children, dot }: { tone: Tone; children: React.ReactNode; dot?: boolean }) {
  return <span className={`wb-pill t-${tone}`}>{dot && <i />}{children}</span>;
}

export function Big({ value, unit, size }: { value: string; unit?: string; size?: "l" | "m" | "s" }) {
  return (
    <span className={`wb-big ${size ?? "m"}`}>
      {value}{unit && value !== "––" && <small>{unit}</small>}
    </span>
  );
}

/** A 0–100 score as a ring, number in the middle. */
export function Ring({ value, size = 132, stroke = 10, tone }: { value?: number; size?: number; stroke?: number; tone: Tone }) {
  const r = (size - stroke) / 2, c = 2 * Math.PI * r;
  const v = value === undefined ? 0 : clamp(value, 0, 100);
  return (
    <div className={`wb-ring t-${tone}`} style={{ width: size, height: size }}>
      <svg viewBox={`0 0 ${size} ${size}`} width={size} height={size} aria-hidden>
        <circle cx={size / 2} cy={size / 2} r={r} className="trk" strokeWidth={stroke} />
        <circle cx={size / 2} cy={size / 2} r={r} className="arc" strokeWidth={stroke}
          strokeDasharray={`${(v / 100) * c} ${c}`} transform={`rotate(-90 ${size / 2} ${size / 2})`} />
      </svg>
      <b>{value === undefined ? "––" : Math.round(value)}</b>
    </div>
  );
}

/** Vita's half gauge: 0 at the left, 100 at the right, a knob where it stands. */
export function Gauge({ value, tone }: { value?: number; tone: Tone }) {
  const w = 120, h = 66, r = 52, cx = w / 2, cy = h - 6;
  const f = clamp((value ?? 0) / 100, 0, 1);
  const a = Math.PI * (1 + f);
  const x = cx + r * Math.cos(a), y = cy + r * Math.sin(a);
  const arc = (to: number) => `M ${cx - r} ${cy} A ${r} ${r} 0 0 1 ${cx + r * Math.cos(to)} ${cy + r * Math.sin(to)}`;
  return (
    <svg className={`wb-gauge t-${tone}`} viewBox={`0 0 ${w} ${h}`} width={w} height={h} aria-hidden>
      <path d={arc(2 * Math.PI)} className="trk" />
      {value !== undefined && f > 0 && <path d={arc(a)} className="arc" />}
      {value !== undefined && <circle cx={x} cy={y} r={5.5} className="knob" />}
    </svg>
  );
}

/** A line, no axes, for a row or a tile. */
export function Spark({ points, tone = "info", w = 96, h = 26 }: { points: number[]; tone?: Tone; w?: number; h?: number }) {
  if (points.length < 2) return null;
  const lo = Math.min(...points), hi = Math.max(...points), span = hi - lo || 1;
  const xy = points.map((v, i) => [(i / (points.length - 1)) * (w - 4) + 2, h - 3 - ((v - lo) / span) * (h - 6)]);
  return (
    <svg className={`wb-spark t-${tone}`} viewBox={`0 0 ${w} ${h}`} width={w} height={h} aria-hidden>
      <polyline points={xy.map((p) => p.join(",")).join(" ")} />
      <circle cx={xy[xy.length - 1][0]} cy={xy[xy.length - 1][1]} r={2.5} />
    </svg>
  );
}

/** Contribution to a score, drawn out from the middle: right helps, left costs. */
export function Diverge({ value, max = 15 }: { value: number; max?: number }) {
  const f = clamp(Math.abs(value) / max, 0, 1) * 50;
  return (
    <span className="wb-div" aria-hidden>
      <i className={value >= 0 ? "pos" : "neg"} style={value >= 0 ? { left: "50%", width: `${f}%` } : { right: "50%", width: `${f}%` }} />
    </span>
  );
}

/** One stacked bar of parts, each a share of the whole, 2px apart. */
export function Stack({ parts, height = 14 }: { parts: { key: string; value: number; cls: string; title: string }[]; height?: number }) {
  const total = parts.reduce((s, p) => s + Math.max(0, p.value), 0) || 1;
  return (
    <div className="wb-stack" style={{ height }}>
      {parts.filter((p) => p.value > 0).map((p) => (
        <span key={p.key} className={p.cls} style={{ flexGrow: p.value / total }} title={p.title} />
      ))}
    </div>
  );
}

// ── Charts ──────────────────────────────────────────────────────────────

type ChartColors = { ink: string; ink2: string; ink3: string; line: string; pop: string; [k: string]: string };

/** An ECharts box whose option is built from the page's colours, again on a theme change. */
export function Chart({ build, height, deps }: { build: (c: ChartColors) => echarts.EChartsOption; height: number; deps: unknown[] }) {
  const ref = useRef<HTMLDivElement>(null);
  const inst = useRef<echarts.ECharts | null>(null);
  const light = useDaylight();
  useEffect(() => {
    if (!ref.current) return;
    inst.current = echarts.init(ref.current, undefined, { renderer: "svg" });
    const ro = new ResizeObserver(() => inst.current?.resize());
    ro.observe(ref.current);
    return () => { ro.disconnect(); inst.current?.dispose(); inst.current = null; };
  }, []);
  useEffect(() => {
    const el = ref.current;
    // A frame later, so the page's tokens have switched with the theme.
    const id = requestAnimationFrame(() => {
      const c: ChartColors = {
        ink: cssv(el, "--wb-t0", "#1d1a16"), ink2: cssv(el, "--wb-t2", "#6b7076"), ink3: cssv(el, "--wb-t3", "#9aa0a6"),
        line: cssv(el, "--wb-line", "rgba(0,0,0,.08)"), pop: cssv(el, "--wb-pop", "#fff"),
        green: cssv(el, "--wb-green", "#3f9d5a"), blue: cssv(el, "--wb-blue", "#4a6cd9"), orange: cssv(el, "--wb-orange", "#d9822b"),
        red: cssv(el, "--wb-red", "#c9463d"), silhouette: cssv(el, "--wb-silhouette", "#e4e1da"),
        deep: cssv(el, "--wb-s-deep", "#3a4fc0"), core: cssv(el, "--wb-s-core", "#6f8ff0"), rem: cssv(el, "--wb-s-rem", "#9cc4f5"),
      };
      inst.current?.setOption(build(c), { notMerge: true });
    });
    return () => cancelAnimationFrame(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [light, ...deps]);
  return <div ref={ref} className="wb-chartbox" style={{ height }} />;
}

/** The parts every small chart shares: recessive axes, a tooltip in the page's colours. */
export function base(c: ChartColors, fmtVal: (v: number) => string, dates: string[], short: (d: string) => string): echarts.EChartsOption {
  const mono = "Geist Mono Variable, ui-monospace, monospace";
  return {
    backgroundColor: "transparent",
    animationDuration: 350,
    grid: { left: 34, right: 8, top: 10, bottom: 22 },
    tooltip: {
      trigger: "axis", confine: true, backgroundColor: c.pop, borderColor: c.line, borderWidth: 1, padding: [6, 9],
      textStyle: { color: c.ink, fontSize: 12, fontFamily: mono },
      axisPointer: { type: "line", lineStyle: { color: c.ink3, width: 1, type: "dashed" } },
      formatter: (ps: any) => {
        const p = Array.isArray(ps) ? ps[0] : ps;
        return `<span style="color:${c.ink2}">${short(dates[p.dataIndex] ?? "")}</span>&nbsp;&nbsp;<b>${fmtVal(Number(p.value))}</b>`;
      },
    },
    xAxis: {
      type: "category", data: dates.map(short), axisLine: { lineStyle: { color: c.line } }, axisTick: { show: false },
      axisLabel: { color: c.ink3, fontFamily: mono, fontSize: 10, hideOverlap: true },
    },
    yAxis: {
      type: "value", scale: true, splitNumber: 3, splitLine: { lineStyle: { color: c.line } },
      axisLabel: { color: c.ink3, fontFamily: mono, fontSize: 10 },
    },
  };
}

// ── The body figure ─────────────────────────────────────────────────────

/** Vita's Home figure, front view, head to hips, a watch on the left wrist (240×350). */
const FIG = { shoulder: 54, waist: 84, hip: 80, arm: 27, neck: 15, headW: 27, headH: 34 };
export const FIGURE_POINTS = { lungs: { x: 98, y: 158 }, heart: { x: 136, y: 186 }, wrist: { x: 240 - FIG.shoulder - 6, y: 298 } };

function torso() {
  const { shoulder: s, waist: w, hip: h, neck: n } = FIG;
  return `M ${120 - n} 104 C ${120 - n - 4} 116 ${s + 34} 118 ${s + 16} 124 Q ${s - 2} 128 ${s + 4} 150 L ${s + 20} 172
    C ${s + 22} 206 ${w - 2} 236 ${w} 262 C ${w + 1} 290 ${h + 2} 320 ${h} 350 L ${240 - h} 350
    C ${240 - h - 2} 320 ${240 - w - 1} 290 ${240 - w} 262 C ${240 - w + 2} 236 ${240 - s - 22} 206 ${240 - s - 20} 172
    L ${240 - s - 4} 150 Q ${240 - s + 2} 128 ${240 - s - 16} 124 C ${240 - s - 34} 118 ${120 + n + 4} 116 ${120 + n} 104 Z`;
}
function arm() {
  const { shoulder: s, arm: w } = FIG;
  return `M ${s + 16} 122 C ${s - 4} 122 ${s - 9} 140 ${s - 7} 164 C ${s - 6} 188 ${s - 12} 212 ${s - 9} 236
    C ${s - 7} 256 ${s - 6} 282 ${s - 3} 300 C ${s - 4} 316 ${s - 3} 334 ${s + 4} 340
    C ${s + 10} 347 ${s - 3 + w * 0.72} 342 ${s - 3 + w * 0.72} 330 C ${s - 2 + w * 0.74} 320 ${s - 4 + w * 0.7} 310 ${s - 4 + w * 0.68} 302
    C ${s - 4 + w * 0.7} 282 ${s - 9 + w * 0.84} 262 ${s - 9 + w * 0.82} 240 C ${s - 9 + w * 0.8} 214 ${s + 18} 196 ${s + 20} 174
    Q ${s + 24} 140 ${s + 16} 122 Z`;
}
function hairCap() {
  const r = FIG.headW;
  return `M ${120 - r - 1} 62 C ${120 - r - 4} 18 ${120 + r + 4} 18 ${120 + r + 1} 62 C ${120 + r - 2} 48 140 42 126 42 C 108 42 98 46 ${120 - r - 1} 62 Z`;
}
function details() {
  let d = "";
  for (const m of [false, true]) {
    const x = (v: number) => (m ? 240 - v : v);
    d += `M ${x(116)} 122 Q ${x(98)} 128 ${x(FIG.shoulder + 26)} 126 `;
    d += `M ${x(92)} 204 Q ${x(104)} 214 ${x(117)} 210 `;
  }
  return d + "M 120 140 L 120 214";
}

export function Figure() {
  const { neck: n, headW, headH } = FIG, c = FIGURE_POINTS.wrist;
  return (
    <svg className="wb-fig" viewBox="0 0 240 350" width={240} height={350} aria-hidden>
      <defs>
        <linearGradient id="wb-skin" gradientUnits="userSpaceOnUse" x1="60" y1="40" x2="200" y2="350">
          <stop offset="0" className="s1" /><stop offset="0.5" className="s2" /><stop offset="1" className="s3" />
        </linearGradient>
      </defs>
      <path d={torso()} fill="url(#wb-skin)" />
      <path d={`M ${120 - n + 1} 84 L ${120 - n} 110 Q 120 116 ${120 + n} 110 L ${120 + n - 1} 84 Z`} fill="url(#wb-skin)" />
      <ellipse cx={120} cy={99} rx={n + 2} ry={7} className="jaw" />
      <path d={arm()} className="gap" fill="url(#wb-skin)" />
      <path d={arm()} className="gap" fill="url(#wb-skin)" transform="matrix(-1 0 0 1 240 0)" />
      <rect x={c.x - 11} y={c.y - 4} width={22} height={8} rx={3} className="band" />
      <rect x={c.x - 8} y={c.y - 8} width={16} height={16} rx={4.5} className="face" />
      <path d={details()} className="det" />
      <ellipse cx={120 - headW - 1} cy={66} rx={4} ry={8} className="ear" />
      <ellipse cx={120 + headW + 1} cy={66} rx={4} ry={8} className="ear" />
      <ellipse cx={120} cy={64} rx={headW} ry={headH} className="gap" fill="url(#wb-skin)" />
      <path d={hairCap()} className="hair" />
    </svg>
  );
}

/** The card's width, for drawing leaders to a figure pinned at its right. */
export function useWidth<T extends HTMLElement>(): [React.RefObject<T | null>, number] {
  const ref = useRef<T>(null);
  const [w, setW] = useState(0);
  useLayoutEffect(() => {
    if (!ref.current) return;
    const ro = new ResizeObserver(([e]) => setW(e.contentRect.width));
    ro.observe(ref.current);
    setW(ref.current.getBoundingClientRect().width);
    return () => ro.disconnect();
  }, []);
  return [ref, w];
}
