import { translate, useI18n } from "../lib/i18n";
import { useSearchParams, useNavigate } from "react-router-dom";
import { PATHS } from "../lib/routes";
import { focusInWorld } from "../world/bus";
import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { SquareIcon, ChevronRightIcon, PlusIcon, ArrowUpIcon, TerminalIcon, FolderIcon, FolderOpenIcon, PaperclipIcon, SwordsIcon, ClipboardCheckIcon } from "lucide-react";
import { useAttachments } from "../lib/useAttachments";
import AttachmentChips from "../components/AttachmentChips";
import { WorkspaceAbsPath } from "../../wailsjs/go/app/App";
import "./coding.css";
import { createPortal } from "react-dom";
import {
  CLIRunDetail,
  CLIRuns,
  CancelCLIRun,
  ExternalAgentsStatus,
  RemoteAgentNames,
  FollowUpCLIRun,
  StartCLIRun,
  CodingSessions,
  CodingSessionLog,
  ReplyToCodingSession,
  CodingSessionImage,
  PickDirectory,
} from "../../wailsjs/go/app/App";
import { app } from "../../wailsjs/go/models";
import { EventsOn } from "../../wailsjs/runtime";
import { fromNow, parseTime } from "../lib/format";
import { useImeGuard } from "@/lib/ime";
import { toast } from "../lib/toasts";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { mdComponents } from "../components/Transcript";
import BrowserCard from "../components/BrowserCard";
import { BrowserCall, browserOp } from "../lib/browser";
import { SkeletonLines, SkeletonRows } from "../components/Skeleton";
import { DebateForm, DebateView, ReviewTrack, ReviewView, ReviewWith, useDebates, type ReviewRec } from "./DebateParts";

// Plain shapes: what arrives over events is JSON, not the generated classes.
type Ev = Omit<app.CLIRunEvent, "convertValues">;
type Run = Omit<app.CLIRun, "convertValues" | "events"> & { events?: Ev[] };
/** A conversation somebody started in a terminal (CodingSessions). */
type Term = { machine?: string; cli: string; session?: string; source: string; project?: string; state: string; tool?: string; at: string; readable: boolean; replyable: boolean };
const termKey = (t: Term) => `term:${t.machine ?? ""}|${t.cli}|${t.session ?? ""}`;

/** A conversation with one CLI: the first run and its follow-ups. */
interface Thread {
  id: string;
  agent: string;
  runs: Run[]; // oldest first
  latest: Run;
}

function threadsOf(runs: Run[]): Thread[] {
  const by = new Map<string, Run[]>();
  for (const r of runs) {
    const list = by.get(r.thread) ?? [];
    list.push(r);
    by.set(r.thread, list);
  }
  const out: Thread[] = [];
  for (const [id, list] of by) {
    list.sort((a, b) => +parseTime(a.started)! - +parseTime(b.started)!);
    out.push({ id, agent: list[0].agent, runs: list, latest: list[list.length - 1] });
  }
  out.sort((a, b) => +parseTime(b.latest.started)! - +parseTime(a.latest.started)!);
  return out;
}

/** The last two parts of a path: enough to know which project. */
const shortPath = (p: string) => {
  const parts = p.split("/").filter(Boolean);
  return parts.length > 2 ? "…/" + parts.slice(-2).join("/") : p;
};

const cost = (n: number) => (n > 0 ? `$${n.toFixed(n < 1 ? 3 : 2)}` : "");

/** "claude.mac" → { cli: "claude", host: "mac" }; a bare name runs on core. */
const partsOf = (n: string) => {
  const i = n.indexOf(".");
  return i < 0 ? { cli: n, host: "" } : { cli: n.slice(0, i), host: n.slice(i + 1) };
};
const whereOf = (n: string) => partsOf(n).host || "core";

/** The @word the caret is in, if any: same rule as the chat composer. */
function mentionAt(value: string, caret: number): { word: string; at: number } | null {
  const before = value.slice(0, caret);
  const at = before.lastIndexOf("@");
  if (at < 0 || (at > 0 && !/\s/.test(before[at - 1]))) return null;
  const word = before.slice(at + 1);
  return /^[\p{L}\p{N}._-]*$/u.test(word) ? { word, at } : null;
}

/** "@claude.mac fix the tests" → ["claude.mac", "fix the tests"]. */
function addressOf(value: string, names: string[]): [string, string] {
  const m = /^\s*@([\p{L}\p{N}._-]+)\s+/u.exec(value);
  if (m && names.includes(m[1])) return [m[1], value.slice(m[0].length)];
  return ["", value];
}

const LAST = "superleo-coding-agent";
const lastAgent = () => {
  try { return localStorage.getItem(LAST) ?? ""; } catch { return ""; }
};

