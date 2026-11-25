import React, { useCallback, useEffect, useMemo, useState } from "react";
import { CalendarClock, Check, ExternalLink, MessageSquare, RotateCw, X } from "lucide-react";
import { FinanceNews, FinanceTopics, NotedHoldings, Quotes } from "../../wailsjs/go/app/App";
import { useI18n } from "../lib/i18n";
import { openExternal } from "../lib/openExternal";
import { askToCompose } from "../lib/compose";
import { SkeletonCards } from "../components/Skeleton";
import { Big, Chart, Pill, Spark, Stack, Tone } from "./wellbeingParts";
import "./wellbeing.css";
import "./finance.css";

/**
 * Finance: the person's investments as the finance server keeps them —
 * totals per currency (amounts in different currencies are never added
 * together), one row per holding with a month of closes, the monthly buying
 * plans, and the news and argus topics filed against the holdings. Read only;
 * a change is asked of the queen.
 */

export type Money = { amount: number; currency?: string };
export interface Plan {
  active?: boolean; amount?: Money; day?: number; doneThisMonth?: boolean; investedThisMonth?: Money;
  nextTime?: string; streakMonths?: number; time?: string;
}
export interface Position {
  avgCost?: Money; costBasis?: Money; marketValue?: Money; realizedPnl?: Money; unrealizedPnl?: Money;
  unrealizedPercent?: number; shares?: number; trades?: number; hasPrice?: boolean;
}
export interface Holding {
  id: string; name: string; symbol: string; quoteSymbol?: string; currency: string; archived?: boolean; notes?: string;
  price?: Money; priceTime?: string; lastPrice?: Money; lastPriceTime?: string; dayChange?: Money;
  plan?: Plan; position?: Position;
}
interface Quote { symbol: string; price?: number; change?: number; changePercent?: number; closes?: number[]; stale?: boolean }
interface Article { title: string; url: string; outlet?: string; published?: string; symbols?: string[] }
interface Outlet { name: string; url?: string; published?: string }
interface Topic { id: string; title: string; summary?: string; updated?: string; outlets?: string[]; viewpoints?: { text: string; outlets?: Outlet[]; disputed?: boolean }[] }
type T = (k: string, v?: Record<string, string | number>) => string;

const amt = (m?: Money) => (m && typeof m.amount === "number" && isFinite(m.amount) ? m.amount : 0);
const tone = (v: number): Tone => (v > 1e-9 ? "good" : v < -1e-9 ? "low" : "muted");
const quoteKey = (h: Holding) => h.quoteSymbol || h.symbol;

export function money(v: number, cur: string, loc?: string, digits = 2) {
  try {
    return new Intl.NumberFormat(loc, { style: "currency", currency: cur, currencyDisplay: "narrowSymbol", minimumFractionDigits: digits, maximumFractionDigits: digits }).format(v);
  } catch {
    return `${v.toLocaleString(loc, { minimumFractionDigits: digits, maximumFractionDigits: digits })} ${cur}`;
  }
}
const sign = (v: number) => (v > 1e-9 ? "+" : v < -1e-9 ? "−" : "");
export const sMoney = (v: number, cur: string, loc?: string) => `${sign(v)}${money(Math.abs(v), cur, loc)}`;
export const sPct = (f: number | undefined) => (f === undefined || !isFinite(f) ? "––" : `${sign(f)}${Math.abs(f * 100).toFixed(2)}%`);
const qty = (v: number | undefined, loc?: string) => (v === undefined ? "––" : v.toLocaleString(loc, { maximumFractionDigits: 4 }));

const ago = (t: T, iso?: string) => {
  if (!iso) return "";
  const ms = new Date(iso).getTime();
  if (isNaN(ms)) return "";
  const s = Math.max(0, (Date.now() - ms) / 1000);
  if (s < 90) return t("just now");
  if (s < 3600) return t("{count} min ago", { count: Math.round(s / 60) });
  if (s < 86400 * 2) return t("{count} h ago", { count: Math.round(s / 3600) });
  return t("{count} d ago", { count: Math.round(s / 86400) });
};

