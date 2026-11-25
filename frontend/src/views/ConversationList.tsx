import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { BotIcon, CpuIcon, HexagonIcon, MoreHorizontalIcon, PencilIcon, PinIcon, PinOffIcon, SearchIcon, TerminalIcon, Trash2Icon, XIcon, type LucideIcon } from "lucide-react";
import { Conversations, DeleteChatSession, RenameChatSession } from "../../wailsjs/go/app/App";
import { app } from "../../wailsjs/go/models";
import { EventsOn } from "../../wailsjs/runtime";
import { useI18n } from "../lib/i18n";
import { withoutCallNotes } from "../lib/format";
import { queenTitle, useQueenName } from "../lib/queenName";
import { isLegacyConsole, legacyConsoleTitle, pairedDeviceNames, useQueenSession } from "../lib/queenConsole";
import { renamePinned, togglePin, unpin, usePinnedConvs } from "../lib/pinnedConvs";
import "./conversations.css";
import { SkeletonRows } from "../components/Skeleton";

export type Conv = app.Conversation;

export const KIND: Record<string, { Icon: LucideIcon; label: string }> = {
  queen: { Icon: HexagonIcon, label: "Queen" },
  worker: { Icon: CpuIcon, label: "Worker" },
  agent: { Icon: BotIcon, label: "Agent" },
  coding: { Icon: TerminalIcon, label: "Coding" },
};

/** The time of the last word: the clock today, the day before that. */
export function when(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  const now = new Date();
  if (d.toDateString() === now.toDateString()) return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
  if (d.getFullYear() === now.getFullYear()) return d.toLocaleDateString([], { month: "numeric", day: "numeric" });
  return d.toLocaleDateString();
}