/**
 * Coding CLIs on core and on every linked agent, driven from here: start one
 * on a task, watch every tool call and what came back, answer its permission
 * prompts, send a follow-up into the same session, stop it. Runs started by
 * "@claude" in a conversation show up here too.
 */
export default function AgentsView() {
  const { t } = useI18n();
  const [params] = useSearchParams();
  const requestedRun = params.get("run") ?? "";
  const [runs, setRuns] = useState<Run[]>([]);
  const [selected, setSelected] = useState<string>("");
  const [events, setEvents] = useState<Record<string, Ev[]>>({});
  // Every coding CLI this core can start: its own ("claude") and each agent's ("claude.mac").
  const [choices, setChoices] = useState<string[] | null>(null);
  // Terminal sessions on every machine, kept current while the page is open.
  const [terms, setTerms] = useState<Term[]>([]);
  useEffect(() => {
    let alive = true;
    const pull = () => CodingSessions().then((l) => { if (alive) setTerms(((l ?? []) as unknown as Term[]).filter((x) => x.source === "terminal")); }).catch(() => {});
    pull();
    const t = window.setInterval(pull, 5000);
    return () => { alive = false; window.clearInterval(t); };
  }, []);

  const load = useCallback(async () => {
    const [list, st, names] = await Promise.all([CLIRuns(), ExternalAgentsStatus(), RemoteAgentNames().catch(() => [])]);
    setRuns(list ?? []);
    const here = (st ?? []).filter((a) => a.installed).map((a) => a.name);
    const there = ((names ?? []) as { name?: string }[]).map((n) => n.name ?? "").filter((n) => n.includes("."));
    setChoices([...here, ...there]);
  }, []);

  useEffect(() => {
    void load().catch(error => { setChoices([]); toast.error(String(error)); });
    const offRun = EventsOn("cli:run", (p: Run) => {
      setRuns((prev) => {
        const i = prev.findIndex((r) => r.id === p.id);
        if (i < 0) return [p, ...prev];
        const next = [...prev];
        next[i] = { ...prev[i], ...p };
        return next;
      });
    });
    const offEv = EventsOn("cli:event", (p: { run: string; event: Ev }) => {
      // A queen run's text as it is written; the run keeps it whole.
      if ((p?.event as { kind?: string } | undefined)?.kind === "delta") return;
      setEvents((prev) => {
        const list = prev[p.run];
        if (!list) return prev; // not opened yet; the detail load brings it
        if (list.some((e) => e.seq === p.event.seq)) return prev;
        return { ...prev, [p.run]: [...list, p.event] };
      });
    });
    return () => {
      offRun();
      offEv();
    };
  }, [load]);

  useEffect(() => {
    if (!requestedRun) return;
    const match = runs.find(run => run.id === requestedRun);
    if (match) setSelected(match.thread);
  }, [requestedRun, runs]);

  const threads = useMemo(() => threadsOf(runs), [runs]);
  const thread = threads.find((t) => t.id === selected) ?? null;
  const { debates, reviews, putDebate, putReview } = useDebates();
  const debate = selected.startsWith("debate:") ? debates.find((d) => `debate:${d.id}` === selected) ?? null : null;
  const review = selected.startsWith("review:") ? reviews.find((r) => `review:${r.id}` === selected) ?? null : null;
  const cwds = useMemo(() => [...new Set(runs.map((r) => r.cwd).filter(Boolean))].slice(0, 8), [runs]);
  const openRun = useCallback((id: string) => {
    const r = runs.find((x) => x.id === id);
    if (r) setSelected(r.thread);
  }, [runs]);
  const startedReview = useCallback((r: ReviewRec) => { putReview(r); setSelected(`review:${r.id}`); }, [putReview]);
  // One list: conversations with a CLI, debates and reviews, newest first.
  const feed = useMemo(() => {
    const at = (s: string) => +(parseTime(s) ?? 0);
    return [
      ...threads.map((x) => ({ at: at(x.latest.started), kind: "thread" as const, key: x.id, thread: x })),
      ...debates.map((d) => ({ at: at(d.started), kind: "debate" as const, key: `debate:${d.id}`, debate: d })),
      ...reviews.map((r) => ({ at: at(r.started), kind: "review" as const, key: `review:${r.id}`, review: r })),
    ].sort((a, b) => b.at - a.at);
  }, [threads, debates, reviews]);
  const term = terms.find((x) => termKey(x) === selected) ?? null;

  // Load each run of the open thread once; live events extend it after.
  useEffect(() => {
    if (!thread) return;
    for (const r of thread.runs) {
      if (events[r.id]) continue;
      setEvents((prev) => (prev[r.id] ? prev : { ...prev, [r.id]: [] }));
      CLIRunDetail(r.id).then((d) =>
        setEvents((prev) => {
          const live = prev[r.id] ?? [];
          const have = new Set((d.events ?? []).map((e) => e.seq));
          return { ...prev, [r.id]: [...(d.events ?? []), ...live.filter((e) => !have.has(e.seq))] };
        }),
      );
    }
  }, [thread, events]);

  return (
    <div className="view cd">
      <aside className="cd-side">
        <div className="cd-side-h">
          <span>Coding</span>
          <span className="cd-side-b">
            <button className={`cd-ico${selected === "debate:new" ? " on" : ""}`} onClick={() => setSelected("debate:new")} title={t("Debate")} aria-label={t("Debate")}>
              <SwordsIcon size={15} />
            </button>
            <button className={`cd-new${selected === "" ? " on" : ""}`} onClick={() => setSelected("")}>
              <PlusIcon size={14} /> New run
            </button>
          </span>
        </div>
        <div className="cd-list">
          {choices === null && feed.length === 0 && terms.length === 0 && <SkeletonRows n={5} />}
          {choices !== null && feed.length === 0 && terms.length === 0 && <div className="cd-none">{t("Runs you start, and ones the queen starts, show up here.")}</div>}
          {terms.length > 0 && <div className="cd-group">{t("In terminals")}</div>}
          {terms.map((x) => (
            <button key={termKey(x)} className={`cd-item${termKey(x) === selected ? " on" : ""}`} onClick={() => setSelected(termKey(x))} data-testid="term-session">
              <span className="cd-item-top">
                <i className={`cd-dot ${x.state === "working" ? "running" : x.state === "waiting" ? "waiting" : "done"}`} />
                <b>{x.cli}</b>
                <span>{x.machine || "core"}</span>
                <time>{fromNow(parseTime(x.at) ?? new Date())}</time>
              </span>
              <span className="cd-item-p">{x.project || x.session}{x.tool ? ` · ${x.tool}` : ""}</span>
            </button>
          ))}
          {feed.length > 0 && terms.length > 0 && <div className="cd-group">{t("Started by SuperLeo")}</div>}
          {feed.map((row) => {
            if (row.kind === "debate") {
              const d = row.debate;
              return (
                <button key={row.key} className={`cd-item${row.key === selected ? " on" : ""}`} onClick={() => setSelected(row.key)} data-testid="debate-item">
                  <span className="cd-item-top">
                    <i className={`cd-dot ${d.state}`} />
                    <SwordsIcon size={12} />
                    <b>{d.agents.join(" · ")}</b>
                    <time>{fromNow(parseTime(d.started) ?? new Date())}</time>
                  </span>
                  <span className="cd-item-p">{d.topic}</span>
                </button>
              );
            }
            if (row.kind === "review") {
              const r = row.review;
              return (
                <button key={row.key} className={`cd-item${row.key === selected ? " on" : ""}`} onClick={() => setSelected(row.key)} data-testid="review-item">
                  <span className="cd-item-top">
                    <i className={`cd-dot ${r.state === "approved" ? "done" : r.state}`} />
                    <ClipboardCheckIcon size={12} />
                    <b>{r.implementer} → {r.reviewer}</b>
                    <time>{fromNow(parseTime(r.started) ?? new Date())}</time>
                  </span>
                  <span className="cd-item-p">{r.prompt}</span>
                </button>
              );
            }
            const t = row.thread;
            const { cli, host } = partsOf(t.agent);
            return (
              <button key={t.id} className={`cd-item${t.id === selected ? " on" : ""}`} onClick={() => { setSelected(t.id); focusInWorld("machine", t.latest.remote || host || "core"); }}>
                <span className="cd-item-top">
                  <i className={`cd-dot ${t.latest.state}`} />
                  <b>{cli}</b>
                  <span>{host || "core"}</span>
                  {t.runs.length > 1 && <span>· {t.runs.length} turns</span>}
                  <time>{fromNow(parseTime(t.latest.started) ?? new Date())}</time>
                </span>
                <span className="cd-item-p">{t.runs[0].prompt}</span>
              </button>
            );
          })}
        </div>
      </aside>
      <main className="cd-main">
        {term ? (
          <TermView key={termKey(term)} term={term} />
        ) : debate ? (
          <DebateView key={debate.id} debate={debate} reviews={reviews} choices={choices ?? []} onDetail={putDebate} onRun={openRun} onReview={startedReview} onReviewDetail={putReview} />
        ) : review ? (
          <ReviewView key={review.id} review={review} onDetail={putReview} onRun={openRun} />
        ) : selected === "debate:new" ? (
          <DebateForm choices={choices ?? []} cwds={cwds} onStarted={(d) => { putDebate(d); setSelected(`debate:${d.id}`); }} />
        ) : thread ? (
          <ThreadView thread={thread} events={events} choices={choices ?? []} reviews={reviews} onReview={startedReview} onReviewDetail={putReview} onRun={openRun} />
        ) : (
          <Start
            choices={choices}
            onStarted={(r) => {
              setRuns((prev) => (prev.some((x) => x.id === r.id) ? prev : [r, ...prev]));
              setSelected(r.thread);
            }}
          />
        )}
      </main>
    </div>
  );
}