/** The day's move as a fraction: the quote's when there is one, else the stored one. */
function dayPct(h: Holding, q?: Quote): number | undefined {
  if (q && typeof q.changePercent === "number") return q.changePercent / 100;
  const mv = amt(h.position?.marketValue), dc = amt(h.dayChange);
  return mv - dc > 0 ? dc / (mv - dc) : undefined;
}

export type Totals = { currency: string; value: number; cost: number; unreal: number; real: number; day: number; held: Holding[] };

export function totalsOf(items: Holding[]): Totals[] {
  const by = new Map<string, Totals>();
  for (const h of items) {
    const cur = h.currency || "USD";
    const s = by.get(cur) ?? { currency: cur, value: 0, cost: 0, unreal: 0, real: 0, day: 0, held: [] };
    const p = h.position;
    s.value += amt(p?.marketValue); s.cost += amt(p?.costBasis); s.unreal += amt(p?.unrealizedPnl);
    s.real += amt(p?.realizedPnl); s.day += amt(h.dayChange);
    if ((p?.shares ?? 0) > 0) s.held.push(h);
    by.set(cur, s);
  }
  return [...by.values()].sort((a, b) => b.value - a.value);
}

export default function FinanceView() {
  const { t: t0, language } = useI18n();
  // A few words mean something else here than elsewhere in the app.
  const t: T = useCallback((k, v) => { const s = t0(`fin·${k}`, v); return s.startsWith("fin·") ? t0(k, v) : s; }, [t0]);
  const loc = language === "en" ? undefined : language;

  const [items, setItems] = useState<Holding[] | null>(null);
  const [err, setErr] = useState("");
  const [quotes, setQuotes] = useState<Record<string, Quote>>({});
  const [news, setNews] = useState<Article[]>([]);
  const [topics, setTopics] = useState<{ symbol: string; topic: Topic }[]>([]);
  const [sel, setSel] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setBusy(true);
    try {
      const r = (await NotedHoldings()) as { items?: Holding[] };
      const list = (r?.items ?? []).filter((h) => !h.archived);
      setItems(list); setErr("");
      const syms = [...new Set(list.filter((h) => (h.position?.shares ?? 0) > 0).map(quoteKey))].slice(0, 20);
      Quotes(syms).then((qr) => {
        const m: Record<string, Quote> = {};
        for (const q of ((qr as { quotes?: Quote[] })?.quotes ?? [])) m[q.symbol] = q;
        setQuotes(m);
      }).catch(() => {});
      FinanceNews("", 14).then((n) => setNews(((n as { items?: Article[] })?.items) ?? [])).catch(() => {});
      // Topics are asked per holding; each answers when it can.
      Promise.allSettled(list.filter((h) => (h.position?.shares ?? 0) > 0).map((h) =>
        FinanceTopics(h.symbol, "7d", 3).then((x) => ((x as { topics?: Topic[] })?.topics ?? []).map((topic) => ({ symbol: h.symbol, topic }))),
      )).then((rs) => {
        const all = rs.flatMap((x) => (x.status === "fulfilled" ? x.value : []));
        all.sort((a, b) => String(b.topic.updated ?? "").localeCompare(String(a.topic.updated ?? "")));
        setTopics(all);
      });
    } catch (e) {
      setErr(String((e as Error)?.message ?? e));
    } finally {
      setBusy(false);
    }
  }, []);
  useEffect(() => { load(); }, [load]);

  const totals = useMemo(() => totalsOf(items ?? []), [items]);
  const held = useMemo(() => totals.flatMap((s) => [...s.held].sort((a, b) => amt(b.position?.marketValue) - amt(a.position?.marketValue))), [totals]);
  const plans = useMemo(() => (items ?? []).filter((h) => h.plan?.active), [items]);
  const asOf = useMemo(() => {
    const ts = (items ?? []).map((h) => h.priceTime || h.lastPriceTime || "").filter(Boolean).sort();
    return ts[ts.length - 1];
  }, [items]);
  const selected = held.find((h) => h.id === sel);

  const head = (
    <header className="wb-head">
      <h1>{t("Finance")}</h1>
      <div className="wb-meta">
        {asOf && <span className="wb-chip">{t("As of")} {new Date(asOf).toLocaleString(loc, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })}</span>}
        {items && <span className="wb-chip ghost">{t("{n} holdings", { n: held.length })}</span>}
      </div>
      <div className="fin-head-end">
        <button className="fin-icon" onClick={load} disabled={busy} title={t("Refresh")} aria-label={t("Refresh")}>
          <RotateCw size={15} className={busy ? "spin" : ""} />
        </button>
        <div className="wb-seg" role="tablist" aria-label={t("Range")}>
          <button role="tab" aria-selected className="on">{t("1M")}</button>
        </div>
      </div>
    </header>
  );

  if (!items && !err) return (
    <div className="wb-view fin-view" aria-busy="true">{head}<SkeletonCards n={3} h={110} /></div>
  );
  if (err && !items) return (
    <div className="wb-view fin-view">
      {head}
      <div className="wb-empty">
        <div className="wb-empty-title">{t("Finance is not connected")}</div>
        <ol className="wb-steps"><li>{t("Settings › MCP › add finance")}</li></ol>
        <div className="fin-err">{err}</div>
      </div>
    </div>
  );
  if (!held.length && !totals.length) return (
    <div className="wb-view fin-view">
      {head}
      <div className="wb-empty"><div className="wb-empty-title">{t("No holdings yet")}</div></div>
    </div>
  );

  const p = { t, loc };
  return (
    <div className="wb-view fin-view">
      {head}
      {err && <div className="wb-error">{err}</div>}

      <div className="fin-sums">
        {totals.map((s) => <SummaryCard key={s.currency} s={s} {...p} />)}
      </div>

      <div className="wb-cols">
        <div className="wb-col main">
          <HoldingsCard held={held} quotes={quotes} sel={sel} onSel={(id) => setSel((cur) => (cur === id ? null : id))} multi={totals.length > 1} {...p} />
        </div>
        <div className="wb-col side">
          {selected && <Detail key={selected.id} h={selected} q={quotes[quoteKey(selected)]} onClose={() => setSel(null)} {...p} />}
          {plans.length > 0 && <PlansCard plans={plans} onSel={setSel} {...p} />}
          <NewsCard news={news} topics={topics} {...p} />
        </div>
      </div>
    </div>
  );
}

