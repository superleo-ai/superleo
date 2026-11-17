import React, { useCallback, useEffect, useRef, useState } from "react";
import { AlarmClockIcon, ChevronRightIcon, SquareIcon } from "lucide-react";
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
    <div className="vh-actions">
      {/* Asked for from a click, never on load: an unprompted permission
          dialog is what teaches people to press "block", and a blocked
          notification cannot be asked for again from script. */}
      {banners === "default" && (
        <button
          className="btn ghost sm"
          title={t("Show a system notification when a scheduled run finishes while this tab is in the background")}
          onClick={async () => {
            const granted = await requestNotifyPermission();
            setBanners(granted);
            // One banner the moment it is allowed. Being told "notifications
            // are on" is not the same as having seen one arrive, and the
            // difference is where Focus modes and full-screen apps hide.
            if (granted === "granted") {
              notifyNow("SuperLeo", "通知已开启 —— 定时任务跑完时会这样提醒你。");
            }
          }}
        >
          🔔 Enable notifications
        </button>
      )}
      {banners === "granted" && (
        <button
          className="btn ghost sm"
          title={t("Post a test notification now")}
          onClick={() => {
            const result = notifyNow("SuperLeo", "这是一条测试通知。定时任务跑完时就是这个样子。");
            setNote(
              result === "posted"
                ? {
                    kind: "ok",
                    text: "已发出一条测试通知。没看见的话，多半是系统的「专注模式」把它收进了通知中心。",
                  }
                : { kind: "err", text: `通知没能发出（${result}）。` },
            );
          }}
        >
          🔔 试一下
        </button>
      )}
      {/* Wrapped rather than passed directly: onClick would hand load()
          the click event as its `quiet` argument. */}
      <button className="btn ghost sm" onClick={() => load()} disabled={loading}>
        {loading ? (
          <>
            <span className="spinner" style={{ borderTopColor: "var(--text-1)" }} /> {t("Loading…")}
          </>
        ) : (
          `↻ ${t("Refresh")}`
        )}
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
            <AlarmClockIcon className="ie-icon" size={22} strokeWidth={1.6} />
            <div>{t("No scheduled tasks.")}</div>
          </div>
        )}

        {list.length > 0 && (
          <div className="record-list">
            {list.map((s) => {
              const next = parseTime(s.next_run);
              const last = parseTime(s.last_run);
              const words = describeCron(s.schedule);
              const title = (s.note || "").trim() || firstLine(s.prompt, 70);
              // Two different things, kept apart on purpose: `acting` is a
              // binding call in flight (milliseconds), `running` is the agent
              // turn itself (minutes). Conflating them is what made "Run now"
              // look finished the moment the click returned.
              const acting = busy === s.id;
              const running = !!s.running;
              const isOpen = open.has(s.id);
              return (
                <div
                  className={`record-card${s.enabled ? "" : " paused"}`}
                  key={s.id}
                >
                  <div
                    className="rc-title sched-head"
                    style={{ display: "flex", alignItems: "center", gap: 8, cursor: "pointer" }}
                    role="button"
                    tabIndex={0}
                    aria-expanded={isOpen}
                    onClick={() => toggle(s.id)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" || e.key === " ") {
                        e.preventDefault();
                        toggle(s.id);
                      }
                    }}
                  >
                    <ChevronRightIcon
                      size={15}
                      style={{ flexShrink: 0, transition: "transform .15s", transform: isOpen ? "rotate(90deg)" : "none", color: "var(--text-3)" }}
                    />
                    <span className={`status-dot ${s.enabled ? "ok" : "unknown"}`} />
                    <span className="sched-name">{title}</span>
                    {!s.enabled && <span className="chip">{t("Paused")}</span>}
                    {running && (
                      <span className="chip">
                        <span className="spinner" style={{ borderTopColor: "var(--text-1)" }} />{" "}
                        {t("Running")}
                      </span>
                    )}
                    {!isOpen && s.enabled && !running && (
                      <span className="sched-when">
                        {words || s.schedule}
                        {next ? ` · ${fromNow(next)}` : ""}
                      </span>
                    )}
                    <span className="sched-actions" onClick={(e) => e.stopPropagation()}>
                      {/* Run now is the only way to find out whether a schedule
                          does what was meant without waiting for the hour. It
                          starts the run and returns; while the run is in flight
                          the same button stops it, the way the composer's send
                          button becomes stop mid-answer. */}
                      {running ? (
                        <button
                          className="btn ghost sm"
                          disabled={acting}
                          title={t("Stop this run")}
                          onClick={() =>
                            act(s.id, "Stop", () => CancelScheduledRun(s.id), {
                              refusalIsFine: true,
                            })
                          }
                        >
                          <SquareIcon className="size-3 fill-current" /> {t("Stop")}
                        </button>
                      ) : (
                        <button
                          className="btn ghost sm"
                          disabled={acting}
                          title={t("Run this prompt immediately")}
                          onClick={() => act(s.id, "Run", () => RunScheduledPromptNow(s.id))}
                        >
                          {acting ? (
                            <>
                              <span
                                className="spinner"
                                style={{ borderTopColor: "var(--text-1)" }}
                              />{" "}
                              {t("Starting…")}
                            </>
                          ) : (
                            t("Run now")
                          )}
                        </button>
                      )}
                      <button
                        className="btn ghost sm"
                        disabled={acting}
                        onClick={() =>
                          act(s.id, s.enabled ? "Pause" : "Resume", () =>
                            SetScheduledPromptEnabled(s.id, !s.enabled),
                          )
                        }
                      >
                        {s.enabled ? t("Pause") : t("Resume")}
                      </button>
                      <button
                        className="btn ghost sm"
                        disabled={acting}
                        onClick={() => {
                          if (confirmDelete !== s.id) {
                            setConfirmDelete(s.id);
                            return;
                          }
                          setConfirmDelete("");
                          act(s.id, "Delete", () => DeleteScheduledPrompt(s.id));
                        }}
                      >
                        {confirmDelete === s.id ? t("Click again to delete") : t("Delete")}
                      </button>
                    </span>
                  </div>
                  {isOpen && (<>
                  <div className="rc-row">
                    <span className="rc-key">{t("When")}</span>
                    <span className="rc-val">
                      {words !== "" && <>{words} </>}
                      <span className="mono">{s.schedule}</span>
                    </span>
                  </div>
                  <div className="rc-row">
                    <span className="rc-key">{t("Next run")}</span>
                    <span className="rc-val">
                      {!s.enabled
                        ? "—"
                        : next
                          ? `${formatStamp(next)} · ${fromNow(next)}`
                          : "unknown — “Run now” tests it without waiting"}
                    </span>
                  </div>
                  {last && (
                    <div className="rc-row">
                      <span className="rc-key">{t("Last run")}</span>
                      <span className="rc-val">
                        {formatStamp(last)} · {fromNow(last)}
                      </span>
                    </div>
                  )}
                  {title.trim() !== s.prompt.trim() && (
                    <div className="rc-row">
                      <span className="rc-key">{t("Prompt")}</span>
                      <span className="rc-val">{s.prompt}</span>
                    </div>
                  )}
                  {!!s.session && (
                    <div className="rc-row">
                      <span className="rc-key">{t("Conversation")}</span>
                      <span className="rc-val">
                        <button className="link-btn" onClick={() => onOpenConversation(s.session!)}>
                          {s.session}
                        </button>
                      </span>
                    </div>
                  )}
                  </>)}
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
          <div className="view-title">{t("Scheduled tasks")}{list.length > 0 ? ` (${list.length})` : ""}</div>
        </div>
        {actions}
      </div>
      <div className="panel-scroll">{body}</div>
    </div>
  );
}