/** A new run: one box, @ picks which CLI and where. */
function Start({ choices, onStarted }: { choices: string[] | null; onStarted: (r: Run) => void }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const [text, setText] = useState("");
  const [cwd, setCwd] = useState("");
  const [ask, setAsk] = useState(true);
  const [busy, setBusy] = useState(false);
  const [fallback, setFallback] = useState(lastAgent);
  const box = useRef<HTMLTextAreaElement>(null);
  const names = choices ?? [];
  const [addressed, task] = addressOf(text, names);
  const agent = addressed || (names.includes(fallback) ? fallback : names[0] ?? "");
  // A run on this computer can take its folder from the native dialog; one on
  // another machine is typed, as a path on that machine.
  const canPick = !!agent && whereOf(agent) === "core" && typeof window !== "undefined" && !!(window as any).runtime;
  const pickFolder = async () => {
    try {
      const dir = await PickDirectory(cwd);
      if (dir) setCwd(dir);
    } catch { /* dismissed or no dialog: the path stays typed */ }
  };

  // Grouped by machine: this is the answer to "what can run where".
  const machines = useMemo(() => {
    const by = new Map<string, string[]>();
    for (const n of names) by.set(whereOf(n), [...(by.get(whereOf(n)) ?? []), n]);
    return [...by.entries()].sort(([a], [b]) => (a === "core" ? -1 : b === "core" ? 1 : a.localeCompare(b)));
  }, [names]);

  const start = async () => {
    if (!task.trim() || !agent || busy) return;
    setBusy(true);
    try {
      const r = await StartCLIRun(agent, task.trim(), cwd, "", ask);
      try { localStorage.setItem(LAST, agent); } catch { /* remembered for this visit only */ }
      setFallback(agent);
      onStarted(r);
    } catch (e) {
      toast.error(String(e));
    } finally {
      setBusy(false);
    }
  };

  // Clicking a CLI addresses the message to it, keeping what was typed.
  const pick = (n: string) => {
    setFallback(n);
    try { localStorage.setItem(LAST,n); } catch { /* in-memory choice remains */ }
    const rest = addressOf(text, names)[1];
    const next = `@${n} ${rest}`;
    setText(next);
    requestAnimationFrame(() => { box.current?.focus(); box.current?.setSelectionRange(next.length, next.length); });
  };

  if (choices && choices.length === 0) {
    return (
      <div className="cd-start">
        <div className="cd-start-in">
          <h2>{t("No coding CLI to run")}</h2>
          <p className="cd-sub">{t("Core has none installed and no linked agent offers one. Install Claude Code or Codex on core, or run")} <code>superleo agent</code> on a machine that has them.</p>
          <button className="cd-btn" onClick={() => navigate(PATHS.settings)}>{t("Open Settings")}</button>
        </div>
      </div>
    );
  }

  return (
    <div className="cd-start">
      <div className="cd-start-in">
        <h2>{translate("New run")}</h2>
        <Composer
          boxRef={box}
          value={text}
          onChange={setText}
          names={names}
          placeholder={agent ? `Tell ${partsOf(agent).cli} on ${whereOf(agent)} what to do. @ for another CLI or machine` : "Loading…"}
          onSend={start}
          sendable={!!task.trim() && !!agent && !busy}
          foot={
            <>
              {agent && (
                <label className="cd-to" title={translate("Select a coding agent")}>
                  <TerminalIcon size={13} />
                  <select className="cd-agent-select" aria-label={translate("Select a coding agent")} value={agent} onChange={event => pick(event.target.value)}>
                    {machines.map(([host, list]) => <optgroup key={host} label={host === "core" ? "Core" : host}>
                      {list.map(name => <option key={name} value={name}>{partsOf(name).cli} · {whereOf(name)}</option>)}
                    </optgroup>)}
                  </select>
                </label>
              )}
              <span className="cd-cwd" title={t("The directory it works in, on that machine")}>
                {canPick ? (
                  <button type="button" className="cd-pick" aria-label={t("Choose a folder")} title={t("Choose a folder")} onClick={() => void pickFolder()}>
                    <FolderOpenIcon size={13} />
                  </button>
                ) : <FolderIcon size={13} />}
                <input value={cwd} placeholder={translate("workspace")} aria-label={translate("workspace")} onChange={(e) => setCwd(e.target.value)} spellCheck={false} />
              </span>
              <label className="cd-ask" title={t("Off: the CLI runs its tools without asking")}>
                <input type="checkbox" checked={ask} onChange={(e) => setAsk(e.target.checked)} /> {translate("Ask before tools run")}
              </label>
            </>
          }
        />

      </div>
    </div>
  );
}