type P = { t: T; loc?: string };

function Card({ title, aside, className, children }: { title: React.ReactNode; aside?: React.ReactNode; className?: string; children: React.ReactNode }) {
  return (
    <section className={`wb-card ${className ?? ""}`}>
      <header className="wb-card-head"><h2>{title}</h2>{aside && <div className="wb-card-aside">{aside}</div>}</header>
      {children}
    </section>
  );
}

function Fig({ label, value, sub, cls }: { label: string; value: string; sub?: React.ReactNode; cls?: string }) {
  return (
    <div className="fin-fig">
      <span className="wb-k">{label}</span>
      <b className={cls}>{value}</b>
      {sub && <span className={`fin-fig-sub ${cls ?? ""}`}>{sub}</span>}
    </div>
  );
}

const ALLOC = ["a1", "a2", "a3", "a4", "a5", "a6"];

// ── Per currency ────────────────────────────────────────────────────────

function SummaryCard({ s, t, loc }: { s: Totals } & P) {
  const c = s.currency;
  const prev = s.value - s.day;
  const dayF = prev > 0 ? s.day / prev : undefined;
  const unrealF = s.cost > 0 ? s.unreal / s.cost : undefined;
  const parts = [...s.held].sort((a, b) => amt(b.position?.marketValue) - amt(a.position?.marketValue));
  return (
    <Card className="fin-sum" title={<span className="fin-cur">{c}</span>} aside={<Pill tone={tone(s.day)}>{t("Today")} {sPct(dayF)}</Pill>}>
      <div className="fin-sum-top">
        <div className="fin-fig">
          <span className="wb-k">{t("Market value")}</span>
          <Big value={money(s.value, c, loc)} size="m" />
        </div>
      </div>
      {parts.length > 1 && (
        <div className="fin-alloc">
          <Stack height={6} parts={parts.map((h, i) => ({ key: h.id, value: amt(h.position?.marketValue), cls: `fin-${ALLOC[i % ALLOC.length]}`, title: `${h.symbol} ${money(amt(h.position?.marketValue), c, loc)}` }))} />
          <ul className="fin-alloc-legend">
            {parts.slice(0, 6).map((h, i) => (
              <li key={h.id}><i className={`fin-${ALLOC[i % ALLOC.length]}`} />{h.symbol}<em>{s.value > 0 ? `${Math.round((amt(h.position?.marketValue) / s.value) * 100)}%` : ""}</em></li>
            ))}
          </ul>
        </div>
      )}
      <div className="fin-figs">
        <Fig label={t("Cost")} value={money(s.cost, c, loc)} />
        <Fig label={t("Unrealized")} value={sMoney(s.unreal, c, loc)} sub={sPct(unrealF)} cls={`t-${tone(s.unreal)}`} />
        <Fig label={t("Today")} value={sMoney(s.day, c, loc)} sub={sPct(dayF)} cls={`t-${tone(s.day)}`} />
        <Fig label={t("Realized")} value={sMoney(s.real, c, loc)} cls={`t-${tone(s.real)}`} />
      </div>
    </Card>
  );
}

