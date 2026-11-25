import React, { useCallback, useEffect, useRef, useState } from "react";
import { BellIcon, BellRingIcon, ClockIcon, EllipsisIcon, MessageSquareIcon, PlayIcon, RotateCwIcon, SquareIcon, Trash2Icon, WorkflowIcon } from "lucide-react";
import "./automation.css";
import {
  CancelScheduledRun,
  DeleteScheduledPrompt,
  RunScheduledPromptNow,
  ScheduledPrompts,
  SetScheduledPromptEnabled,
} from "../../wailsjs/go/app/App";
import { agent } from "../../wailsjs/go/models";
import { AppStatus } from "../lib/types";
import { describeCron } from "../lib/cron";
import { firstLine, formatStamp, fromNow, parseTime } from "../lib/format";
import { ScheduleRunLog } from "../lib/useScheduleRuns";
import { ScheduleRunList } from "../components/ScheduleRuns";
import { notifyNow, notifyPermission, requestNotifyPermission } from "../lib/notify";
import { toast } from "../lib/toasts";
import { useI18n } from "../lib/i18n";
import { Skeleton, SkeletonLines, SkeletonRows, SkeletonCards } from "../components/Skeleton";

type Note = { kind: "ok" | "err"; text: string } | null;

/**
 * Scheduled prompts: what SuperLeo will do on its own, and what it did.
 *
 * Every mutating binding answers with a message string rather than throwing, and
 * the message is the whole point — the backend refuses a schedule it cannot parse
 * and says why, instead of accepting it and silently never firing. So each action
 * routes its reply into the note line, "ok" included.
 */
