import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { ChevronRightIcon, ClipboardCheckIcon, ExternalLinkIcon, FolderIcon, FolderOpenIcon, SquareIcon, SwordsIcon } from "lucide-react";
import { useI18n } from "../lib/i18n";
import { EventsOn } from "../../wailsjs/runtime";
import {
  CancelDebate, CancelImplementReview, ContinueDebate, DebateDetail, Debates, ImplementReviewDetail, ImplementReviewDiff,
  ImplementReviews, PickDirectory, StartDebate, StartImplementReview,
} from "../../wailsjs/go/app/App";
import { mdComponents } from "../components/Transcript";
import { parseTime } from "../lib/format";
import { toast } from "../lib/toasts";
import "./debate.css";

// Plain shapes: what arrives over events is JSON, not the generated classes.
export interface Stance { kind: "agree" | "disagree" | "revised"; text: string }
export interface DStep {
  id: string; phase: "position" | "review" | "conclusion"; round: number; agent: string; side: number; run?: string;
  state: string; started: string; ended?: string; in: number; out: number; costUsd: number; text?: string;
  stances?: Stance[]; error?: string; readOnly: boolean;
}
export interface Conclusion {
  verdict: string; agreed: string[]; disagreements: { text: string; who?: string }[]; nextSteps: string[]; raw: string; judge: string;
}
export interface DebateRec {
  id: string; topic: string; cwd: string; agents: string[]; rounds: number; judge: string; judgeAgent: string; state: string;
  steps?: DStep[]; conclusion?: Conclusion; reviews?: string[]; started: string; ended?: string; error?: string;
}
export interface RStep {
  id: string; kind: "implement" | "review" | "fix"; cycle: number; agent: string; run?: string; state: string; started: string; ended?: string;
  in: number; out: number; costUsd: number; text?: string; verdict?: string; findings?: { file?: string; line?: number; text: string }[];
  diff?: { files: number; add: number; del: number }; error?: string;
}
export interface ReviewRec {
  id: string; cwd: string; prompt: string; implementer: string; reviewer: string; cycles: number; debate?: string; fromRun?: string;
  state: string; steps?: RStep[]; started: string; ended?: string; error?: string;
}

const upsert = <T extends { id: string }>(prev: T[], p: T): T[] => {
  const i = prev.findIndex((x) => x.id === p.id);
  if (i < 0) return [p, ...prev];
  const next = [...prev];
  next[i] = p;
  return next;
};

/** Debates and implement-review tracks, kept current by their events. */
export function useDebates() {
  const [debates, setDebates] = useState<DebateRec[]>([]);
  const [reviews, setReviews] = useState<ReviewRec[]>([]);
  useEffect(() => {
    let alive = true;
    Debates().then((l) => alive && setDebates((prev) => {
      const have = new Map(prev.map((d) => [d.id, d]));
      return ((l ?? []) as unknown as DebateRec[]).map((d) => (have.get(d.id)?.steps ? have.get(d.id)! : d));
    })).catch(() => {});
    ImplementReviews().then((l) => alive && setReviews((prev) => {
      const have = new Map(prev.map((d) => [d.id, d]));
      return ((l ?? []) as unknown as ReviewRec[]).map((d) => (have.get(d.id)?.steps ? have.get(d.id)! : d));
    })).catch(() => {});
    const a = EventsOn("debate:update", (p: DebateRec) => setDebates((prev) => upsert(prev, p)));
    const b = EventsOn("review:update", (p: ReviewRec) => setReviews((prev) => upsert(prev, p)));
    return () => { alive = false; a(); b(); };
  }, []);
  // What a call returns can be older than what the events already said (a
  // debate's first steps are pushed before its start call answers): never
  // replace a record with one that has fewer steps.
  const putDebate = useCallback((d: DebateRec) => setDebates((prev) => ((prev.find((x) => x.id === d.id)?.steps?.length ?? 0) > (d.steps?.length ?? 0) ? prev : upsert(prev, d))), []);
  const putReview = useCallback((r: ReviewRec) => setReviews((prev) => ((prev.find((x) => x.id === r.id)?.steps?.length ?? 0) > (r.steps?.length ?? 0) ? prev : upsert(prev, r))), []);
  return { debates, reviews, putDebate, putReview };
}