/**
 * The input both for a new run and for a follow-up: a textarea with a footer
 * of settings and the send button. With `names`, @ completes coding CLIs.
 */
function Composer({
  value, onChange, names = [], placeholder, onSend, sendable, disabled, foot, action, boxRef, onPaste,
}: {
  onPaste?: (e: React.ClipboardEvent<HTMLTextAreaElement>) => void;
  value: string;
  onChange: (v: string) => void;
  names?: string[];
  placeholder: string;
  onSend: () => void;
  sendable: boolean;
  disabled?: boolean;
  foot?: React.ReactNode;
  action?: React.ReactNode;
  boxRef?: React.RefObject<HTMLTextAreaElement | null>;
}) {
  const own = useRef<HTMLTextAreaElement>(null);
  const ref = boxRef ?? own;
  const ime = useImeGuard();
  const [q, setQ] = useState<{ word: string; at: number } | null>(null);
  const [active, setActive] = useState(0);
  // Where the caret goes once a picked name is in: set in the same frame as
  // the new text, so nothing typed in between lands in front of it.
  const caretTo = useRef<number | null>(null);
  useLayoutEffect(() => {
    if (caretTo.current == null) return;
    ref.current?.focus();
    ref.current?.setSelectionRange(caretTo.current, caretTo.current);
    caretTo.current = null;
  }, [value, ref]);
  const matches = useMemo(() => {
    if (!q) return [];
    const w = q.word.toLowerCase();
    const hits = names.filter((n) => n.toLowerCase().startsWith(w) || whereOf(n).toLowerCase().startsWith(w));
    return hits.length === 1 && hits[0].toLowerCase() === w ? [] : hits;
  }, [q, names]);

  // WKWebView can deliver native input and composition commits without a
  // matching React change event. Keep the controlled draft in step with both.
  const syncInput = (input: HTMLTextAreaElement) => {
    onChange(input.value);
    setQ(names.length ? mentionAt(input.value, input.selectionStart ?? input.value.length) : null);
    setActive(0);
  };

  const accept = (n: string) => {
    if (!q) return;
    const caret = ref.current?.selectionStart ?? value.length;
    const ins = `@${n} `;
    const next = value.slice(0, q.at) + ins + value.slice(caret);
    caretTo.current = q.at + ins.length;
    onChange(next);
    setQ(null);
  };

  return (
    <div className={`cd-comp${disabled ? " off" : ""}`}>
      {matches.length > 0 && (
        <div className="cd-menu" role="listbox">
          {matches.map((n, i) => (
            <button
              key={n}
              role="option"
              aria-selected={i === active}
              className={i === active ? "on" : ""}
              onMouseDown={(e) => { e.preventDefault(); accept(n); }}
            >
              <b>@{n}</b>
              <span>{partsOf(n).cli} on {whereOf(n)}</span>
            </button>
          ))}
        </div>
      )}
      <textarea
        ref={ref}
        rows={2}
        value={value}
        placeholder={placeholder}
        disabled={disabled}
        aria-label={translate("Task instructions")}
        onInput={event => syncInput(event.currentTarget)}
        onChange={event => syncInput(event.currentTarget)}
        onBlur={() => setQ(null)}
        onPaste={onPaste}
        onCompositionStart={ime.handlers.onCompositionStart}
        onCompositionEnd={event => { ime.handlers.onCompositionEnd(event); syncInput(event.currentTarget); }}
        onKeyDown={(e) => {
          if (matches.length > 0) {
            if (e.key === "ArrowDown" || e.key === "ArrowUp") {
              e.preventDefault();
              setActive((i) => (i + (e.key === "ArrowDown" ? 1 : matches.length - 1)) % matches.length);
              return;
            }
            if ((e.key === "Enter" || e.key === "Tab") && !ime.composing(e)) {
              e.preventDefault();
              accept(matches[active]);
              return;
            }
            if (e.key === "Escape") { e.preventDefault(); setQ(null); return; }
          }
          if (e.key === "Enter" && !e.shiftKey && !ime.composing(e)) {
            e.preventDefault();
            if (sendable) onSend();
          }
        }}
      />
      <div className="cd-comp-f">
        {foot}
        <span className="cd-grow" />
        {action ?? (
          <button className="cd-send" onClick={onSend} disabled={!sendable} aria-label="Send">
            <ArrowUpIcon size={16} />
          </button>
        )}
      </div>
    </div>
  );
}