export default function SchedulesView({
  status,
  log,
  onOpenConversation,
  embedded = false,
  onCount,
}: {
  status: AppStatus | null;
  log: ScheduleRunLog;
  onOpenConversation: (session: string) => void;
  /** Rendered as a tab inside Records: drop the page chrome, keep the actions.
   *  The tab and the page around it already say what this is. */
  embedded?: boolean;
  /** Report the list length so the tab that holds this can count it. Only the
   *  scheduler knows the real number; Life() reports its own view of the same
   *  thing and the two would drift. */
  onCount?: (n: number) => void;
}) {
  const { t } = useI18n();
  const [list, setList] = useState<agent.ScheduledPrompt[]>([]);
  const [loading, setLoading] = useState(true);
  const [note, setNote] = useState<Note>(null);
  const [busy, setBusy] = useState("");
  const [confirmDelete, setConfirmDelete] = useState("");
  const [menuFor, setMenuFor] = useState("");
  useEffect(() => {
    if (!menuFor) return;
    const away = (e: MouseEvent) => {
      if (!(e.target as HTMLElement).closest(".au-more-wrap")) { setMenuFor(""); setConfirmDelete(""); }
    };
    window.addEventListener("mousedown", away);
    return () => window.removeEventListener("mousedown", away);
  }, [menuFor]);
  // Folded to one line each; the prompt can run to a page.
  const [open, setOpen] = useState<Set<string>>(() => new Set());
  const toggle = (id: string) =>
    setOpen((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  // Whether this browser will draw a banner when a run finishes while the page
  // is in the background. Read once: it only changes when the button below is
  // pressed, or in browser settings, which reloads anyway.
  const [banners, setBanners] = useState(notifyPermission);

  const load = useCallback(async (quiet = false) => {
    if (!quiet) setLoading(true);
    try {
      const res = await ScheduledPrompts();
      setList(Array.isArray(res) ? res : []);
    } catch (e: any) {
      if (!quiet) toast.error(String(e?.message || e));
    } finally {
      if (!quiet) setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  // "Run now" returns as soon as the run has started, so the row's state comes
  // from the backend's own `running` flag rather than from the click. Poll only
  // while something is in flight: a schedule list at rest changes once an hour,
  // and a spinner that never stops is worse than no spinner at all.
  const anyRunning = list.some((s) => s.running);
  useEffect(() => {
    if (!anyRunning) return;
    const t = window.setInterval(() => load(true), 1500);
    return () => window.clearInterval(t);
  }, [anyRunning, load]);

  // Being on this page is what "I have read the runs" means, so the sidebar
  // badge and any toasts clear on arrival and stay clear while it is open.
  useEffect(() => {
    if (log.unseen > 0 || log.toasts.length > 0) log.acknowledge();
  }, [log]);

  // A run that just finished changed last_run, and may have moved next_run on.
  // Keyed on the newest run rather than the count, which stops changing once the
  // log is full.
  const newestRun = log.runs.length > 0 ? log.runs[0].key : "";
  const refreshedFor = useRef(newestRun);
  useEffect(() => {
    if (newestRun === refreshedFor.current) return;
    refreshedFor.current = newestRun;
    load();
  }, [newestRun, load]);

  useEffect(() => {
    onCount?.(list.length);
  }, [list.length, onCount]);

  // opts.refusalIsFine covers Stop: a stop that lands after the run has already
  // finished is late, not wrong, and the backend says so in words. Painting
  // that reply red would teach the user that the button is broken.
  const act = async (
    id: string,
    label: string,
    call: () => Promise<string>,
    opts: { refusalIsFine?: boolean } = {},
  ) => {
    setBusy(id);
    setNote(null);
    setConfirmDelete("");
    try {
      const res = await call();
      if (res === "ok") toast.success(`${label} done.`);
      else if (opts.refusalIsFine) toast.info(res);
      else toast.error(res);
    } catch (e: any) {
      toast.error(String(e?.message || e));
    } finally {
      setBusy("");
      load();
    }
  };

  // A missing "scheduler" key is read as running, so a backend that stops
  // reporting it cannot produce a false alarm here.
  const schedulerDown = status !== null && status.ready && !status.scheduler;

  const actions = (
    <div className="au-head-actions">
      {/* Asked for from a click, never on load: an unprompted permission
          dialog is what teaches people to press "block". */}
      {banners === "default" && (
        <button
          className="au-icon"
          title={t("Enable notifications")}
          aria-label={t("Enable notifications")}
          onClick={async () => {
            const granted = await requestNotifyPermission();
            setBanners(granted);
            if (granted === "granted") notifyNow("SuperLeo", "通知已开启 —— 定时任务跑完时会这样提醒你。");
          }}
        >
          <BellIcon size={16} />
        </button>
      )}
      {banners === "granted" && (
        <button
          className="au-icon"
          title={t("Post a test notification now")}
          aria-label={t("Post a test notification now")}
          onClick={() => {
            const result = notifyNow("SuperLeo", "这是一条测试通知。定时任务跑完时就是这个样子。");
            if (result !== "posted") toast.error(`通知没能发出（${result}）。`);
          }}
        >
          <BellRingIcon size={16} />
        </button>
      )}
      <button className="au-icon" title={t("Refresh")} aria-label={t("Refresh")} onClick={() => load()} disabled={loading}>
        <RotateCwIcon size={16} className={loading ? "au-spin" : ""} />
      </button>
    </div>
  );

  const body = (
    <>
        {banners === "denied" && (
          <div className="save-note" style={{ display: "block", marginBottom: 12 }}>
            The browser is blocking notifications. Schedules still run, but nothing pops up
            while this tab is in the background. To allow them, open the site settings left of
            the address bar and set Notifications to Allow.
          </div>
        )}

        {note && (
          <div className={`save-note ${note.kind}`} style={{ display: "block", marginBottom: 12 }}>
            {note.kind === "ok" ? "✓" : "⚠"} {note.text}
          </div>
        )}

        {schedulerDown && (
          <div className="report-error" style={{ marginBottom: 12 }}>
            ⚠ The scheduler is not running{status?.schedulerError ? ` — ${status.schedulerError}` : ""}.
            Existing schedules are listed but nothing will fire.
          </div>
        )}

        {loading && list.length === 0 && (
          <SkeletonCards n={3} h={88} />
        )}

        {!loading && list.length === 0 && (
          <div className="inline-empty">
            <WorkflowIcon className="ie-icon" size={22} strokeWidth={1.6} />
            <div>{t("No scheduled tasks.")}</div>
          </div>
        )}

        {list.length > 0 && (
          <div className="au-list">
            {list.map((s) => {
              const next = parseTime(s.next_run);
              const last = parseTime(s.last_run);
              const words = describeCron(s.schedule);
              const title = (s.note || "").trim() || firstLine(s.prompt, 70);
              // `acting` is a binding call in flight (milliseconds), `running`
              // is the agent turn itself (minutes).
              const acting = busy === s.id;
              const running = !!s.running;
              const isOpen = open.has(s.id);
              const menuOpen = menuFor === s.id;
              return (
                <div className={`au-card${s.enabled ? "" : " off"}${isOpen ? " open" : ""}`} key={s.id}>
                  <div
                    className="au-row"
                    role="button"
                    tabIndex={0}
                    aria-expanded={isOpen}
                    onClick={() => toggle(s.id)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" || e.key === " ") { e.preventDefault(); toggle(s.id); }
                    }}
                  >
                    <span className={`au-badge${running ? " run" : ""}`}>
                      {running ? <span className="au-pulse" /> : <ClockIcon size={18} strokeWidth={1.8} />}
                    </span>
                    <span className="au-main">
                      <span className="au-title">{title}</span>
                      <span className="au-sub">
                        {running ? t("Running") : (
                          <>
                            {words || s.schedule}
                            {s.enabled && next ? <><span className="au-dot">·</span>{fromNow(next)}</> : null}
                            {!s.enabled && <><span className="au-dot">·</span>{t("Paused")}</>}
                          </>
                        )}
                      </span>
                    </span>
                    <span className="au-actions" onClick={(e) => e.stopPropagation()}>
                      {running ? (
                        <button
                          className="au-run stop"
                          disabled={acting}
                          title={t("Stop this run")}
                          onClick={() => act(s.id, "Stop", () => CancelScheduledRun(s.id), { refusalIsFine: true })}
                        >
                          <SquareIcon size={12} className="fill-current" /> {t("Stop")}
                        </button>
                      ) : (
                        <button
                          className="au-run"
                          disabled={acting}
                          title={t("Run this prompt immediately")}
                          onClick={() => act(s.id, "Run", () => RunScheduledPromptNow(s.id))}
                        >
                          {acting ? <span className="au-mini-spin" /> : <PlayIcon size={13} className="fill-current" />} {t("Run now")}
                        </button>
                      )}
                      <button
                        className={`au-switch${s.enabled ? " on" : ""}`}
                        role="switch"
                        aria-checked={s.enabled}
                        disabled={acting}
                        title={s.enabled ? t("Pause") : t("Resume")}
                        onClick={() => act(s.id, s.enabled ? "Pause" : "Resume", () => SetScheduledPromptEnabled(s.id, !s.enabled))}
                      >
                        <span />
                      </button>
                      <span className="au-more-wrap">
                        <button
                          className="au-icon sm"
                          aria-label={t("More")}
                          aria-haspopup="menu"
                          aria-expanded={menuOpen}
                          onClick={() => { setMenuFor(menuOpen ? "" : s.id); setConfirmDelete(""); }}
                        >
                          <EllipsisIcon size={16} />
                        </button>
                        {menuOpen && (
                          <div className="au-menu" role="menu">
                            {confirmDelete === s.id ? (
                              <div className="au-confirm">
                                <div className="au-confirm-q">{t("Delete this automation?")}</div>
                                <div className="au-confirm-btns">
                                  <button onClick={() => { setConfirmDelete(""); setMenuFor(""); }}>{t("Cancel")}</button>
                                  <button
                                    className="danger"
                                    onClick={() => { setMenuFor(""); act(s.id, "Delete", () => DeleteScheduledPrompt(s.id)); }}
                                  >
                                    {t("Delete")}
                                  </button>
                                </div>
                              </div>
                            ) : (
                              <>
                                {!!s.session && (
                                  <button role="menuitem" onClick={() => { setMenuFor(""); onOpenConversation(s.session!); }}>
                                    <MessageSquareIcon size={14} /> {t("Conversation")}
                                  </button>
                                )}
                                <button role="menuitem" className="danger" onClick={() => setConfirmDelete(s.id)}>
                                  <Trash2Icon size={14} /> {t("Delete")}
                                </button>
                              </>
                            )}
                          </div>
                        )}
                      </span>
                    </span>
                  </div>
                  {isOpen && (
                    <div className="au-detail">
                      <dl>
                        <dt>{t("When")}</dt>
                        <dd>{words !== "" && <>{words} </>}<code>{s.schedule}</code></dd>
                        <dt>{t("Next run")}</dt>
                        <dd>{!s.enabled ? "—" : next ? `${formatStamp(next)} · ${fromNow(next)}` : "—"}</dd>
                        {last && (<><dt>{t("Last run")}</dt><dd>{formatStamp(last)} · {fromNow(last)}</dd></>)}
                      </dl>
                      <div className="au-prompt">{s.prompt}</div>
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}

      <ScheduleRunList log={log} onOpenConversation={onOpenConversation} />
    </>
  );

  if (embedded) {
    return (
      <>
        <div className="tab-actions">{actions}</div>
        {body}
      </>
    );
  }

  return (
    <div className="view">
      <div className="view-header with-action">
        <div>
          <div className="view-title">{t("Automation")}</div>
        </div>
        {actions}
      </div>
      <div className="panel-scroll">{body}</div>
    </div>
  );
}