export const bare = (m?: string) => (m ?? "").replace(/^https?:\/\//, "").replace(/\/+$/, "");

/** Who a row is with, and where. */
export function useWho() {
  const { t } = useI18n();
  const q = useQueenName();
  return useMemo(() => ({
    name: (c: { kind: string; name?: string }) => (c.kind === "queen" ? queenTitle(q) : c.name || t(KIND[c.kind]?.label ?? "Agent")),
    kind: (c: { kind: string; source?: string }) => t(c.source === "terminal" ? "Terminal" : KIND[c.kind]?.label ?? "Agent"),
  }), [q, t]);
}

/** Only the person's own queen conversations are kept by the core, so only those can be renamed or deleted. */
const keptByCore = (c: Conv, main: boolean) => c.kind === "queen" && !main;

function Row({ c, on, main, pinned, compact, onOpen, onMenu, renaming, onRename }: {
  c: Conv; on: boolean; main: boolean; pinned: boolean; compact?: boolean; onOpen: () => void; onMenu: (x: number, y: number) => void;
  renaming: boolean; onRename: (name: string | null) => void;
}) {
  const { t } = useI18n();
  const who = useWho();
  const Icon = KIND[c.kind]?.Icon ?? BotIcon;
  const [name, setName] = useState(c.title);
  useEffect(() => { if (renaming) setName(withoutCallNotes(c.title)); }, [renaming, c.title]);
  const title = main ? t("Main conversation") : withoutCallNotes(c.title) || t("Untitled");
  const stamp = c.state === "running" ? <span className="cv2-live" title={t("Working…")} /> : when(c.at);
  const rename = (
    <input className="cv2-rename" autoFocus value={name} aria-label={t("Rename")} onChange={(e) => setName(e.target.value)}
      onFocus={(e) => e.currentTarget.select()}
      onKeyDown={(e) => { if (e.key === "Enter" && !e.nativeEvent.isComposing) onRename(name); if (e.key === "Escape") onRename(null); }}
      onBlur={() => onRename(name)} data-testid="conv-rename" />
  );
  // Compact (the chat sidebar): the title first with the time at the right,
  // who it is with underneath.
  const body = compact ? (
    <>
      <span className="cv2-ico"><Icon size={14} /></span>
      <span className="cv2-main">
        <span className="cv2-top">
          {renaming ? rename : <b className="cv2-ttl" title={title}>{title}</b>}
          {pinned && <PinIcon size={11} className="cv2-pin" />}
          {!renaming && <em>{stamp}</em>}
        </span>
        <span className="cv2-sub">{[who.name(c), c.machine ? bare(c.machine) : "", who.kind(c)].filter(Boolean).join(" · ")}</span>
      </span>
    </>
  ) : (
    <>
      <span className="cv2-ico"><Icon size={15} /></span>
      <span className="cv2-main">
        <span className="cv2-top">
          <b>{who.name(c)}</b>
          {c.machine && <i>{bare(c.machine)}</i>}
          <span className="cv2-kind">{who.kind(c)}</span>
          {pinned && <PinIcon size={11} className="cv2-pin" />}
          <em>{stamp}</em>
        </span>
        {renaming ? rename : <span className="cv2-title">{title}</span>}
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

function RowMenu({ c, x, y, main, pinned, minLeft = 8, onClose, onRename, onDelete }: {
  c: Conv; x: number; y: number; main: boolean; minLeft?: number; pinned: boolean; onClose: () => void; onRename: () => void; onDelete: () => void;
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
  const left = Math.max(minLeft, Math.min(x - 180, window.innerWidth - 196));
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


/**
 * Every conversation, with who/search above it: the queen's, the workers',
 * the agents' and the coding runs'. `compact` is the chat sidebar's form: one
 * scrolling row of chips and two-line rows. The page that holds it decides
 * what opening one does.
 */
export function ConversationList({ openId, onOpen, onDeleted, compact, hideMain, refresh, className }: {
  openId: string;
  onOpen: (c: Conv) => void;
  onDeleted?: (c: Conv) => void;
  compact?: boolean;
  /** Leave out the person's main conversation, which the page shows elsewhere. */
  hideMain?: boolean;
  /** Changes when the open conversation said something: read the list again. */
  refresh?: number;
  className?: string;
}) {
  const { t } = useI18n();
  const who = useWho();
  const [withKey, setWithKey] = useState("");
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
  useEffect(() => { load(); }, [load, refresh]);
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

  const isMain = (c: Conv) => c.kind === "queen" && c.session === consoleId;
  const items = (data?.items ?? []).filter((c) => !(hideMain && isMain(c)));
  const parties = data?.parties ?? [];
  const total = parties.reduce((n, p) => n + p.count, 0);

  return (
    <div className={`cv2-listbox${compact ? " cv2-compact" : ""}${className ? ` ${className}` : ""}`}>
      <header className="cv2-head">
        {compact ? <div className="dk-sec">{t("Conversations")}</div> : <h1>{t("Conversations")}</h1>}
        <label className="cv2-search">
          <SearchIcon size={14} />
          <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t("Search")} aria-label={t("Search")} data-testid="conv-search" />
          {query && <button type="button" title={t("Clear")} aria-label={t("Clear")} onClick={() => setQuery("")}><XIcon size={13} /></button>}
        </label>
        <div className="cv2-who" role="tablist" aria-label={t("Who")}>
          <button role="tab" aria-selected={!withKey} className={!withKey ? "on" : ""} onClick={() => setWithKey("")} data-testid="who-all">
            {t("Everyone")} <i>{total}</i>
          </button>
          {parties.map((p) => {
            const Icon = KIND[p.kind]?.Icon ?? BotIcon;
            return (
              <button key={p.with} role="tab" aria-selected={withKey === p.with} className={withKey === p.with ? "on" : ""}
                onClick={() => setWithKey(withKey === p.with ? "" : p.with)} title={bare(p.machine)} data-testid="who-chip">
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
          <Row key={c.id} c={c} on={openId === c.id} main={isMain(c)} compact={compact}
            pinned={pinned.some((p) => p.id === c.id)}
            onOpen={() => onOpen(c)}
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
      {menu && <RowMenu c={menu.c} x={menu.x} y={menu.y} main={isMain(menu.c)} minLeft={compact ? 88 : 8}
        pinned={pinned.some((p) => p.id === menu.c.id)} onClose={() => setMenu(null)}
        onRename={() => { setRenaming(menu.c.id); setMenu(null); }}
        onDelete={async () => {
          const c = menu.c;
          setMenu(null);
          await DeleteChatSession(c.session);
          unpin(c.id);
          onDeleted?.(c);
          load();
        }} />}
    </div>
  );
}