function ThreadView({ thread, events, choices, reviews, onReview, onReviewDetail, onRun }: {
  thread: Thread; events: Record<string, Ev[]>; choices: string[]; reviews: ReviewRec[];
  onReview: (r: ReviewRec) => void; onReviewDetail: (r: ReviewRec) => void; onRun: (id: string) => void;
}) {
  const [follow, setFollow] = useState("");
  const [sending, setSending] = useState(false);
  const end = useRef<HTMLDivElement>(null);
  const running = thread.latest.state === "running";
  const total = thread.runs.reduce((n, r) => n + r.costUsd, 0);
  const count = thread.runs.reduce((n, r) => n + (events[r.id]?.length ?? 0), 0);
  const { cli, host } = partsOf(thread.agent);
  // A finished run on this machine can be reviewed by the other of claude and codex.
  const own = !thread.runs[0].readOnly && thread.runs[0].origin !== "review" && thread.runs[0].origin !== "debate";
  const canReview = own && !running && thread.latest.state === "done" && !host && (thread.agent === "claude" || thread.agent === "codex")
    && choices.includes(thread.agent === "claude" ? "codex" : "claude");
  const mine = reviews.filter((r) => !!r.fromRun && thread.runs.some((x) => x.id === r.fromRun));

  useEffect(() => {
    end.current?.scrollIntoView({ block: "end" });
  }, [count, thread.id]);

  const send = async () => {
    if (!follow.trim() || sending || running) return;
    setSending(true);
    try {
      await FollowUpCLIRun(thread.latest.id, follow);
      setFollow("");
    } catch (e) {
      toast.error(String(e));
    } finally {
      setSending(false);
    }
  };

  return (
    <div className="cd-thread">
      <div className="cd-thread-h">
        <TerminalIcon size={15} />
        <b>{cli}</b>
        <span>on {host || "core"}</span>
        <span className="cd-path" title={thread.latest.cwd}>{shortPath(thread.latest.cwd)}</span>
        <span className="cd-meta">
          {thread.latest.ask ? "asks before tools" : "unattended"}
          {total > 0 && ` · ${cost(total)}`}
        </span>
        {canReview && <ReviewWith agent={thread.agent} fromRun={thread.runs[0].id} onStarted={onReview} />}
      </div>
      <div className="cd-log">
        <div className="cd-log-in">
          {thread.runs.map((r) => (
            <RunBlock key={r.id} run={r} events={events[r.id] ?? []} />
          ))}
          {mine.map((r) => <ReviewTrack key={r.id} review={r} onDetail={onReviewDetail} onRun={onRun} />)}
          <div ref={end} />
        </div>
      </div>
      <div className="cd-follow">
        <Composer
          value={follow}
          onChange={setFollow}
          placeholder={running ? `${cli} is working…` : `Continue with ${cli}`}
          onSend={send}
          sendable={!running && !sending && !!follow.trim()}
          disabled={running}
          action={
            running ? (
              <button className="cd-send stop" onClick={() => CancelCLIRun(thread.latest.id)} aria-label="Stop" title="Stop">
                <SquareIcon size={12} fill="currentColor" />
              </button>
            ) : undefined
          }
        />
      </div>
    </div>
  );
}

