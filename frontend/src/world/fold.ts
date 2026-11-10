import { useEffect, useState } from "react";

// Parts of the world that fold away to their hub: the MCP servers fold into
// the MCP plug. Remembered on this machine; the world and the inspector both
// follow it.

export type Foldable = "mcp";

const key = (k: Foldable) => `superleo-world-fold-${k}`;
const subs = new Set<(k: Foldable, folded: boolean) => void>();

export function isFolded(k: Foldable): boolean {
  try { return localStorage.getItem(key(k)) === "1"; } catch { return false; }
}

export function setFolded(k: Foldable, folded: boolean) {
  try { localStorage.setItem(key(k), folded ? "1" : "0"); } catch { /* still applies for this session */ }
  subs.forEach((f) => f(k, folded));
}

export function onFold(f: (k: Foldable, folded: boolean) => void) {
  subs.add(f);
  return () => { subs.delete(f); };
}

export function useFolded(k: Foldable): [boolean, (on: boolean) => void] {
  const [v, setV] = useState(() => isFolded(k));
  useEffect(() => onFold((kk, on) => { if (kk === k) setV(on); }), [k]);
  return [v, (on) => setFolded(k, on)];
}