const STATE: Record<string, string> = {
  running: "Running", done: "Done", failed: "Failed", cancelled: "Cancelled", approved: "Approved", changes: "Changes", pending: "Running",
};
const TONE: Record<string, string> = {
  running: "live", done: "ok", approved: "ok", failed: "bad", changes: "warn", cancelled: "mute", pending: "mute",
};

function Chip({ state }: { state: string }) {
  const { t } = useI18n();
  return <span className={`db-chip ${TONE[state] ?? "mute"}`}>{state === "running" && <i className="db-pulse" />}{t(STATE[state] ?? state)}</span>;
}

/** Re-renders each second while something is running, so elapsed time moves. */
function useNow(active: boolean) {
  const [, tick] = useState(0);
  useEffect(() => {
    if (!active) return;
    const h = window.setInterval(() => tick((n) => n + 1), 1000);
    return () => window.clearInterval(h);
  }, [active]);
  return Date.now();
}

const secs = (a?: string, b?: string, now = Date.now()) => {
  const s = parseTime(a ?? "");
  if (!s) return 0;
  const e = b ? parseTime(b) : null;
  return Math.max(0, ((e ? +e : now) - +s) / 1000);
};
const dur = (s: number) => (s >= 60 ? `${Math.floor(s / 60)}m ${Math.round(s % 60)}s` : `${s.toFixed(s < 10 ? 1 : 0)}s`);
const tok = (n: number) => (n >= 1000 ? `${(n / 1000).toFixed(n >= 10000 ? 0 : 1)}k` : String(n));
const base = (p: string) => p.split("/").filter(Boolean).slice(-1)[0] ?? p;
const other = (a: string) => (a === "codex" ? "claude" : "codex");

function Figures({ state, started, ended, tin, tout }: { state: string; started: string; ended?: string; tin: number; tout: number }) {
  const now = useNow(state === "running");
  const s = secs(started, ended, now);
  return (
    <span className="db-fig">
      {s > 0 && <span>{dur(s)}</span>}
      {tin + tout > 0 && <span>{tok(tin)} in</span>}
      {tin + tout > 0 && <span>{tok(tout)} out</span>}
    </span>
  );
}

function Md({ text }: { text: string }) {
  return <div className="db-md md"><ReactMarkdown remarkPlugins={[remarkGfm]} components={mdComponents}>{text}</ReactMarkdown></div>;
}

// ---- the form ----