// ── Holdings ────────────────────────────────────────────────────────────

function HoldingsCard({ held, quotes, sel, onSel, multi, t, loc }: { held: Holding[]; quotes: Record<string, Quote>; sel: string | null; onSel: (id: string) => void; multi: boolean } & P) {
  let lastCur = "";
  return (
    <Card title={t("Holdings")} className="fin-hold">
      <div className="fin-table" role="table">
        <div className="fin-tr fin-th" role="row">
          <span>{t("Name")}</span><span className="fin-c-spark">{t("1M")}</span><span>{t("Price")}</span><span>{t("Today")}</span>
          <span>{t("Shares")}</span><span>{t("Value")}</span><span>{t("Gain")}</span>
        </div>
        {held.map((h) => {
          const q = quotes[quoteKey(h)];
          const pos = h.position ?? {};
          const c = h.currency;
          const d = dayPct(h, q);
          const g = amt(pos.unrealizedPnl);
          const closes = q?.closes ?? [];
          const trend = closes.length > 1 ? closes[closes.length - 1] - closes[0] : 0;
          const group = multi && c !== lastCur ? c : "";
          lastCur = c;
          return (
            <React.Fragment key={h.id}>
              {group && <div className="fin-group" role="row"><span>{group}</span></div>}
              <button role="row" className={`fin-tr fin-row ${sel === h.id ? "on" : ""}`} onClick={() => onSel(h.id)} aria-pressed={sel === h.id}>
                <span className="fin-name"><b>{h.name}</b><em>{h.symbol}</em></span>
                <span className="fin-c-spark">{closes.length > 1 ? <Spark points={closes} tone={tone(trend)} w={88} h={26} /> : <i className="fin-dash">––</i>}</span>
                <span className="fin-n">{h.price ? money(amt(h.price), c, loc) : "––"}</span>
                <span className={`fin-n t-${tone(d ?? 0)}`}>{sPct(d)}</span>
                <span className="fin-n dim">{qty(pos.shares, loc)}</span>
                <span className="fin-n">{money(amt(pos.marketValue), c, loc)}</span>
                <span className={`fin-n fin-gain t-${tone(g)}`}><b>{sMoney(g, c, loc)}</b><em>{sPct(pos.unrealizedPercent)}</em></span>
              </button>
            </React.Fragment>
          );
        })}
      </div>
    </Card>
  );
}

// ── One holding ─────────────────────────────────────────────────────────