/** Pictures already read, by session and reference: the log is pulled again
 *  every few seconds, and a picture is megabytes. */
const pictureCache = new Map<string, Promise<string>>();

function sessionPicture(term: Term, ref: string): Promise<string> {
  const key = `${termKey(term)}|${ref}`;
  let p = pictureCache.get(key);
  if (!p) {
    p = CodingSessionImage(term.machine ?? "", term.cli, term.session ?? "", ref);
    p.catch(() => pictureCache.delete(key));
    pictureCache.set(key, p);
  }
  return p;
}

/** The pictures pasted with what the person said, small; one opens full size. */
function SessionPictures({ term, refs }: { term: Term; refs: string[] }) {
  const { t } = useI18n();
  const [srcs, setSrcs] = useState<Record<string, string>>({});
  const [open, setOpen] = useState("");
  useEffect(() => {
    let alive = true;
    for (const ref of refs) {
      sessionPicture(term, ref)
        .then((src) => { if (alive) setSrcs((s) => (s[ref] ? s : { ...s, [ref]: src })); })
        .catch(() => { /* gone from the log: the text still says it was there */ });
    }
    return () => { alive = false; };
  }, [term, refs.join(" ")]);
  useEffect(() => {
    if (!open) return;
    const esc = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(""); };
    window.addEventListener("keydown", esc);
    return () => window.removeEventListener("keydown", esc);
  }, [open]);
  return (
    <div className="cd-pics">
      {refs.map((ref, i) => srcs[ref] ? (
        <button key={ref} type="button" className="cd-pic" onClick={() => setOpen(srcs[ref])} aria-label={t("Open picture {n}", { n: i + 1 })}>
          <img src={srcs[ref]} alt="" />
        </button>
      ) : <span key={ref} className="cd-pic loading" />)}
      {open && createPortal(
        <div className="cd-lightbox" role="dialog" aria-modal="true" onClick={() => setOpen("")}>
          <img src={open} alt="" />
        </div>,
        document.body,
      )}
    </div>
  );
}

