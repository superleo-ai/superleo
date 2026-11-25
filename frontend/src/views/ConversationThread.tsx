import type React from "react";
import { Fragment, useCallback, useEffect, useRef, useState } from "react";
import { ArrowLeftIcon, ArrowUpIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { AskIn, ConversationThread, DeleteChatSession, FollowUpCLIRun, ReplyToCodingSession, SendChat, StartCLIRun } from "../../wailsjs/go/app/App";
import { app, backend } from "../../wailsjs/go/models";
import { EventsOn } from "../../wailsjs/runtime";
import { Response } from "@/components/ai-elements/response";
import { SentMessage } from "../components/SentFiles";
import { useI18n } from "../lib/i18n";
import { useImeGuard } from "../lib/ime";
import { visibleAnswer, withoutCallNotes } from "../lib/format";
import { queenSession } from "../lib/queenConsole";
import { useStickToBottom } from "../lib/useStickToBottom";
import { unpin } from "../lib/pinnedConvs";
import { SkeletonLines } from "../components/Skeleton";
import { bare, useWho } from "./ConversationList";
import "./conversations.css";

/** The day a message was said on, for the line between days. */
function dayKey(iso?: string): string {
  const d = iso ? new Date(iso) : null;
  return d && !isNaN(d.getTime()) ? d.toDateString() : "";
}
function dayLabel(iso: string, t: (k: string) => string): string {
  const d = new Date(iso);
  const now = new Date();
  if (d.toDateString() === now.toDateString()) return t("Today");
  const y = new Date(now); y.setDate(now.getDate() - 1);
  if (d.toDateString() === y.toDateString()) return t("Yesterday");
  return d.getFullYear() === now.getFullYear() ? d.toLocaleDateString([], { month: "long", day: "numeric" }) : d.toLocaleDateString();
}


export function Thread({ id, onBack, onOpen, onChanged, embedded, lead }: {
  id: string; onBack: () => void; onOpen: (id: string) => void; onChanged: () => void;
  /** Shown inside the chat page, where the list is beside it: no way back. */
  embedded?: boolean;
  /** Buttons at the head's start, for the page that holds it. */
  lead?: React.ReactNode;
}) {
  const { t } = useI18n();
  const who = useWho();
  const ime = useImeGuard();
  const [th, setTh] = useState<app.ConversationThread | null>(null);
  const [error, setError] = useState("");
  const [text, setText] = useState("");
  // What was just said, until the saved thread has it.
  const [pending, setPending] = useState("");
  const [busy, setBusy] = useState(false);
  const [streamed, setStreamed] = useState("");
  const [fresh, setFresh] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const live = useRef("");
  const raw = useRef("");
  const stick = useStickToBottom<HTMLDivElement>();
  const c = th?.conversation;

  const load = useCallback(() => ConversationThread(id).then((x) => { setTh(x); setError(""); return x; })
    .catch((e) => { setError(String(e?.message ?? e)); return null; }), [id]);
  useEffect(() => { void load(); }, [load]);

  const settle = useCallback(() => {
    live.current = ""; raw.current = ""; setStreamed(""); setPending(""); setBusy(false);
  }, []);

  // The queen answers on the chat events of the request; the words stream in.
  useEffect(() => {
    const offs = [
      EventsOn("chat:event", (p: any) => {
        if (!live.current || p?.requestId !== live.current) return;
        if (p.type === "partial" && p.content) { raw.current += p.content; setStreamed(visibleAnswer(raw.current)); }
        else if (p.type === "tombstone" && p.debugType === "retry") { raw.current = ""; setStreamed(""); }
      }),
      EventsOn("chat:done", (p: any) => { if (live.current && p?.requestId === live.current) load().then(settle); else if (c?.kind === "queen") void load(); }),
      EventsOn("chat:error", (p: any) => { if (live.current && p?.requestId === live.current) { setError(String(p.error ?? "")); settle(); } }),
      EventsOn("chat:cancelled", (p: any) => { if (live.current && p?.requestId === live.current) settle(); }),
    ];
    return () => offs.forEach((f) => typeof f === "function" && f());
  }, [load, settle, c?.kind]);

  // A coding run or a terminal session answers on its own time: read the
  // thread again until it stops working.
  const waiting = busy && (c?.kind === "coding");
  const running = c?.state === "running";
  useEffect(() => {
    if (!waiting && !running) return;
    let polls = 0;
    const h = window.setInterval(async () => {
      polls++;
      const x = await load();
      const done = x && x.conversation.state !== "running";
      // A terminal reply shows in its log when the agent writes; give it a minute.
      if (done && (x.conversation.source !== "terminal" || polls >= 20)) { settle(); onChanged(); }
      else if (polls >= 80) settle();
    }, 3000);
    return () => window.clearInterval(h);
  }, [waiting, running, load, settle, onChanged]);

  const send = async () => {
    const s = text.trim();
    if (!s || busy || !c) return;
    setText("");
    setPending(s);
    setBusy(true);
    setError("");
    try {
      if (fresh || (c.kind === "coding" && !c.replyable && c.source !== "terminal")) {
        const agent = (c.source === "terminal" && c.machine ? `${c.agent}.${c.machine}` : c.agent) ?? "";
        const run = await StartCLIRun(agent, s, c.cwd ?? "", "", true);
        settle();
        onChanged();
        onOpen("cli:" + run.thread);
        return;
      }
      switch (c.kind) {
        case "queen":
          raw.current = ""; setStreamed("");
          live.current = await SendChat(c.session, s, []);
          return; // the chat events finish it
        case "worker": case "agent": {
          const r = await AskIn(c.session, c.kind, c.name ?? "", s);
          await load();
          if (r.failed) setError(r.text);
          break;
        }
        case "coding":
          if (c.source === "terminal") await ReplyToCodingSession(c.machine ?? "", c.agent ?? c.name ?? "", c.session, s);
          else await FollowUpCLIRun(c.run!, s);
          await load();
          return; // polled until it is done
      }
      settle();
      onChanged();
    } catch (e: any) {
      setError(String(e?.message ?? e));
      settle();
    }
  };

  const del = async () => {
    if (!confirm) { setConfirm(true); window.setTimeout(() => setConfirm(false), 4000); return; }
    await DeleteChatSession(c!.session).catch((e) => setError(String(e?.message ?? e)));
    unpin(c!.id);
    onChanged();
    onBack();
  };

  if (!th && error) return <div className="cv2-none">{error}</div>;
  if (!th) return <div className="cv2-none" style={{ alignItems: "stretch", justifyContent: "flex-start", padding: 24 }}><SkeletonLines n={5} /></div>;
  const turns = (th.turns ?? []).filter((m: backend.ChatTurn) => m.kind !== "context");
  const canReply = !!c && (c.replyable || fresh) ;
  const startNew = !!c && c.kind === "coding" && !c.replyable;
  const canDelete = c?.kind === "queen" && c.session !== queenSession();
  return (
    <div className="cv2-pane" data-testid="conv-thread">
      <header className="cv2-phead">
        {lead}
        {!embedded && <button className="cv2-back" onClick={onBack} title={t("Back")} aria-label={t("Back")}><ArrowLeftIcon size={17} /></button>}
        <div>
          <b>{who.name(c!)}{c!.machine ? <i> · {bare(c!.machine)}</i> : null}</b>
          <small>{who.kind(c!)}{c!.title && c!.session !== queenSession() ? ` · ${withoutCallNotes(c!.title)}` : ""}</small>
        </div>
        {canDelete && (
          <button className={`cv2-del${confirm ? " arm" : ""}`} onClick={del} title={confirm ? t("Delete this conversation?") : t("Delete")} aria-label={t("Delete")}>
            <Trash2Icon size={15} />{confirm && <span>{t("Delete")}</span>}
          </button>
        )}
      </header>
      <div className="cv2-msgs" ref={stick.ref} onScroll={stick.onScroll}>
        {turns.length === 0 && !pending && <div className="dk-empty">{t("No messages")}</div>}
        {turns.map((m: backend.ChatTurn, i: number) => {
          const day = dayKey(m.at) && dayKey(m.at) !== dayKey(turns[i - 1]?.at) ? <div className="cv2-day">{dayLabel(m.at!, t)}</div> : null;
          return m.role === "user" ? (
            <Fragment key={i}>{day}<div className="dk-u"><SentMessage content={withoutCallNotes(m.content)} /></div></Fragment>
          ) : (
            <Fragment key={i}>{day}
              <div className={m.kind === "error" ? "dk-q bad" : "dk-q"}>
                {m.steps && m.steps.length > 0 && (
                  <details className="cv2-steps"><summary>{t("Steps")} · {m.steps.length}</summary>{m.steps.map((s, n) => <div key={n}>{s}</div>)}</details>
                )}
                {m.kind === "error" ? <span>{m.content}</span> : <Response>{withoutCallNotes(m.content)}</Response>}
              </div>
            </Fragment>
          );
        })}
        {pending && !turns.slice(-2).some((m: backend.ChatTurn) => m.role === "user" && m.content.trim() === pending) && (
          <div className="dk-u"><SentMessage content={pending} /></div>
        )}
        {streamed && <div className="dk-q"><Response>{streamed}</Response></div>}
        {(busy || running) && !streamed && <div className="dk-q"><span className="dk-thinking">{c!.kind === "coding" ? t("Working…") : t("Thinking…")}</span></div>}
        {error && <div className="dk-err" role="alert">{error}</div>}
      </div>
      {startNew && !fresh && (
        <div className="cv2-readonly">
          <span>{c!.why || t("Read only")}</span>
          <button onClick={() => setFresh(true)} data-testid="conv-start-new"><PlusIcon size={14} /> {t("Start new")}</button>
        </div>
      )}
      {(canReply || (startNew && fresh)) && (
        <form className="dk-comp" onSubmit={(e) => { e.preventDefault(); void send(); }}>
          <textarea rows={1} value={text} onChange={(e) => setText(e.target.value)} data-testid="conv-input" autoFocus
            placeholder={fresh ? t("New conversation with {name}", { name: who.name(c!) }) : t("Message {name}", { name: who.name(c!) })}
            onCompositionStart={ime.handlers.onCompositionStart} onCompositionEnd={ime.handlers.onCompositionEnd}
            onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); if (!ime.composing(e)) void send(); } }} />
          <button type="submit" className="dk-send" disabled={!text.trim() || busy} title={t("Send")} aria-label={t("Send")} data-testid="conv-send"><ArrowUpIcon size={18} /></button>
        </form>
      )}
    </div>
  );
}