function Detail({ h, q, onClose, t, loc }: { h: Holding; q?: Quote; onClose: () => void } & P) {
  const [news, setNews] = useState<Article[] | null>(null);
  const [topics, setTopics] = useState<Topic[] | null>(null);
  useEffect(() => {
    FinanceNews(h.symbol, 5).then((n) => setNews(((n as { items?: Article[] })?.items) ?? [])).catch(() => setNews([]));
    FinanceTopics(h.symbol, "7d", 3).then((x) => setTopics(((x as { topics?: Topic[] })?.topics) ?? [])).catch(() => setTopics([]));
  }, [h.symbol]);

  const c = h.currency, pos = h.position ?? {};
  const closes = q?.closes ?? [];
  const d = dayPct(h, q);
  const avg = amt(pos.avgCost);
  const hi = closes.length ? Math.max(...closes) : undefined, lo = closes.length ? Math.min(...closes) : undefined;
  const m1 = closes.length > 1 ? closes[closes.length - 1] / closes[0] - 1 : undefined;

  return (
    <Card className="fin-detail" title={<span className="fin-dt"><b>{h.name}</b><em>{h.symbol}</em></span>}
      aside={<button className="fin-icon" onClick={onClose} title={t("Close")} aria-label={t("Close")}><X size={15} /></button>}>
      <div className="fin-price">
        <Big value={h.price ? money(amt(h.price), c, loc) : "––"} size="l" />
        <Pill tone={tone(d ?? 0)}>{sPct(d)}</Pill>
        {m1 !== undefined && <span className={`fin-m1 t-${tone(m1)}`}>{t("1M")} {sPct(m1)}</span>}
      </div>
      {closes.length > 1 && (
        <div className="fin-chart">
          <Chart height={170} deps={[closes.join(","), avg, c]} build={(k) => {
            const up = closes[closes.length - 1] >= closes[0];
            const col = up ? k.green : k.red;
            const mono = "Geist Mono Variable, ui-monospace, monospace";
            const n = closes.length;
            return {
              backgroundColor: "transparent", animationDuration: 350,
              grid: { left: 6, right: 52, top: 10, bottom: 18 },
              tooltip: {
                trigger: "axis", confine: true, backgroundColor: k.pop, borderColor: k.line, borderWidth: 1, padding: [5, 8],
                textStyle: { color: k.ink, fontSize: 12, fontFamily: mono },
                axisPointer: { type: "line", lineStyle: { color: k.ink3, width: 1, type: "dashed" } },
                formatter: (ps: any) => { const p0 = Array.isArray(ps) ? ps[0] : ps; return `<b>${money(Number(p0.value), c, loc)}</b>`; },
              },
              xAxis: {
                type: "category", boundaryGap: false, data: closes.map((_, i) => String(i - n + 1)),
                axisLine: { lineStyle: { color: k.line } }, axisTick: { show: false },
                axisLabel: { color: k.ink3, fontFamily: mono, fontSize: 10, alignMinLabel: "left", alignMaxLabel: "right", interval: (i: number) => i === 0 || i === n - 1, formatter: (_: string, i: number) => (i === n - 1 ? t("Today") : t("{n} sessions ago", { n: n - 1 })) },
              },
              yAxis: { type: "value", scale: true, position: "right", splitNumber: 3, splitLine: { lineStyle: { color: k.line } }, axisLabel: { color: k.ink3, fontFamily: mono, fontSize: 10 } },
              series: [{
                type: "line", data: closes, showSymbol: false, smooth: 0.2, lineStyle: { color: col, width: 1.6 },
                areaStyle: { color: { type: "linear", x: 0, y: 0, x2: 0, y2: 1, colorStops: [{ offset: 0, color: col + "33" }, { offset: 1, color: col + "00" }] } as any },
                markLine: avg > 0 ? {
                  symbol: "none", silent: true, lineStyle: { color: k.blue, type: "dashed", width: 1 },
                  label: { formatter: t("Avg cost"), color: k.blue, fontSize: 10, fontFamily: mono, position: "insideEndTop" },
                  data: [{ yAxis: avg }],
                } : undefined,
              }],
            };
          }} />
          <div className="fin-hilo"><span>{t("High")} <b>{money(hi ?? 0, c, loc)}</b></span><span>{t("Low")} <b>{money(lo ?? 0, c, loc)}</b></span></div>
        </div>
      )}

      <div className="fin-figs">
        <Fig label={t("Shares")} value={qty(pos.shares, loc)} />
        <Fig label={t("Avg cost")} value={money(avg, c, loc)} />
        <Fig label={t("Cost")} value={money(amt(pos.costBasis), c, loc)} />
        <Fig label={t("Market value")} value={money(amt(pos.marketValue), c, loc)} />
        <Fig label={t("Unrealized")} value={sMoney(amt(pos.unrealizedPnl), c, loc)} sub={sPct(pos.unrealizedPercent)} cls={`t-${tone(amt(pos.unrealizedPnl))}`} />
        <Fig label={t("Today")} value={sMoney(amt(h.dayChange), c, loc)} sub={sPct(d)} cls={`t-${tone(amt(h.dayChange))}`} />
        <Fig label={t("Realized")} value={sMoney(amt(pos.realizedPnl), c, loc)} cls={`t-${tone(amt(pos.realizedPnl))}`} />
        <Fig label={t("Trades")} value={String(pos.trades ?? 0)} />
      </div>

      {h.plan?.active && <PlanBlock h={h} t={t} loc={loc} />}
      {h.notes && <div className="fin-notes">{h.notes}</div>}

      <div className="fin-sub">
        <div className="fin-subhead"><span className="wb-k">{t("News")}</span></div>
        {news === null ? <div className="fin-dim">…</div> : news.length === 0 ? <div className="fin-dim">{t("No news")}</div> : <NewsList items={news} t={t} compact />}
      </div>
      {topics && topics.length > 0 && (
        <div className="fin-sub">
          <div className="fin-subhead"><span className="wb-k">{t("Topics")}</span></div>
          <TopicList items={topics.map((topic) => ({ symbol: h.symbol, topic }))} t={t} full />
        </div>
      )}
      <div className="fin-actions">
        <button className="fin-btn" onClick={askToCompose}><MessageSquare size={14} />{t("Ask the queen")}</button>
      </div>
    </Card>
  );
}