export function DebateForm({ choices, cwds, onStarted }: { choices: string[]; cwds: string[]; onStarted: (d: DebateRec) => void }) {
  const { t } = useI18n();
  const local = choices.filter((c) => !c.includes("."));
  const [cwd, setCwd] = useState(cwds[0] ?? "");
  const [topic, setTopic] = useState("");
  const [a, setA] = useState(local.includes("claude") ? "claude" : local[0] ?? "");
  const [b, setB] = useState(local.includes("codex") ? "codex" : local[1] ?? local[0] ?? "");
  const [rounds, setRounds] = useState(2);
  const [judge, setJudge] = useState("queen");
  const [busy, setBusy] = useState(false);
  const canPick = typeof window !== "undefined" && !!(window as any).runtime;
  // Choices arrive after the first paint.
  useEffect(() => {
    if (!a && local[0]) setA(local.includes("claude") ? "claude" : local[0]);
    if (!b && local[0]) setB(local.includes("codex") ? "codex" : local[1] ?? local[0]);
  }, [local.join(" ")]); // eslint-disable-line react-hooks/exhaustive-deps
  const go = async () => {
    if (!topic.trim() || !a || !b || busy) return;
    setBusy(true);
    try {
      onStarted((await StartDebate({ topic: topic.trim(), cwd, agents: [a, b], models: [], rounds, judge } as any)) as unknown as DebateRec);
    } catch (e) {
      toast.error(String(e));
    } finally {
      setBusy(false);
    }
  };
  const pickFolder = async () => {
    try { const d = await PickDirectory(cwd); if (d) setCwd(d); } catch { /* dismissed */ }
  };
  const agentSelect = (v: string, set: (s: string) => void, label: string) => (
    <select className="db-sel" aria-label={label} value={v} onChange={(e) => set(e.target.value)}>
      {local.map((n) => <option key={n} value={n}>{n}</option>)}
    </select>
  );
  return (
    <div className="cd-start">
      <div className="cd-start-in db-form">
        <h2><SwordsIcon size={20} /> {t("Debate")}</h2>
        <div className="db-card">
          <label className="db-f">
            <span>{t("Project")}</span>
            <span className="db-pathbox">
              {canPick ? (
                <button type="button" className="cd-pick" aria-label={t("Choose a folder")} title={t("Choose a folder")} onClick={() => void pickFolder()}><FolderOpenIcon size={14} /></button>
              ) : <FolderIcon size={14} />}
              <input list="db-cwds" value={cwd} onChange={(e) => setCwd(e.target.value)} placeholder={t("workspace")} spellCheck={false} aria-label={t("Project")} />
              <datalist id="db-cwds">{cwds.map((c) => <option key={c} value={c} />)}</datalist>
            </span>
          </label>
          <label className="db-f">
            <span>{t("Question")}</span>
            <textarea value={topic} onChange={(e) => setTopic(e.target.value)} rows={4} aria-label={t("Question")}
              onKeyDown={(e) => { if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); void go(); } }} />
          </label>
          <div className="db-f">
            <span>{t("Agents")}</span>
            <span className="db-row">
              {agentSelect(a, setA, `${t("Agent")} 1`)}
              <i className="db-vs">vs</i>
              {agentSelect(b, setB, `${t("Agent")} 2`)}
            </span>
          </div>
          <div className="db-f">
            <span>{t("Debate rounds")}</span>
            <span className="db-seg" role="group" aria-label={t("Debate rounds")}>
              {[1, 2, 3].map((n) => <button key={n} type="button" className={rounds === n ? "on" : ""} onClick={() => setRounds(n)}>{n}</button>)}
            </span>
          </div>
          <div className="db-f">
            <span>{t("Judge")}</span>
            <select className="db-sel" aria-label={t("Judge")} value={judge} onChange={(e) => setJudge(e.target.value)}>
              <option value="queen">{t("Queen")}</option>
              {local.map((n) => <option key={n} value={n}>{n}</option>)}
            </select>
          </div>
          <div className="db-actions">
            <button className="db-go" onClick={() => void go()} disabled={!topic.trim() || !a || !b || busy}>{t("Start")}</button>
          </div>
        </div>
      </div>
    </div>
  );
}

// ---- a debate ----

function StepCard({ step, onRun }: { step: DStep; onRun: (id: string) => void }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const has = !!step.text?.trim();
  return (
    <div className={`db-step ${step.state}`} data-testid="debate-step">
      <div className="db-step-h">
        <button className="db-fold" onClick={() => setOpen((v) => !v)} disabled={!has} aria-expanded={open} aria-label={step.agent}>
          <ChevronRightIcon size={13} className={open ? "rot" : ""} style={{ opacity: has ? 1 : 0.25 }} />
          <b>{step.agent}</b>
        </button>
        <Chip state={step.state} />
        <Figures state={step.state} started={step.started} ended={step.ended} tin={step.in} tout={step.out} />
        {step.run && <button className="db-link" onClick={() => onRun(step.run!)} title={t("Open")} aria-label={t("Open")}><ExternalLinkIcon size={12} /></button>}
      </div>
      {(step.stances?.length ?? 0) > 0 && (
        <div className="db-chips">
          {step.stances!.map((s, i) => <span key={i} className={`db-st ${s.kind}`} title={s.text}><b>{s.kind === "agree" ? "AGREE" : s.kind === "disagree" ? "DISAGREE" : "REVISED"}</b>{s.text}</span>)}
        </div>
      )}
      {step.error && <div className="db-err">{step.error}</div>}
      {open && has && <Md text={step.text!} />}
    </div>
  );
}

