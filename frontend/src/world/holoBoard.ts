// A dashboard on its hologram, drawn as the dashboard is: the ```bigscreen
// panels laid out on its 12-column grid — numbers as numbers, gauges as
// rings, ranks as bars, charts as charts — in the hologram's blue light.
//
// The dashboard page draws these with @ai-gui's plugin, as page elements; a
// hologram is a canvas texture, so they are drawn here instead. A chart goes
// through ECharts' server-side renderer to an SVG and onto the canvas as an
// image, which arrives a moment later and repaints the board.

import * as echarts from "echarts";

type Item = { name?: string; label?: string; title?: string; value?: number | string };
type Panel = {
  kind?: string; span?: number; title?: string; label?: string;
  value?: number | string; prefix?: string; unit?: string; decimals?: number; max?: number;
  items?: Item[]; option?: Record<string, any>;
};
export type Board = { title: string; panels: Panel[] };

/** The ```bigscreen block of a dashboard's source, or null when it has none. */
export function boardOf(source: string | undefined): Board | null {
  if (!source) return null;
  const fence = /```bigscreen\n([\s\S]*?)```/g;
  let m: RegExpExecArray | null;
  while ((m = fence.exec(source))) {
    try {
      const j = JSON.parse(m[1]);
      const panels = Array.isArray(j?.panels) ? (j.panels as Panel[]) : [];
      if (panels.length) return { title: String(j.title ?? ""), panels };
    } catch { /* not JSON: try the next block */ }
  }
  return null;
}

type Ink = { title: string; text: string; dim: string; rule: string; blue: string; series: string[] };
function inkOf(night: boolean): Ink {
  return night
    ? { title: "#eef3ff", text: "rgba(222,232,255,0.94)", dim: "rgba(178,200,255,0.82)", rule: "rgba(143,176,255,0.32)", blue: "#8fb0ff",
        series: ["#8fb0ff", "#b9cdff", "#6f96ff", "#dbe5ff", "#5b80f0", "#a3bcff"] }
    : { title: "#1d4ed8", text: "rgba(31,63,153,0.92)", dim: "rgba(47,107,255,0.62)", rule: "rgba(47,107,255,0.28)", blue: "#2f6bff",
        series: ["#2f6bff", "#6f96ff", "#1d4ed8", "#9db8ff", "#4f7dff", "#c3d3ff"] };
}

function num(v: number | string | undefined, decimals?: number): string {
  if (typeof v !== "number") return v == null ? "" : String(v);
  const d = typeof decimals === "number" ? decimals : Number.isInteger(v) ? 0 : 2;
  const abs = Math.abs(v);
  // Big numbers in the short form a wall reads at a glance.
  if (abs >= 1e8) return (v / 1e8).toFixed(2) + "亿";
  if (abs >= 1e5) return (v / 1e4).toFixed(1) + "万";
  return v.toLocaleString(undefined, { minimumFractionDigits: d, maximumFractionDigits: d });
}

// Charts, by board and panel, as images once ECharts has drawn them.
const charts = new Map<string, HTMLImageElement | "pending" | "failed">();

function chartImage(key: string, option: Record<string, any>, w: number, h: number, ink: Ink, done: () => void): HTMLImageElement | null {
  const had = charts.get(key);
  if (had instanceof HTMLImageElement) return had;
  if (had) return null;
  charts.set(key, "pending");
  try {
    const chart = echarts.init(null as unknown as HTMLElement, undefined, { renderer: "svg", ssr: true, width: Math.round(w), height: Math.round(h) });
    chart.setOption({
      ...option,
      title: undefined,
      animation: false,
      backgroundColor: "transparent",
      color: ink.series,
      textStyle: { color: ink.text, fontFamily: "'Geist Mono', 'SF Mono', Menlo, 'PingFang SC', monospace", fontSize: 13 },
      // No legend: on a pane this size it only crowds the labels, which
      // already name each slice or bar.
      legend: { show: false },
    });
    const svg = chart.renderToSVGString();
    chart.dispose();
    const img = new Image();
    img.onload = () => { charts.set(key, img); done(); };
    img.onerror = () => charts.set(key, "failed");
    img.src = "data:image/svg+xml;charset=utf-8," + encodeURIComponent(svg);
  } catch {
    charts.set(key, "failed");
  }
  return null;
}

