// Conversations the person pinned to the chat sidebar, kept on this device.
import { useEffect, useState } from "react";

export type PinnedConv = { id: string; kind: string; session: string; title: string };

const KEY = "superleo-pinned-conversations";
const EVT = "superleo:pinned-conversations";

export function loadPinned(): PinnedConv[] {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? "[]");
    return Array.isArray(v) ? v.filter((p) => p && typeof p.id === "string") : [];
  } catch { return []; }
}

function save(list: PinnedConv[]) {
  try { localStorage.setItem(KEY, JSON.stringify(list)); } catch { /* private window */ }
  window.dispatchEvent(new Event(EVT));
}

export function isPinned(id: string): boolean { return loadPinned().some((p) => p.id === id); }

export function togglePin(c: PinnedConv) {
  const list = loadPinned();
  save(list.some((p) => p.id === c.id) ? list.filter((p) => p.id !== c.id) : [...list, c]);
}

/** A renamed conversation keeps its pin, under its new name. */
export function renamePinned(id: string, title: string) {
  const list = loadPinned();
  if (list.some((p) => p.id === id)) save(list.map((p) => (p.id === id ? { ...p, title } : p)));
}

export function unpin(id: string) { save(loadPinned().filter((p) => p.id !== id)); }

export function usePinnedConvs(): PinnedConv[] {
  const [list, setList] = useState(loadPinned);
  useEffect(() => {
    const on = () => setList(loadPinned());
    window.addEventListener(EVT, on);
    window.addEventListener("storage", on);
    return () => { window.removeEventListener(EVT, on); window.removeEventListener("storage", on); };
  }, []);
  return list;
}
