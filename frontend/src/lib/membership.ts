import { useEffect, useState, useSyncExternalStore } from "react";
import { HiveMembership } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";

/** Where this Mac stands in the hive (HiveMembership), kept current by
 *  hive:membership and a slow poll. */
export interface Membership {
  joined: boolean;
  hive?: string;
  link?: string;
  name?: string;
  connected: boolean;
  unreachable: boolean;
  error?: string;
  windowLive: boolean;
  needsCode?: boolean;
  localDuties: string[];
}

/** The hive's address without the scheme: what it is called on screen. */
export const hiveHost = (url?: string) => (url || "").replace(/^https?:\/\//, "").replace(/\/+$/, "");

/** How long between the checks made while the hive is out of reach. */
export const RETRY_SECONDS = 15;

let current: Membership | null = null;
const listeners = new Set<() => void>();
let stop: (() => void) | null = null;

async function recheck() {
  try {
    current = (await HiveMembership()) as Membership;
    listeners.forEach((f) => f());
  } catch {
    /* keep what was last known */
  }
}

function start() {
  void recheck();
  const poll = window.setInterval(() => void recheck(), 10000);
  const off = EventsOn("hive:membership", () => void recheck());
  stop = () => {
    window.clearInterval(poll);
    if (typeof off === "function") off();
  };
}

// One function for every render: React resubscribes whenever subscribe
// changes, and an inline one made each render unsubscribe the last listener
// (stopping the poll) and subscribe again (starting it, which asks at once);
// the answer re-rendered, and the window asked thousands of times a second.
function subscribe(fn: () => void) {
  listeners.add(fn);
  if (listeners.size === 1) start();
  return () => {
    listeners.delete(fn);
    if (listeners.size === 0) { stop?.(); stop = null; }
  };
}
const snapshot = () => current;

/** One poll and one subscription shared by everything on screen. */
export function useMembership(): { m: Membership | null; recheck: () => Promise<void> } {
  const m = useSyncExternalStore(subscribe, snapshot, snapshot);
  return { m, recheck };
}

/** Joined and the hive does not answer. */
export const isCutOff = (m: Membership | null) => !!m && m.joined && (m.unreachable || !m.connected && !!m.error);

/** Seconds until the next check while cut off, counting down from RETRY_SECONDS. */
export function useRetryCountdown(active: boolean, recheck: () => Promise<void>) {
  const [left, setLeft] = useState(RETRY_SECONDS);
  useEffect(() => {
    if (!active) { setLeft(RETRY_SECONDS); return; }
    const id = window.setInterval(() => {
      setLeft((n) => {
        if (n <= 1) { void recheck(); return RETRY_SECONDS; }
        return n - 1;
      });
    }, 1000);
    return () => window.clearInterval(id);
  }, [active, recheck]);
  return { left, reset: () => setLeft(RETRY_SECONDS) };
}