function Conclusion({ d, choices, onReview }: { d: DebateRec; choices: string[]; onReview: (r: ReviewRec) => void }) {
  const { t } = useI18n();
  const c = d.conclusion!;
  const local = choices.filter((x) => !x.includes("."));
  const [impl, setImpl] = useState(d.agents[0] ?? "claude");
  const [cycles, setCycles] = useState(2);
  const [busy, setBusy] = useState(false);
  const run = async () => {
    setBusy(true);
    try {
      const prompt = `Implement the following conclusion in this project. Make the changes it calls for and nothing else.\n\nQuestion: ${d.topic}\n\n${c.raw}`;
      onReview((await StartImplementReview({ cwd: d.cwd, prompt, implementer: impl, reviewer: other(impl), cycles, debate: d.id, fromRun: "" } as any)) as unknown as ReviewRec);
    } catch (e) {
      toast.error(String(e));
    } finally {
      setBusy(false);
    }
  };
  const cont = async () => {
    try { await ContinueDebate(d.id); } catch (e) { toast.error(String(e)); }
  };
  return (
    <section className="db-concl" data-testid="debate-conclusion">
      <h3>{t("Conclusion")} <span className="db-by">{c.judge}</span></h3>
      {c.verdict && <div className="db-verdict"><Md text={c.verdict} /></div>}
      <div className="db-concl-grid">
        {c.agreed.length > 0 && (
          <div>
            <h4 className="ok">{t("Agreed")}</h4>
            <ul>{c.agreed.map((x, i) => <li key={i}><ReactMarkdown remarkPlugins={[remarkGfm]} components={mdComponents}>{x}</ReactMarkdown></li>)}</ul>
          </div>
        )}
        {c.disagreements.length > 0 && (
          <div>
            <h4 className="bad">{t("Disagreements")}</h4>
            <ul>{c.disagreements.map((x, i) => (
              <li key={i}>
                {x.who && <span className={`db-who${x.who === "unresolved" ? " un" : ""}`}>{x.who === "unresolved" ? t("Unresolved") : x.who}</span>}
                <ReactMarkdown remarkPlugins={[remarkGfm]} components={mdComponents}>{x.text}</ReactMarkdown>
              </li>
            ))}</ul>
          </div>
        )}
        {c.nextSteps.length > 0 && (
          <div>
            <h4>{t("Next steps")}</h4>
            <ol>{c.nextSteps.map((x, i) => <li key={i}><ReactMarkdown remarkPlugins={[remarkGfm]} components={mdComponents}>{x}</ReactMarkdown></li>)}</ol>
          </div>
        )}
      </div>
      <div className="db-concl-f">
        <select className="db-sel sm" aria-label={t("Implement")} value={impl} onChange={(e) => setImpl(e.target.value)}>
          {local.map((n) => <option key={n} value={n}>{n}</option>)}
        </select>
        <select className="db-sel sm" aria-label={t("Cycles")} value={cycles} onChange={(e) => setCycles(+e.target.value)}>
          {[0, 1, 2, 3].map((n) => <option key={n} value={n}>{n}</option>)}
        </select>
        <button className="db-go" onClick={() => void run()} disabled={busy || d.state === "running"}>{t("Run the conclusion")}</button>
        <button className="db-ghost" onClick={() => void cont()} disabled={d.state === "running"}>{t("Continue debate")}</button>
      </div>
    </section>
  );
}