function PlanBlock({ h, t, loc }: { h: Holding } & P) {
  const pl = h.plan!;
  const c = pl.amount?.currency || h.currency;
  return (
    <div className="fin-plan">
      <div className="fin-subhead">
        <span className="wb-k"><CalendarClock size={13} />{t("Monthly plan")}</span>
        {pl.doneThisMonth ? <Pill tone="good" dot>{t("Done this month")}</Pill> : <Pill tone="fair" dot>{t("Due")}</Pill>}
      </div>
      <div className="fin-figs three">
        <Fig label={t("Amount")} value={money(amt(pl.amount), c, loc)} />
        <Fig label={t("Buy on")} value={pl.day ? `${t("Day {n}", { n: pl.day })}${pl.time ? ` · ${pl.time}` : ""}` : "––"} />
        <Fig label={t("Next")} value={pl.nextTime ? new Date(pl.nextTime).toLocaleDateString(loc, { month: "short", day: "numeric" }) : "––"} />
        <Fig label={t("Invested this month")} value={money(amt(pl.investedThisMonth), c, loc)} />
        <Fig label={t("Streak")} value={t("{n} mo", { n: pl.streakMonths ?? 0 })} />
      </div>
    </div>
  );
}

// ── Plans due this month ────────────────────────────────────────────────

function PlansCard({ plans, onSel, t, loc }: { plans: Holding[]; onSel: (id: string) => void } & P) {
  const due = plans.filter((h) => !h.plan?.doneThisMonth).length;
  return (
    <Card title={t("Monthly plans")} aside={due > 0 ? <Pill tone="fair">{t("{n} due", { n: due })}</Pill> : <Pill tone="good">{t("All done")}</Pill>}>
      <ul className="fin-plans">
        {plans.map((h) => {
          const pl = h.plan!;
          const c = pl.amount?.currency || h.currency;
          return (
            <li key={h.id}>
              <button onClick={() => onSel(h.id)}>
                <span className={`wb-ico ${pl.doneThisMonth ? "t-good" : "t-fair"}`}>{pl.doneThisMonth ? <Check size={15} /> : <CalendarClock size={15} />}</span>
                <span className="fin-plan-name"><b>{h.name}</b><em>{h.symbol} · {t("Day {n}", { n: pl.day ?? 0 })}{pl.time ? ` ${pl.time}` : ""}</em></span>
                <span className="fin-plan-n"><b>{money(amt(pl.amount), c, loc)}</b><em>{pl.doneThisMonth ? t("Done this month") : pl.nextTime ? new Date(pl.nextTime).toLocaleDateString(loc, { month: "short", day: "numeric" }) : ""}</em></span>
              </button>
            </li>
          );
        })}
      </ul>
    </Card>
  );
}