/** Paints board onto a hologram's canvas. repaint is called when a chart
 *  that was still being drawn is ready. */
export function paintBoard(c: CanvasRenderingContext2D, w: number, h: number, key: string, name: string, status: string,
  board: Board, night: boolean, repaint: () => void) {
  const ink = inkOf(night);
  const k = w / 768;
  const mono = (px: number, weight = 500) => `${weight} ${px * k}px 'Geist Mono', 'SF Mono', Menlo, 'PingFang SC', monospace`;

  // The pane, as every hologram has it.
  const bg = c.createLinearGradient(0, 0, 0, h);
  if (night) { bg.addColorStop(0, "rgba(10,24,66,0.62)"); bg.addColorStop(1, "rgba(24,52,130,0.72)"); }
  else { bg.addColorStop(0, "rgba(91,141,255,0.05)"); bg.addColorStop(1, "rgba(91,141,255,0.17)"); }
  c.fillStyle = bg; c.fillRect(0, 0, w, h);
  c.fillStyle = "rgba(47,107,255,0.05)";
  for (let y = 0; y < h; y += 6 * k) c.fillRect(0, y, w, 2 * k);
  c.shadowColor = night ? "rgba(70,120,255,0.9)" : "rgba(91,141,255,0.6)";
  c.shadowBlur = 8 * k;
  c.textBaseline = "top";

  const pad = 26 * k;
  c.font = mono(30, 600); c.fillStyle = ink.title;
  c.fillText(name, pad, 20 * k);
  const nameW = c.measureText(name).width;
  c.font = mono(18, 400); c.fillStyle = ink.dim;
  c.fillText(status, pad + nameW + 16 * k, 30 * k);
  c.fillStyle = ink.rule; c.fillRect(pad, 64 * k, w - pad * 2, 2 * k);

  // Rows of the 12-column grid; a row of numbers is short, one with a chart
  // or a rank tall.
  const rows: Panel[][] = [];
  let row: Panel[] = [], used = 0;
  for (const p of board.panels) {
    const span = Math.min(12, Math.max(1, p.span ?? 3));
    if (used + span > 12 && row.length) { rows.push(row); row = []; used = 0; }
    row.push({ ...p, span }); used += span;
  }
  if (row.length) rows.push(row);
  const tall = (r: Panel[]) => r.some((p) => p.kind === "chart" || p.kind === "rank");
  const units = rows.reduce((n, r) => n + (tall(r) ? 2.2 : 1), 0);
  const top = 78 * k, gap = 12 * k;
  const unitH = (h - top - pad * 0.6 - gap * (rows.length - 1)) / units;
  const colW = (w - pad * 2) / 12;

  let y = top;
  rows.forEach((r, ri) => {
    const rh = unitH * (tall(r) ? 2.2 : 1);
    let x = pad;
    r.forEach((p, pi) => {
      const pw = colW * p.span! - gap * 0.5;
      cell(p, x, y, pw, rh, `${key}/${ri}.${pi}`);
      x += colW * p.span!;
    });
    y += rh + gap;
  });

  function label(t: string | undefined, x: number, y: number, max: number) {
    if (!t) return;
    c.font = mono(16, 500); c.fillStyle = ink.dim;
    let s = t;
    while (s.length > 1 && c.measureText(s).width > max) s = s.slice(0, -1);
    c.fillText(s === t ? s : s.slice(0, -1) + "…", x, y);
  }

  function cell(p: Panel, x: number, y: number, pw: number, ph: number, ck: string) {
    // A hairline frame, as the bigscreen's panels have.
    c.strokeStyle = ink.rule; c.lineWidth = 1.5 * k;
    c.strokeRect(x, y, pw, ph);
    const ix = x + 10 * k, iw = pw - 20 * k;
    label(p.title ?? p.label, ix, y + 8 * k, iw);
    const body = { x: ix, y: y + 32 * k, w: iw, h: ph - 40 * k };
    switch (p.kind) {
      case "kpi": {
        const v = `${p.prefix ?? ""}${num(p.value, p.decimals)}`;
        let px = Math.min(44, body.h / k * 0.8);
        c.font = mono(px, 700);
        while (px > 14 && c.measureText(v + (p.unit ?? "")).width > body.w) { px -= 2; c.font = mono(px, 700); }
        c.fillStyle = ink.title;
        c.fillText(v, body.x, body.y);
        if (p.unit) {
          const vw = c.measureText(v).width;
          c.font = mono(px * 0.45, 500); c.fillStyle = ink.dim;
          c.fillText(" " + p.unit, body.x + vw, body.y + px * k * 0.45);
        }
        break;
      }
      case "gauge": {
        const v = typeof p.value === "number" ? p.value : Number(p.value) || 0;
        const max = p.max ?? 100;
        const r = Math.min(body.w / 2, body.h) * 0.9;
        const cx = body.x + body.w / 2, cy = body.y + body.h * 0.95;
        c.lineCap = "round";
        c.lineWidth = 7 * k; c.strokeStyle = ink.rule;
        c.beginPath(); c.arc(cx, cy, r, Math.PI, 2 * Math.PI); c.stroke();
        c.strokeStyle = ink.blue;
        c.beginPath(); c.arc(cx, cy, r, Math.PI, Math.PI + Math.PI * Math.max(0, Math.min(1, v / max))); c.stroke();
        c.font = mono(Math.min(28, r / k * 0.55), 700); c.fillStyle = ink.title;
        const t = `${num(v, p.decimals)}${p.unit ?? ""}`;
        c.fillText(t, cx - c.measureText(t).width / 2, cy - r * 0.55);
        break;
      }
      case "rank": {
        const items = (p.items ?? []).filter((it) => it.name ?? it.label ?? it.title);
        const n = Math.max(1, Math.min(items.length, Math.floor(body.h / (24 * k))));
        const max = Math.max(...items.slice(0, n).map((it) => Number(it.value) || 0), 1);
        const rowH = body.h / n;
        items.slice(0, n).forEach((it, i) => {
          const ry = body.y + i * rowH;
          const nm = String(it.name ?? it.label ?? it.title);
          const val = num(it.value);
          c.font = mono(15, 500); c.fillStyle = ink.text;
          const vw = c.measureText(val).width;
          let s = nm;
          while (s.length > 1 && c.measureText(s).width > body.w * 0.5) s = s.slice(0, -1);
          c.fillText(s === nm ? s : s.slice(0, -1) + "…", body.x, ry);
          c.fillText(val, body.x + body.w - vw, ry);
          const bw = (body.w * 0.48 - vw - 8 * k) * ((Number(it.value) || 0) / max);
          c.fillStyle = ink.blue;
          c.fillRect(body.x + body.w * 0.5, ry + 5 * k, Math.max(2 * k, bw), 9 * k);
        });
        break;
      }
      case "chart": {
        if (!p.option) break;
        const img = chartImage(ck, p.option, body.w, body.h, ink, repaint);
        if (img) {
          c.shadowBlur = 0;
          c.drawImage(img, body.x, body.y, body.w, body.h);
          c.shadowBlur = 8 * k;
        }
        break;
      }
      default:
        if (p.value != null) {
          c.font = mono(28, 700); c.fillStyle = ink.title;
          c.fillText(`${p.prefix ?? ""}${num(p.value, p.decimals)}${p.unit ? " " + p.unit : ""}`, body.x, body.y);
        }
    }
  }
}

/** Forget a board's chart images, for a board that changed. */
export function forgetCharts(key: string) {
  for (const k of charts.keys()) if (k.startsWith(key + "/")) charts.delete(k);
}