export function DebateView({ debate, reviews, choices, onDetail, onRun, onReview, onReviewDetail }: {
  debate: DebateRec; reviews: ReviewRec[]; choices: string[];
  onDetail: (d: DebateRec) => void; onRun: (id: string) => void; onReview: (r: ReviewRec) => void; onReviewDetail: (r: ReviewRec) => void;
}) {
  const { t } = useI18n();
  const asked = useRef("");
  useEffect(() => {
    if (debate.steps || asked.current === debate.id) return;
    asked.current = debate.id;
    DebateDetail(debate.id).then((d) => onDetail(d as unknown as DebateRec)).catch(() => {});
  }, [debate.id, debate.steps, onDetail]);
  const steps = debate.steps ?? [];
  const phases = useMemo(() => {
    const out: { key: string; label: string; round: number; cells: (DStep | undefined)[] }[] = [];
    for (const s of steps) {
      if (s.phase === "conclusion") continue;
      const key = `${s.phase}-${s.round}`;
      let row = out.find((r) => r.key === key);
      if (!row) {
        row = { key, label: s.phase === "position" ? t("Position") : t("Round {n}", { n: s.round }), round: s.round, cells: [undefined, undefined] };
        out.push(row);
      }
      row.cells[s.side === 1 ? 1 : 0] = s;
    }
    return out;
  }, [steps, t]);
  const judges = steps.filter((s) => s.phase === "conclusion");
  const judge = judges[judges.length - 1];
  const mine = reviews.filter((r) => r.debate === debate.id);
  const total = steps.reduce((n, s) => n + s.costUsd, 0);
  const running = debate.state === "running";
  return (
    <div className="cd-thread">
      <div className="cd-thread-h">
        <SwordsIcon size={15} />
        <b className="db-topic" title={debate.topic}>{debate.topic}</b>
        <span className="cd-path" title={debate.cwd}>{debate.cwd}</span>
        <Chip state={debate.state} />
        {total > 0 && <span className="cd-meta">${total.toFixed(total < 1 ? 3 : 2)}</span>}
        {running && (
          <button className="db-stop" onClick={() => CancelDebate(debate.id)} aria-label={t("Cancel")} title={t("Cancel")}><SquareIcon size={11} fill="currentColor" /></button>
        )}
      </div>
      <div className="cd-log">
        <div className="db-in">
          {debate.conclusion && <Conclusion d={debate} choices={choices} onReview={onReview} />}
          {debate.error && !running && <div className="db-err">{debate.error}</div>}
          <div className="db-cols db-cols-h">
            {debate.agents.map((a, i) => <div key={i} className="db-col-h"><b>{a}</b></div>)}
          </div>
          {phases.map((p) => (
            <div key={p.key} className="db-phase">
              <div className="db-phase-l">{p.label}</div>
              <div className="db-cols">
                {[0, 1].map((i) => p.cells[i] ? <StepCard key={p.cells[i]!.id} step={p.cells[i]!} onRun={onRun} /> : <div key={i} className="db-step empty" />)}
              </div>
            </div>
          ))}
          {judge && (
            <div className="db-phase">
              <div className="db-phase-l">{t("Judge")}</div>
              <div className="db-cols one"><StepCard step={judge} onRun={onRun} /></div>
            </div>
          )}
          {mine.map((r) => <ReviewTrack key={r.id} review={r} onDetail={onReviewDetail} onRun={onRun} />)}
        </div>
      </div>
    </div>
  );
}

// ---- implement -> review ----

function DiffModal({ id, onClose }: { id: string; onClose: () => void }) {
  const [text, setText] = useState<string | null>(null);
  const [err, setErr] = useState("");
  useEffect(() => {
    ImplementReviewDiff(id).then(setText).catch((e) => setErr(String(e)));
    const esc = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    window.addEventListener("keydown", esc);
    return () => window.removeEventListener("keydown", esc);
  }, [id, onClose]);
  return createPortal(
    <div className="db-diff-wrap" role="dialog" aria-modal="true" onClick={onClose}>
      <pre className="db-diff" onClick={(e) => e.stopPropagation()}>
        {err && <span className="del">{err}</span>}
        {(text ?? "").split("\n").map((l, i) => (
          <span key={i} className={l.startsWith("+++") || l.startsWith("---") || l.startsWith("diff ") ? "meta" : l.startsWith("@@") ? "hunk" : l.startsWith("+") ? "add" : l.startsWith("-") ? "del" : ""}>{l + "\n"}</span>
        ))}
      </pre>
    </div>,
    document.body,
  );
}