/** A session somebody started in a terminal: its log, read from the machine
 *  it runs on every couple of seconds while open, and a box that types an
 *  answer into its terminal. */
function TermView({ term }: { term: Term }) {
  const { t } = useI18n();
  const [evs, setEvs] = useState<Ev[] | null>(null);
  const [err, setErr] = useState("");
  const [reply, setReply] = useState("");
  const [sending, setSending] = useState(false);
  // Pictures and files go in by path: the CLI reads them off this Mac's disk,
  // so only a session running here can take them.
  const local = !term.machine;
  const att = useAttachments({ drop: local });
  const end = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const log = useRef<HTMLDivElement>(null);
  useEffect(() => {
    let alive = true;
    const pull = () =>
      CodingSessionLog(term.machine ?? "", term.cli, term.session ?? "")
        .then((l) => { if (alive) { setEvs(((l as { events?: Ev[] }).events ?? []) as Ev[]); setErr(""); } })
        .catch((e) => { if (alive) setErr(String(e instanceof Error ? e.message : e)); });
    pull();
    const timer = window.setInterval(pull, 2500);
    return () => { alive = false; window.clearInterval(timer); };
  }, [term.machine, term.cli, term.session]);
  const n = evs?.length ?? 0;
  useEffect(() => { if (stick.current) end.current?.scrollIntoView({ block: "end" }); }, [n]);

  const send = async () => {
    const typed = reply.trim();
    if ((!typed && att.paths.length === 0) || sending) return;
    setSending(true);
    try {
      const files = await Promise.all(att.paths.map((p) => WorkspaceAbsPath(p)));
      const text = [typed, ...files.map((f) => (/\s/.test(f) ? `"${f}"` : f))].filter(Boolean).join("\n");
      const d = (await ReplyToCodingSession(term.machine ?? "", term.cli, term.session ?? "", text)) as { via?: string; note?: string };
      setReply("");
      att.clear();
      stick.current = true;
      if (d?.note) toast.success(d.note);
    } catch (e) {
      toast.error(String(e instanceof Error ? e.message : e));
    } finally {
      setSending(false);
    }
  };

  const shown = evs ?? [];
  const results = new Map<string, Ev>();
  for (const e of shown) if (e.kind === "result" && e.callId) results.set(e.callId, e);
  return (
    <div className="cd-thread">
      <div className="cd-thread-h">
        <TerminalIcon size={15} />
        <b>{term.cli}</b>
        <span>{t("on {host}", { host: term.machine || "core" })}</span>
        <span className="cd-path">{term.project}</span>
        <span className="cd-meta">{t(term.state)}</span>
      </div>
      <div className="cd-log" ref={log} onScroll={(e) => { const el = e.currentTarget; stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 60; }}>
        <div className="cd-log-in">
          {evs === null && !err && <SkeletonLines n={5} />}
          {err && <div className="agents-note bad">{err}</div>}
          <div className="agents-run" data-testid="term-log">
            {withBrowserCards(shown.filter((e) => !(e.kind === "result" && e.callId && results.has(e.callId) && shown.some((c) => c.kind === "tool" && c.callId === e.callId))), results, term.state === "running", termKey(term)).map((e) =>
              isCard(e) ? (
                <BrowserCard key={`bw-${e.seq}`} turnKey={e.key} calls={e.calls} running={e.running} live={e.running} />
              ) : e.kind === "you" ? (
                <div key={e.seq} className="agents-you">
                  {e.text}
                  {(e.images?.length ?? 0) > 0 && <SessionPictures term={term} refs={e.images!} />}
                </div>
              ) : e.kind === "tool" ? (
                <ToolStep key={e.seq} call={e} result={e.callId ? results.get(e.callId) : undefined} />
              ) : e.kind === "result" ? (
                <ToolStep key={e.seq} call={{ ...e, kind: "tool", detail: "" } as Ev} result={e} />
              ) : (
                <div key={e.seq} className="agents-text md">
                  <ReactMarkdown remarkPlugins={[remarkGfm]} components={mdComponents}>{e.text ?? ""}</ReactMarkdown>
                </div>
              ),
            )}
          </div>
          <div ref={end} />
        </div>
      </div>
      <div className="cd-follow">
        <AttachmentChips paths={att.paths} onRemove={att.remove} />
        <Composer
          value={reply}
          onChange={setReply}
          placeholder={term.replyable ? t("Reply to {cli}", { cli: term.cli }) : t("This session has ended")}
          onSend={send}
          onPaste={local ? att.paste : undefined}
          sendable={term.replyable && !sending && !att.importing && (!!reply.trim() || att.paths.length > 0)}
          disabled={!term.replyable}
          foot={local && term.replyable ? (
            <button type="button" className="cd-attach" title={t("Attach files")} aria-label={t("Attach files")} onClick={att.pick} disabled={att.importing}>
              <PaperclipIcon size={16} />
            </button>
          ) : undefined}
        />
      </div>
    </div>
  );
}

