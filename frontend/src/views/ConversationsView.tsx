import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { ArrowLeftIcon, ArrowUpIcon, BotIcon, CpuIcon, HexagonIcon, MoreHorizontalIcon, PencilIcon, PinIcon, PinOffIcon, PlusIcon, SearchIcon, TerminalIcon, Trash2Icon, XIcon, type LucideIcon } from "lucide-react";
import {
  AskIn, ConversationThread, Conversations, DeleteChatSession, FollowUpCLIRun, RenameChatSession, ReplyToCodingSession, SendChat, StartCLIRun,
} from "../../wailsjs/go/app/App";
import { app, backend } from "../../wailsjs/go/models";
import { EventsOn } from "../../wailsjs/runtime";
import { Response } from "@/components/ai-elements/response";
import { SentMessage } from "../components/SentFiles";
import { useI18n } from "../lib/i18n";
import { useImeGuard } from "../lib/ime";
import { visibleAnswer, withoutCallNotes } from "../lib/format";
import { queenTitle, useQueenName } from "../lib/queenName";
import { isLegacyConsole, legacyConsoleTitle, pairedDeviceNames, queenSession, useQueenSession } from "../lib/queenConsole";
import { useStickToBottom } from "../lib/useStickToBottom";
import { renamePinned, togglePin, unpin, usePinnedConvs } from "../lib/pinnedConvs";
import "./conversations.css";
import { SkeletonLines, SkeletonRows } from "../components/Skeleton";

type Conv = app.Conversation;

const KIND: Record<string, { Icon: LucideIcon; label: string }> = {
  queen: { Icon: HexagonIcon, label: "Queen" },
  worker: { Icon: CpuIcon, label: "Worker" },
  agent: { Icon: BotIcon, label: "Agent" },
  coding: { Icon: TerminalIcon, label: "Coding" },
};

/** The time of the last word: the clock today, the day before that. */
function when(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  const now = new Date();
  if (d.toDateString() === now.toDateString()) return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
  if (d.getFullYear() === now.getFullYear()) return d.toLocaleDateString([], { month: "numeric", day: "numeric" });
  return d.toLocaleDateString();
}

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

