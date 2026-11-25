import { translate, useI18n } from "../lib/i18n";
import React, { useEffect, useRef, useState } from "react";
import {
  AlertTriangleIcon,
  BellIcon,
  CheckIcon,
  InfoIcon,
  MessageSquareIcon,
  XCircleIcon,
} from "lucide-react";
import {
  Notification,
  useNotificationList,
  useUnreadNotifications,
} from "../lib/notifications";
import { attentionPath, deeplinkPath } from "../lib/routes";
import { AttentionItem, attentionTitle } from "../canvas/attention";
import { ToolApproval } from "../lib/useToolApprovals";
import { SkeletonRows } from "./Skeleton";

/** What needs a decision now: shown above the notices, not on a page of its own. */
export type NeedsYou = {
  items: AttentionItem[];
  approvals: ToolApproval[];
  onResolve?: (id: string, allow: boolean) => void;
};

const NEEDS_LABEL: Record<string, string> = {
  approval: "Approval required",
  bee: "Waiting for your reply",
  report: "From a bee",
  failed: "Task failed",
  lost: "Worker offline",
  run: "Coding run failed",
};

/**
 * What SuperLeo said while you were not looking.
 *
 * The bell lives in the status bar because that is the one strip on screen in
 * every view, and a notification centre you have to navigate to is one you
 * check after you already found out the hard way. The badge is the whole
 * feature: the panel is only what you open once the badge has told you there is
 * something to open.
 *
 * Opening marks everything read, and the rows that were unread keep their mark
 * until the panel closes — otherwise opening the panel erases the very thing
 * you opened it to find.
 */

const ICONS: Record<Notification["level"], typeof InfoIcon> = {
  info: InfoIcon,
  success: CheckIcon,
  warn: AlertTriangleIcon,
  error: XCircleIcon,
};

/** "3m", "2h", "yesterday" — a centre is read by recency, not by timestamp. */
function ago(at: string): string {
  const t = new Date(at).getTime();
  if (!Number.isFinite(t)) return "";
  const secs = Math.max(0, Math.round((Date.now() - t) / 1000));
  if (secs < 60) return translate("just now");
  if (secs < 3600) return translate("{count}m ago", {count:Math.floor(secs/60)});
  if (secs < 86400) return translate("{count}h ago", {count:Math.floor(secs/3600)});
  if (secs < 172800) return translate("yesterday");
  return translate("{count}d ago", {count:Math.floor(secs/86400)});
}

