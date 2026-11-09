// What a dashboard's hologram says: a few short lines out of its source.
//
// A dashboard is the markdown answer that made it, often with a ```bigscreen
// block of panels. The hologram has room for five lines of type, so it takes
// what reads at that size: each stat as "title value", the first entries of a
// ranked list, and otherwise the sentences of the text. Charts are left out —
// a series of numbers is not a line.

const MAX = 5;

type Panel = {
  title?: string; label?: string;
  value?: number | string; prefix?: string; unit?: string; decimals?: number;
  items?: { name?: string; label?: string; title?: string; value?: number | string }[];
};

function num(v: number | string | undefined, decimals?: number): string {
  if (typeof v !== "number") return v == null ? "" : String(v);
  const d = typeof decimals === "number" ? decimals : Number.isInteger(v) ? 0 : 2;
  return v.toLocaleString(undefined, { minimumFractionDigits: d, maximumFractionDigits: d });
}

function stat(p: Panel): string | null {
  if (p.value == null || (!p.title && !p.label)) return null;
  return `${p.title ?? p.label}  ${p.prefix ?? ""}${num(p.value, p.decimals)}${p.unit ? " " + p.unit : ""}`.trim();
}

function ranked(p: Panel, room: number): string[] {
  const out: string[] = [];
  for (const it of p.items ?? []) {
    if (out.length >= room) break;
    const name = it.name ?? it.label ?? it.title;
    if (!name) continue;
    out.push(it.value == null ? String(name) : `${name}  ${num(it.value)}${p.unit ? " " + p.unit : ""}`);
  }
  return out;
}

/** Lines out of a ```bigscreen block: stats first, then ranked lists. */
function fromPanels(panels: Panel[]): string[] {
  const out: string[] = [];
  for (const p of panels) {
    const s = stat(p);
    if (s) out.push(s);
    if (out.length >= MAX) return out;
  }
  for (const p of panels) {
    if (!p.items?.length) continue;
    out.push(...ranked(p, MAX - out.length));
    if (out.length >= MAX) break;
  }
  return out;
}

/** Plain markdown as lines: headings, list items and table rows as they
 *  are, paragraphs a sentence at a time, the markup taken off. */
function fromText(md: string): string[] {
  const out: string[] = [];
  for (let line of md.split("\n")) {
    line = line.trim();
    if (!line || /^\|?\s*:?-{2,}/.test(line)) continue; // blank, a table's rule
    if (line.startsWith("|")) line = line.split("|").map((c) => c.trim()).filter(Boolean).join(" · ");
    line = line
      .replace(/^#+\s*/, "").replace(/^([-*+]|\d+[.)])\s+/, "")
      .replace(/\*\*|__|`/g, "").replace(/\[([^\]]+)\]\([^)]*\)/g, "$1");
    for (const s of line.split(/(?<=[。！？；])|(?<=[.!?;])\s+/)) {
      const t = s.trim();
      if (t) out.push(t);
      if (out.length >= MAX) return out;
    }
  }
  return out;
}

export function holoLines(source: string | undefined): string[] {
  if (!source) return [];
  const fence = /```(\w*)\n([\s\S]*?)```/g;
  let m: RegExpExecArray | null;
  while ((m = fence.exec(source))) {
    if (m[1] !== "bigscreen") continue;
    try {
      const j = JSON.parse(m[2]);
      const lines = fromPanels(Array.isArray(j?.panels) ? j.panels : []);
      if (lines.length) return lines;
    } catch { /* not JSON: the text below stands */ }
  }
  return fromText(source.replace(fence, ""));
}