const bare = (m?: string) => (m ?? "").replace(/^https?:\/\//, "").replace(/\/+$/, "");

/** Who a row is with, and where. */
function useWho() {
  const { t } = useI18n();
  const q = useQueenName();
  return useMemo(() => ({
    name: (c: { kind: string; name?: string }) => (c.kind === "queen" ? queenTitle(q) : c.name || t(KIND[c.kind]?.label ?? "Agent")),
    kind: (c: { kind: string; source?: string }) => t(c.source === "terminal" ? "Terminal" : KIND[c.kind]?.label ?? "Agent"),
  }), [q, t]);
}

export default function ConversationsView() {
  const { t } = useI18n();
  const who = useWho();
  const [params, setParams] = useSearchParams();
  const withKey = params.get("with") ?? "";
  const openId = params.get("open") ?? "";
  const [query, setQuery] = useState("");
  const [needle, setNeedle] = useState("");
  const [data, setData] = useState<app.ConversationList | null>(null);
  const [failed, setFailed] = useState("");
  const consoleId = useQueenSession();
  const pinned = usePinnedConvs();
  const [menu, setMenu] = useState<{ c: Conv; x: number; y: number } | null>(null);
  const [renaming, setRenaming] = useState("");

  useEffect(() => {
    const h = window.setTimeout(() => setNeedle(query.trim()), 250);
    return () => window.clearTimeout(h);
  }, [query]);

  const load = useCallback(() => {
    Conversations(app.ConversationFilter.createFrom({ with: withKey, query: needle, limit: 0 }))
      .then(async (l) => {
        const devices = (l?.items ?? []).some((c) => c.kind === "queen" && isLegacyConsole(c.session)) ? await pairedDeviceNames() : [];
        for (const c of l?.items ?? []) {
          if (c.kind === "queen" && isLegacyConsole(c.session)) c.title = legacyConsoleTitle(c.session, devices);
        }
        setData(l);
        setFailed("");
      })
      .catch((e) => setFailed(String(e?.message ?? e)));
  }, [withKey, needle]);
  useEffect(() => { load(); }, [load]);
  useEffect(() => {
    const offs = ["chat:done", "chat:error", "cli:run"].map((n) => EventsOn(n, () => load()));
    const tick = window.setInterval(load, 15000);
    window.addEventListener("focus", load);
    return () => {
      offs.forEach((off) => typeof off === "function" && off());
      window.clearInterval(tick);
      window.removeEventListener("focus", load);
    };
  }, [load]);

  const set = (patch: Record<string, string>) => {
    const next = new URLSearchParams(params);
    for (const [k, v] of Object.entries(patch)) v ? next.set(k, v) : next.delete(k);
    setParams(next, { replace: true });
  };

  const items = data?.items ?? [];
  const parties = data?.parties ?? [];
  const total = parties.reduce((n, p) => n + p.count, 0);

  return (
    <div className={`cv2${openId ? " has-open" : ""}`} data-testid="conversations">
      <aside className="cv2-list">
        <header className="cv2-head">
          <h1>{t("Conversations")}</h1>
          <label className="cv2-search">
            <SearchIcon size={14} />
            <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t("Search")} aria-label={t("Search")} data-testid="conv-search" />
            {query && <button type="button" title={t("Clear")} aria-label={t("Clear")} onClick={() => setQuery("")}><XIcon size={13} /></button>}
          </label>
          <div className="cv2-who" role="tablist" aria-label={t("Who")}>
            <button role="tab" aria-selected={!withKey} className={!withKey ? "on" : ""} onClick={() => set({ with: "" })} data-testid="who-all">
              {t("Everyone")} <i>{total}</i>
            </button>
            {parties.map((p) => {
              const Icon = KIND[p.kind]?.Icon ?? BotIcon;
              return (
                <button key={p.with} role="tab" aria-selected={withKey === p.with} className={withKey === p.with ? "on" : ""}
                  onClick={() => set({ with: withKey === p.with ? "" : p.with })} title={bare(p.machine)} data-testid="who-chip">
                  <Icon size={13} /> {who.name(p)} <i>{p.count}</i>
                </button>
              );
            })}
          </div>
        </header>
        <div className="cv2-rows">
          {failed && <div className="cv2-empty" role="alert">{failed}</div>}
          {!failed && !data && <SkeletonRows n={7} />}
          {!failed && data && items.length === 0 && <div className="cv2-empty">{t("No conversations")}</div>}
          {items.map((c) => (
            <Row key={c.id} c={c} on={openId === c.id} main={c.kind === "queen" && c.session === consoleId}
              pinned={pinned.some((p) => p.id === c.id)}
              onOpen={() => set({ open: c.id })}
              onMenu={(x, y) => setMenu({ c, x, y })}
              renaming={renaming === c.id}
              onRename={async (name) => {
                setRenaming("");
                if (name === null) return;
                await RenameChatSession(c.session, name);
                renamePinned(c.id, name || c.title);
                load();
              }} />
          ))}
        </div>
      </aside>
      {menu && <RowMenu c={menu.c} x={menu.x} y={menu.y} main={menu.c.kind === "queen" && menu.c.session === consoleId}
        pinned={pinned.some((p) => p.id === menu.c.id)} onClose={() => setMenu(null)}
        onRename={() => { setRenaming(menu.c.id); setMenu(null); }}
        onDelete={async () => {
          const c = menu.c;
          setMenu(null);
          await DeleteChatSession(c.session);
          unpin(c.id);
          if (openId === c.id) set({ open: "" });
          load();
        }} />}
      <section className="cv2-thread">
        {openId ? (
          <Thread key={openId} id={openId} onBack={() => set({ open: "" })} onOpen={(id) => set({ open: id })} onChanged={load} />
        ) : (
          <div className="cv2-none">{t("Pick a conversation")}</div>
        )}
      </section>
    </div>
  );
}

/** Only the person's own queen conversations are kept by the core, so only those can be renamed or deleted. */
const keptByCore = (c: Conv, main: boolean) => c.kind === "queen" && !main;