// ── News and topics ─────────────────────────────────────────────────────

function NewsList({ items, t, compact }: { items: Article[]; t: T; compact?: boolean }) {
  return (
    <ul className={`fin-news ${compact ? "compact" : ""}`}>
      {items.map((a, i) => (
        <li key={a.url || i}>
          <div className="fin-news-meta">
            <span className="fin-outlet">{a.outlet}</span>
            <span className="fin-time">{ago(t, a.published)}</span>
            {!compact && (a.symbols ?? []).slice(0, 3).map((s) => <span key={s} className="fin-sym">{s}</span>)}
          </div>
          <a href={a.url} onClick={(e) => { e.preventDefault(); openExternal(a.url); }} className="fin-news-title">
            {a.title}<ExternalLink size={12} />
          </a>
        </li>
      ))}
    </ul>
  );
}

function TopicList({ items, t, full }: { items: { symbol: string; topic: Topic }[]; t: T; full?: boolean }) {
  return (
    <ul className="fin-topics">
      {items.map(({ symbol, topic }) => (
        <li key={`${symbol}-${topic.id}`}>
          <div className="fin-news-meta">
            {!full && <span className="fin-sym">{symbol}</span>}
            <span className="fin-time">{ago(t, topic.updated)}</span>
            {(topic.outlets ?? []).length > 0 && <span className="fin-outlet">{(topic.outlets ?? []).slice(0, 3).join(" · ")}{(topic.outlets ?? []).length > 3 ? ` +${(topic.outlets ?? []).length - 3}` : ""}</span>}
          </div>
          <b className="fin-topic-title">{topic.title}</b>
          {topic.summary && <p className="fin-topic-sum">{topic.summary}</p>}
          {full && (topic.viewpoints ?? []).length > 0 && (
            <ul className="fin-views">
              {(topic.viewpoints ?? []).map((v, i) => (
                <li key={i} className={v.disputed ? "disputed" : ""}>
                  <span>{v.text}</span>
                  <em>
                    {(v.outlets ?? []).slice(0, 3).map((o, j) => (
                      <a key={j} href={o.url} onClick={(e) => { e.preventDefault(); openExternal(o.url); }}>{o.name}</a>
                    ))}
                    {v.disputed && <Pill tone="fair">{t("Disputed")}</Pill>}
                  </em>
                </li>
              ))}
            </ul>
          )}
        </li>
      ))}
    </ul>
  );
}

function NewsCard({ news, topics, t }: { news: Article[]; topics: { symbol: string; topic: Topic }[] } & P) {
  const [tab, setTab] = useState<"news" | "topics">("news");
  const showTopics = topics.length > 0;
  const cur = showTopics ? tab : "news";
  if (!news.length && !showTopics) return null;
  return (
    <Card title={t("News")} aside={showTopics && (
      <div className="wb-seg small" role="tablist">
        <button role="tab" aria-selected={cur === "news"} className={cur === "news" ? "on" : ""} onClick={() => setTab("news")}>{t("News")}</button>
        <button role="tab" aria-selected={cur === "topics"} className={cur === "topics" ? "on" : ""} onClick={() => setTab("topics")}>{t("Topics")} <em className="fin-count">{topics.length}</em></button>
      </div>
    )}>
      {cur === "news" ? <NewsList items={news} t={t} /> : <TopicList items={topics} t={t} />}
    </Card>
  );
}