export default function NotificationCenter({
  onOpenConversation,
  variant = "default",
  needs,
}: {
  variant?: "default" | "desk";
  onOpenConversation?: (session: string) => void;
  needs?: NeedsYou;
}) {
  const { t } = useI18n();
  const unread = useUnreadNotifications();
  const [open, setOpen] = useState(false);
  const { items, loading, loaded, error, reload, markAllRead, clear } = useNotificationList(open);
  const wrap = useRef<HTMLDivElement | null>(null);
  const order = ["approval", "bee", "report", "failed", "run", "lost"];
  const needItems = (needs?.items ?? [])
    .filter((i) => i.level === "needs" && i.kind !== "reminder")
    .sort((a, b) => (order.includes(a.kind) ? order.indexOf(a.kind) : 99) - (order.includes(b.kind) ? order.indexOf(b.kind) : 99));
  const badge = unread + needItems.length;
  const openNeed = (it: AttentionItem) => {
    if (it.kind === "approval") {
      const a = needs?.approvals.find((x) => x.id === it.ref);
      if (a?.session && onOpenConversation) onOpenConversation(a.session);
    } else window.location.hash = "#" + attentionPath(it);
    setOpen(false);
  };

  // Where the panel hangs. Measured rather than written in CSS: the status bar
  // wraps to a second row when the pills do not fit, and the bell is not the
  // rightmost thing in it — a panel anchored to the bell's right edge runs off
  // the left of a phone screen.
  const [pos, setPos] = useState<{ top?: number; bottom?: number; left: number; width: number }>({ top: 0, left: 0, width: 340 });
  useEffect(() => {
    if (!open) return;
    const place = () => {
      const bell = wrap.current?.querySelector(".notif-bell");
      if (!bell) return;
      const r = bell.getBoundingClientRect();
      const width = Math.min(340, window.innerWidth - 16);
      const left = Math.min(Math.max(8, r.right - width), window.innerWidth - width - 8);
      // A bell in the lower half (the desk keeps it above the bottom buttons)
      // opens its panel upward.
      if (r.top > window.innerHeight / 2) setPos({ bottom: window.innerHeight - r.top + 8, left, width });
      else setPos({ top: r.bottom + 8, left, width });
    };
    place();
    window.addEventListener("resize", place);
    return () => window.removeEventListener("resize", place);
  }, [open, variant]);

  // Which rows to keep showing as new for as long as this panel is open. Taken
  // once per opening, before the read-marking lands.
  const [fresh, setFresh] = useState<Set<string>>(new Set());
  useEffect(() => {
    if (!open) {
      setFresh(new Set());
      return;
    }
    if (loading) return;
    const unreadIds = items.filter(n => !n.read).map(n => n.id);
    if (!unreadIds.length) return;
    setFresh(current => new Set([...current, ...unreadIds]));
    void markAllRead(unreadIds);
  }, [open, loading, items, markAllRead]);

  // Click-away and Escape. A panel that hangs over the app until you find its
  // button again is a panel people stop opening.
  useEffect(() => {
    if (!open) return;
    const away = (e: MouseEvent) => {
      if (wrap.current && !wrap.current.contains(e.target as Node)) setOpen(false);
    };
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", away);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("mousedown", away);
      document.removeEventListener("keydown", key);
    };
  }, [open]);

  return (
    <div className={`notif${variant === "desk" ? " dk-notif" : ""}`} ref={wrap} style={variant === "desk" ? { "--wails-draggable": "no-drag" } as React.CSSProperties : undefined}>
      <button
        type="button"
        className={`${variant === "desk" ? "dk-icon dk-bell" : "pill-btn icon-only"} notif-bell${open ? " on" : ""}`}
        onClick={() => setOpen((v) => !v)}
        title={badge > 0 ? t("{count} unread",{count:badge}) : t("Notifications")}
        aria-label={t("Notifications")}
        aria-expanded={open}
      >
        <BellIcon size={variant === "desk" ? 18 : 16} />
        {badge > 0 && <span className={`notif-badge${needItems.length ? " notif-badge-needs" : ""}`}>{badge > 99 ? "99+" : badge}</span>}
      </button>

      {open && (
        <div
          className="notif-panel"
          role="dialog"
          aria-label={t("Notifications")}
          style={{ top: pos.top, bottom: pos.bottom, left: pos.left, width: pos.width }}
        >
          <div className="notif-head">
            <span className="notif-title">{t("Notifications")}</span>
            {items.length > 0 && (
              <button className="notif-action" onClick={() => void clear()}>
                {t("Clear all")}
              </button>
            )}
          </div>
          {error && <div className="notif-empty" role="alert">{t(error)} <button className="notif-action" onClick={() => void reload()}>{t("Retry")}</button></div>}
          <div className="notif-list">
            {needItems.map((it, i) => {
              const approval = it.kind === "approval" ? needs?.approvals.find((a) => a.id === it.ref) : undefined;
              return (
                <div key={`need-${it.kind}-${it.ref}-${i}`} className="notif-row notif-warn notif-new notif-need">
                  <AlertTriangleIcon size={14} className="notif-icon" />
                  <div className="notif-text">
                    <div className="notif-row-title">{t(NEEDS_LABEL[it.kind] ?? "Needs attention")}</div>
                    <div className="notif-message">{attentionTitle(it)}</div>
                    {approval?.command && <pre className="notif-cmd">{approval.command}</pre>}
                    <div className="notif-need-actions">
                      {approval && needs?.onResolve ? (
                        <>
                          <button className="notif-action" onClick={() => needs.onResolve!(approval.id, true)}>{t("Allow once")}</button>
                          <button className="notif-action" onClick={() => needs.onResolve!(approval.id, false)}>{t("Deny")}</button>
                        </>
                      ) : (
                        <button className="notif-action" onClick={() => openNeed(it)}>{t("Open")}</button>
                      )}
                    </div>
                  </div>
                </div>
              );
            })}
            {items.length === 0 && !error && needItems.length === 0 && (
              !loaded ? <SkeletonRows n={3} /> : <div className="notif-empty">{t("Nothing yet.")}</div>
            )}
            {items.map((n) => {
              const Icon = ICONS[n.level] ?? InfoIcon;
              // A conversation's notice opens it; a schedule notice opens 日程安排.
              const path = n.session && onOpenConversation ? "" : deeplinkPath(n.deeplink);
              const go = () => {
                if (path) window.location.hash = "#" + path;
                else onOpenConversation!(n.session!);
                setOpen(false);
              };
              const clickable = Boolean((n.session && onOpenConversation) || path);
              return (
                <div
                  key={n.id}
                  className={
                    `notif-row notif-${n.level}` +
                    (fresh.has(n.id) ? " notif-new" : "") +
                    (clickable ? " notif-clickable" : "")
                  }
                  role={clickable ? "button" : undefined}
                  tabIndex={clickable ? 0 : undefined}
                  onKeyDown={clickable ? e => {
                    if (e.key === "Enter" || e.key === " ") { e.preventDefault(); go(); }
                  } : undefined}
                  onClick={
                    clickable ? go : undefined
                  }
                >
                  <Icon size={14} className="notif-icon" />
                  <div className="notif-text">
                    {n.title && <div className="notif-row-title">{n.title}</div>}
                    <div className="notif-message">{n.message}</div>
                    <div className="notif-meta">
                      {n.source && (
                        <span className="notif-source">
                          <MessageSquareIcon size={10} />
                          {n.source}
                        </span>
                      )}
                      <span className="notif-time">{ago(n.at)}</span>
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}