function Row({ c, on, main, pinned, onOpen, onMenu, renaming, onRename }: {
  c: Conv; on: boolean; main: boolean; pinned: boolean; onOpen: () => void; onMenu: (x: number, y: number) => void;
  renaming: boolean; onRename: (name: string | null) => void;
}) {
  const { t } = useI18n();
  const who = useWho();
  const Icon = KIND[c.kind]?.Icon ?? BotIcon;
  const [name, setName] = useState(c.title);
  useEffect(() => { if (renaming) setName(withoutCallNotes(c.title)); }, [renaming, c.title]);
  const body = (
    <>
      <span className="cv2-ico"><Icon size={15} /></span>
      <span className="cv2-main">
        <span className="cv2-top">
          <b>{who.name(c)}</b>
          {c.machine && <i>{bare(c.machine)}</i>}
          <span className="cv2-kind">{who.kind(c)}</span>
          {pinned && <PinIcon size={11} className="cv2-pin" />}
          <em>{c.state === "running" ? <span className="cv2-live" title={t("Working…")} /> : when(c.at)}</em>
        </span>
        {renaming ? (
          <input className="cv2-rename" autoFocus value={name} aria-label={t("Rename")} onChange={(e) => setName(e.target.value)}
            onFocus={(e) => e.currentTarget.select()}
            onKeyDown={(e) => { if (e.key === "Enter" && !e.nativeEvent.isComposing) onRename(name); if (e.key === "Escape") onRename(null); }}
            onBlur={() => onRename(name)} data-testid="conv-rename" />
        ) : (
          <span className="cv2-title">{main ? t("Main conversation") : withoutCallNotes(c.title) || t("Untitled")}</span>
        )}
        {c.last && <span className="cv2-last">{withoutCallNotes(c.last)}</span>}
      </span>
    </>
  );
  return (
    <div className={`cv2-rowwrap${on ? " on" : ""}`} onContextMenu={(e) => { e.preventDefault(); onMenu(e.clientX, e.clientY); }}>
      {renaming ? <div className={`cv2-row k-${c.kind}`}>{body}</div> : (
        <button className={`cv2-row k-${c.kind}${on ? " on" : ""}`} onClick={onOpen} data-testid="conv-row">{body}</button>
      )}
      {!renaming && (
        <button className="cv2-more" title={t("More")} aria-label={t("More")} data-testid="conv-more"
          onClick={(e) => { e.stopPropagation(); const r = e.currentTarget.getBoundingClientRect(); onMenu(r.right, r.bottom + 4); }}>
          <MoreHorizontalIcon size={15} />
        </button>
      )}
    </div>
  );
}

function RowMenu({ c, x, y, main, pinned, onClose, onRename, onDelete }: {
  c: Conv; x: number; y: number; main: boolean; pinned: boolean; onClose: () => void; onRename: () => void; onDelete: () => void;
}) {
  const { t } = useI18n();
  const [confirm, setConfirm] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const key = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    const away = (e: Event) => { if (!ref.current?.contains(e.target as Node)) onClose(); };
    window.addEventListener("keydown", key);
    window.addEventListener("mousedown", away, true);
    window.addEventListener("contextmenu", away, true);
    window.addEventListener("blur", onClose);
    return () => {
      window.removeEventListener("keydown", key);
      window.removeEventListener("mousedown", away, true);
      window.removeEventListener("contextmenu", away, true);
      window.removeEventListener("blur", onClose);
    };
  }, [onClose]);
  const left = Math.max(8, Math.min(x - 180, window.innerWidth - 196));
  const top = Math.max(8, Math.min(y, window.innerHeight - 140));
  const kept = keptByCore(c, main);
  return (
    <div className="cv2-menu" ref={ref} role="menu" style={{ left, top }} data-testid="conv-menu">
      <button role="menuitem" onClick={() => { togglePin({ id: c.id, kind: c.kind, session: c.session, title: main ? "" : withoutCallNotes(c.title) }); onClose(); }}>
        {pinned ? <PinOffIcon size={14} /> : <PinIcon size={14} />}{pinned ? t("Unpin") : t("Pin to sidebar")}
      </button>
      {kept && <button role="menuitem" onClick={onRename}><PencilIcon size={14} />{t("Rename")}</button>}
      {kept && (
        <button role="menuitem" className={confirm ? "danger arm" : "danger"} onClick={() => (confirm ? onDelete() : setConfirm(true))}>
          <Trash2Icon size={14} />{confirm ? t("Delete this conversation?") : t("Delete")}
        </button>
      )}
    </div>
  );
}

export function Thread({ id, onBack, onOpen, onChanged }: { id: string; onBack: () => void; onOpen: (id: string) => void; onChanged: () => void }) {
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
        <button className="cv2-back" onClick={onBack} title={t("Back")} aria-label={t("Back")}><ArrowLeftIcon size={17} /></button>
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