function RunBlock({ run, events }: { run: Run; events: Ev[] }) {
  // A result is drawn under the call it answers.
  const results = new Map<string, Ev>();
  for (const e of events) if (e.kind === "result" && e.callId) results.set(e.callId, e);
  const shown = events.filter((e) => !(e.kind === "result" && e.callId && events.some((c) => c.kind === "tool" && c.callId === e.callId)));
  const secs = run.ended ? (+parseTime(run.ended)! - +parseTime(run.started)!) / 1000 : 0;

  return (
    <div className="agents-run">
      <div className="agents-you">{run.prompt}</div>
      {withBrowserCards(shown, results, run.state === "running", run.id).map((e) =>
        isCard(e) ? (
          <BrowserCard key={`bw-${e.seq}`} turnKey={e.key} calls={e.calls} running={e.running} live={e.running} />
        ) : e.kind === "tool" ? (
          <ToolStep key={e.seq} call={e} result={e.callId ? results.get(e.callId) : undefined} />
        ) : e.kind === "text" ? (
          <div key={e.seq} className="agents-text md">
            <ReactMarkdown remarkPlugins={[remarkGfm]} components={mdComponents}>
              {e.text ?? ""}
            </ReactMarkdown>
          </div>
        ) : e.kind === "note" ? (
          <div key={e.seq} className={`agents-note${e.failed ? " bad" : ""}`}>
            {e.tool && <b>{e.tool}</b>} {e.text}
          </div>
        ) : e.kind === "error" ? (
          <div key={e.seq} className="agents-note bad">{e.text}</div>
        ) : (
          <ToolStep key={e.seq} call={{ ...e, kind: "tool", detail: "" } as Ev} result={e} />
        ),
      )}
      <div className={`agents-end ${run.state}`}>
        {run.state === "running" ? (
          <><span className="spinner" style={{ borderTopColor: "var(--accent)" }} /> working</>
        ) : (
          <>
            {run.state}
            {secs > 0 && ` · ${secs.toFixed(1)}s`}
            {` · ${run.tools} tool calls`}
            {run.costUsd > 0 && ` · ${cost(run.costUsd)}`}
            {run.error && ` · ${run.error}`}
          </>
        )}
      </div>
    </div>
  );
}

type BrowserItem = { kind: "browser"; seq: number; key: string; calls: BrowserCall[]; running: boolean };
const isCard = (e: Ev | BrowserItem): e is BrowserItem => e.kind === "browser";

/**
 * A log with each turn's browser calls drawn as one card, where the first of
 * them was. A turn here is the stretch between two things the person said.
 */
function withBrowserCards(evs: Ev[], results: Map<string, Ev>, running: boolean, key: string): (Ev | BrowserItem)[] {
  const out: (Ev | BrowserItem)[] = [];
  let card: BrowserItem | null = null;
  evs.forEach((e, i) => {
    if (e.kind === "you") card = null;
    const op = e.kind === "tool" ? browserOp(e.tool) : "";
    if (!op) { out.push(e); return; }
    const r = e.callId ? results.get(e.callId) : undefined;
    const call: BrowserCall = { id: String(e.seq), op, args: {}, detail: e.detail, status: r ? (r.failed ? "fail" : "ok") : running ? "running" : "ok" };
    if (!card) {
      card = { kind: "browser", seq: e.seq, key: `${key}:${e.seq}`, calls: [], running: false };
      out.push(card);
    }
    card.calls.push(call);
    // Only the last turn of a running log can still be at work.
    card.running = running && !evs.slice(i + 1).some((x) => x.kind === "you");
  });
  return out;
}

function ToolStep({ call, result }: { call: Ev; result?: Ev }) {
  const [open, setOpen] = useState(false);
  const out = result?.text ?? "";
  return (
    <div className={`agents-tool${result?.failed ? " bad" : ""}${result ? "" : " live"}`}>
      <button className="agents-tool-head" onClick={() => setOpen((v) => !v)} disabled={!out}>
        <ChevronRightIcon size={13} className={open ? "rot" : ""} style={{ opacity: out ? 1 : 0.25 }} />
        <span className="agents-tool-name">{call.tool}</span>
        <span className="agents-tool-detail">{call.detail}</span>
      </button>
      {open && out && <pre className="agents-tool-out">{out}</pre>}
    </div>
  );
}
