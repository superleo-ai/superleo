import { useSyncExternalStore } from "react";
import { Features, SetFeature } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";
import { ViewKey } from "./types";

/**
 * The parts the person can switch off whole (Settings › Abilities): off, a
 * part has no rail entry, no page and no tile. Read from the core and read
 * again whenever its settings change.
 */
export type Feature = "browser" | "desktop" | "wellbeing" | "finance";
export type FeatureMap = Record<Feature, boolean>;

/** A core that does not say shows everything, as before the switches. */
const ALL_ON: FeatureMap = { browser: true, desktop: true, wellbeing: true, finance: true };

let state: FeatureMap | null = null;
const subs = new Set<() => void>();
let started = false;

function absorb(raw: Record<string, boolean> | null | undefined) {
  const next: FeatureMap = { ...ALL_ON };
  for (const k of Object.keys(ALL_ON) as Feature[]) if (raw && typeof raw[k] === "boolean") next[k] = raw[k];
  if (state && (Object.keys(next) as Feature[]).every((k) => next[k] === state![k])) return;
  state = next;
  subs.forEach((f) => f());
}

export function refreshFeatures() {
  Features().then((r) => absorb(r)).catch(() => absorb(null));
}

function subscribe(f: () => void) {
  subs.add(f);
  if (!started) {
    started = true;
    refreshFeatures();
    EventsOn("settings:changed", refreshFeatures);
  }
  return () => { subs.delete(f); };
}

/** Each feature on or off; null until the core has answered. */
export function useFeatures(): FeatureMap | null {
  return useSyncExternalStore(subscribe, () => state);
}

/** Switch one feature; the state follows at once, then the core's answer. */
export async function setFeature(name: Feature, on: boolean) {
  if (state) { state = { ...state, [name]: on }; subs.forEach((f) => f()); }
  try {
    absorb(await SetFeature(name, on));
  } catch (e) {
    refreshFeatures();
    throw e;
  }
}

/** The page a feature owns, hidden with it. */
export const FEATURE_VIEW: Partial<Record<ViewKey, Feature>> = { wellbeing: "wellbeing" };

/** Whether a page is shown: unknown until heard, so nothing flashes in and out. */
export function viewShown(view: ViewKey | "browser", f: FeatureMap | null): boolean {
  if (view === "browser") return !!f?.browser;
  const need = FEATURE_VIEW[view as ViewKey];
  return !need || !!f?.[need];
}