export function ReviewTrack({ review, onDetail, onRun }: { review: ReviewRec; onDetail: (r: ReviewRec) => void; onRun: (id: string) => void }) {
  const { t } = useI18n();
  const asked = useRef("");
  const [diff, setDiff] = useState(false);
  useEffect(() => {
    if (review.steps || asked.current === review.id) return;
    asked.current = review.id;
    ImplementReviewDetail(review.id).then((r) => onDetail(r as unknown as ReviewRec)).catch(() => {});
  }, [review.id, review.steps, onDetail]);
  const steps = review.steps ?? [];
  const running = review.state === "running";
  return (
    <section className="db-track" data-testid="review-track">
      <div className="db-track-h">
        <ClipboardCheckIcon size={15} />
        <b>{t("Implement")} · {t("Review")}</b>
        <span className="db-by">{review.implementer} → {review.reviewer}</span>
        <Chip state={review.state} />
        <span className="cd-grow" />
        {running && <button className="db-stop" onClick={() => CancelImplementReview(review.id)} aria-label={t("Cancel")} title={t("Cancel")}><SquareIcon size={11} fill="currentColor" /></button>}
        {!running && steps.some((s) => s.diff) && <button className="db-ghost" onClick={() => setDiff(true)}>{t("View diff")}</button>}
      </div>
      <ol className="db-rsteps">
        {steps.map((s) => (
          <li key={s.id} className={`db-rs ${s.kind} ${s.state}`}>
            <i className="db-rs-dot" />
            <div className="db-rs-b">
              <div className="db-rs-h">
                <b>{s.kind === "review" ? t("Review") : t("Implement")}</b>
                <span className="db-by">{s.agent}{s.cycle > 0 ? ` · #${s.cycle}` : ""}</span>
                {s.kind === "review" && s.state === "done" && s.verdict && (
                  <span className={`db-chip ${s.verdict === "APPROVE" ? "ok" : "warn"}`}>{s.verdict === "APPROVE" ? t("Approved") : t("Changes")}</span>
                )}
                {s.state !== "done" && <Chip state={s.state} />}
                <Figures state={s.state} started={s.started} ended={s.ended} tin={s.in} tout={s.out} />
                {s.diff && <span className="db-fig"><span>{t("{n} files", { n: s.diff.files })} · <em className="add">+{s.diff.add}</em> <em className="del">−{s.diff.del}</em></span></span>}
                {s.run && <button className="db-link" onClick={() => onRun(s.run!)} title={t("Open")} aria-label={t("Open")}><ExternalLinkIcon size={12} /></button>}
              </div>
              {s.error && <div className="db-err">{s.error}</div>}
              {(s.findings?.length ?? 0) > 0 && (
                <ul className="db-findings">
                  {s.findings!.map((f, i) => (
                    <li key={i}>{f.file && <code>{f.file}{f.line ? `:${f.line}` : ""}</code>}<span>{f.text}</span></li>
                  ))}
                </ul>
              )}
            </div>
          </li>
        ))}
      </ol>
      {review.error && !running && <div className="db-err">{review.error}</div>}
      {diff && <DiffModal id={review.id} onClose={() => setDiff(false)} />}
    </section>
  );
}

/** A track opened on its own, from the list. */
export function ReviewView({ review, onDetail, onRun }: { review: ReviewRec; onDetail: (r: ReviewRec) => void; onRun: (id: string) => void }) {
  return (
    <div className="cd-thread">
      <div className="cd-thread-h">
        <ClipboardCheckIcon size={15} />
        <b className="db-topic" title={review.prompt}>{review.prompt}</b>
        <span className="cd-path" title={review.cwd}>{base(review.cwd)}</span>
      </div>
      <div className="cd-log"><div className="db-in"><ReviewTrack review={review} onDetail={onDetail} onRun={onRun} /></div></div>
    </div>
  );
}

/** "Review with <other agent>" on a finished run, with how many fixes it may send back. */
export function ReviewWith({ agent, fromRun, onStarted }: { agent: string; fromRun: string; onStarted: (r: ReviewRec) => void }) {
  const { t } = useI18n();
  const [cycles, setCycles] = useState(2);
  const [busy, setBusy] = useState(false);
  const reviewer = other(agent);
  return (
    <span className="db-with">
      <select className="db-sel sm" aria-label={t("Cycles")} value={cycles} onChange={(e) => setCycles(+e.target.value)}>
        {[0, 1, 2, 3].map((n) => <option key={n} value={n}>{n}</option>)}
      </select>
      <button className="db-ghost" disabled={busy} onClick={async () => {
        setBusy(true);
        try {
          onStarted((await StartImplementReview({ fromRun, reviewer, cycles, cwd: "", prompt: "", implementer: "", debate: "" } as any)) as unknown as ReviewRec);
        } catch (e) { toast.error(String(e)); } finally { setBusy(false); }
      }}>{t("Review with {agent}", { agent: reviewer })}</button>
    </span>
  );
}
