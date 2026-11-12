#!/usr/bin/env python3
"""Check a PDF, or check an edited PDF against the one it was made from.

    pdf_check.py FILE [--png DIR]
        What is in the file: pages, size, fonts, text lines, blank pages.

    pdf_check.py ORIGINAL EDITED [--expect 'OLD=>NEW' ...] [--allow-pages] [--png DIR]
        What changed between the two, and whether anything changed that was
        not asked for: text line by line, the font / size / colour / position
        of every line, and the rendered pixels outside the edited lines.

--png DIR also writes each page as a PNG for a person to open.

The last line is "RESULT: OK" or "RESULT: N problem(s)", and the exit status is
0 or 1 to match. A problem is something to fix or to tell the user about; it is
never something to leave out of the answer.
"""
import argparse
import difflib
import os
import re
import sys

try:
    try:
        import pymupdf as fitz
    except ImportError:
        import fitz  # PyMuPDF before 1.24
except ImportError:
    sys.exit("PyMuPDF is missing. Run this through docpy (see the skill), which installs it.")

ZOOM = 2.0  # render scale for the pixel comparison


def norm_font(name):
    """'ABCDEF+Monaco' and 'Monaco' are the same font: drop the subset tag."""
    return re.sub(r"^[A-Z]{6}\+", "", name or "")


def font_key(name):
    """Comparable form of a font name: 'ABCDEF+Arial-BoldMT', 'Arial Bold' -> 'arialbold'.

    The same font is named differently depending on where it was read from —
    the PDF's own entry, the file's name table — so looks are compared by this,
    never by the raw name.
    """
    name = re.sub(r"[^a-z0-9]", "", norm_font(name).lower())
    name = re.sub(r"(psmt|mt|ps)$", "", name)
    return name.replace("psmt", "").replace("regular", "").replace("roman", "")


def look(styles):
    """What a line looks like, for comparing: fonts by key, with size and colour."""
    return {(font_key(f), s, c) for f, s, c in styles}


def norm_text(text):
    return re.sub(r"\s+", " ", text).strip()


def page_lines(page):
    """Text lines of a page, rebuilt from spans by baseline.

    Text written into an existing line by an edit lands in its own text object,
    and the library then reports it as a separate line. Grouping spans by
    baseline puts it back where a reader sees it.
    """
    spans = []
    for block in page.get_text("dict").get("blocks", []):
        for line in block.get("lines", []):
            for span in line.get("spans", []):
                if not span["text"].strip():
                    continue
                spans.append(span)
    spans.sort(key=lambda s: (round(s["origin"][1], 1), s["origin"][0]))
    lines = []
    for span in spans:
        y = span["origin"][1]
        if lines and abs(lines[-1]["y"] - y) <= max(1.0, 0.15 * span["size"]):
            lines[-1]["spans"].append(span)
        else:
            lines.append({"y": y, "spans": [span]})
    out = []
    for line in lines:
        ss = sorted(line["spans"], key=lambda s: s["bbox"][0])
        text = ""
        prev = None
        for s in ss:
            if prev is not None:
                gap = s["bbox"][0] - prev["bbox"][2]
                if gap > 0.2 * s["size"] and not text.endswith(" ") and not s["text"].startswith(" "):
                    text += " "
            text += s["text"]
            prev = s
        x0 = min(s["bbox"][0] for s in ss)
        y0 = min(s["bbox"][1] for s in ss)
        x1 = max(s["bbox"][2] for s in ss)
        y1 = max(s["bbox"][3] for s in ss)
        styles = sorted({(norm_font(s["font"]), round(s["size"], 1), "#%06x" % s["color"]) for s in ss})
        out.append({
            "text": norm_text(text),
            "bbox": (x0, y0, x1, y1),
            # Where the line starts: text added further along, a little above
            # or below, has not moved the line.
            "y": ss[0]["origin"][1],
            "x": x0,
            "styles": styles,
        })
    return out


def style_str(styles):
    return "; ".join("%s %gpt %s" % s for s in styles)


def apply_expectations(text, expects):
    for old, new in expects:
        text = text.replace(old, new)
    return norm_text(text)


def diff_regions(pix_a, pix_b):
    """Rectangles (in pixels) where two same-sized renders differ."""
    if (pix_a.width, pix_a.height, pix_a.n) != (pix_b.width, pix_b.height, pix_b.n):
        return None
    width, height, n = pix_a.width, pix_a.height, pix_a.n
    a, b = pix_a.samples, pix_b.samples
    if a == b:
        return []
    stride = width * n
    rows = []
    for y in range(height):
        ra = a[y * stride:(y + 1) * stride]
        rb = b[y * stride:(y + 1) * stride]
        if ra == rb:
            continue
        lo, hi = None, None
        step = 8 * n
        for off in range(0, stride, step):
            if ra[off:off + step] != rb[off:off + step]:
                if lo is None:
                    lo = off // n
                hi = min(width, (off + step) // n)
        rows.append((y, lo, hi))
    # Merge rows into bands; a gap of a few clean rows still belongs together.
    regions = []
    for y, lo, hi in rows:
        if regions and y - regions[-1][3] <= 3:
            r = regions[-1]
            regions[-1] = (min(r[0], lo), r[1], max(r[2], hi), y + 1)
        else:
            regions.append((lo, y, hi, y + 1))
    return regions


def overlaps(region, rect, pad):
    x0, y0, x1, y1 = region
    a0, b0, a1, b1 = rect
    return not (x1 < a0 - pad or x0 > a1 + pad or y1 < b0 - pad or y0 > b1 + pad)


def save_pngs(doc, out_dir, prefix):
    os.makedirs(out_dir, exist_ok=True)
    paths = []
    for i, page in enumerate(doc, 1):
        path = os.path.join(out_dir, "%s-%d.png" % (prefix, i))
        page.get_pixmap(matrix=fitz.Matrix(ZOOM, ZOOM), alpha=False).save(path)
        paths.append(path)
    return paths


def describe(path, png_dir=None):
    doc = fitz.open(path)
    problems = []
    print("%s: %d page(s)" % (path, len(doc)))
    if doc.needs_pass:
        print("  encrypted: a password is needed")
        problems.append("the file is encrypted")
    for i, page in enumerate(doc, 1):
        lines = page_lines(page)
        fonts = sorted({s for line in lines for s in line["styles"]})
        images = len(page.get_images(full=True))
        print("  page %d: %gx%g pt, %d text line(s), %d image(s)" % (
            i, page.rect.width, page.rect.height, len(lines), images))
        if fonts:
            print("    fonts: " + style_str(fonts[:8]) + (" ..." if len(fonts) > 8 else ""))
        if not lines and not images and not page.get_drawings():
            print("    BLANK: nothing on this page")
            problems.append("page %d is blank" % i)
        elif not lines and images:
            print("    no text layer: this page is a scan or an image; its text cannot be searched or replaced")
        for line in lines[:400]:
            print("    | " + line["text"])
        if len(lines) > 400:
            print("    | ... %d more line(s)" % (len(lines) - 400))
    if png_dir:
        for p in save_pngs(doc, png_dir, os.path.splitext(os.path.basename(path))[0]):
            print("  rendered: " + p)
    return problems


def compare(orig_path, new_path, expects, allow_pages=False, png_dir=None):
    """Compare two PDFs. Returns the list of problems (strings)."""
    problems = []
    a, b = fitz.open(orig_path), fitz.open(new_path)
    print("original: %s (%d page(s))" % (orig_path, len(a)))
    print("edited:   %s (%d page(s))" % (new_path, len(b)))
    if len(a) != len(b):
        msg = "page count changed: %d -> %d" % (len(a), len(b))
        print(("note: " if allow_pages else "PROBLEM: ") + msg)
        if not allow_pages:
            problems.append(msg)

    seen_new = {new: 0 for _, new in expects}
    left_old = {old: 0 for old, _ in expects}
    changed_total = 0

    for i in range(min(len(a), len(b))):
        pa, pb = a[i], b[i]
        tag = "page %d" % (i + 1)
        if abs(pa.rect.width - pb.rect.width) > 0.5 or abs(pa.rect.height - pb.rect.height) > 0.5:
            msg = "%s: page size changed %gx%g -> %gx%g" % (
                tag, pa.rect.width, pa.rect.height, pb.rect.width, pb.rect.height)
            print("PROBLEM: " + msg)
            problems.append(msg)
        la, lb = page_lines(pa), page_lines(pb)
        ta, tb = [l["text"] for l in la], [l["text"] for l in lb]
        for _, new in expects:
            seen_new[new] += sum(t.count(norm_text(new)) for t in tb)
        for old, new in expects:
            if norm_text(old) not in norm_text(new):
                left_old[old] += sum(t.count(norm_text(old)) for t in tb)

        allowed = []  # rectangles (points) where pixels are expected to differ
        matcher = difflib.SequenceMatcher(a=ta, b=tb, autojunk=False)
        for op, i1, i2, j1, j2 in matcher.get_opcodes():
            if op == "equal":
                for k in range(i2 - i1):
                    old, new = la[i1 + k], lb[j1 + k]
                    if look(old["styles"]) != look(new["styles"]):
                        msg = "%s: \"%s\" kept its words but changed its look: %s -> %s" % (
                            tag, old["text"][:60], style_str(old["styles"]), style_str(new["styles"]))
                        print("PROBLEM: " + msg)
                        problems.append(msg)
                        allowed.append(old["bbox"])
                        allowed.append(new["bbox"])
                    if abs(old["x"] - new["x"]) > 1.0 or abs(old["y"] - new["y"]) > 1.0:
                        msg = "%s: \"%s\" moved by (%.1f, %.1f) pt" % (
                            tag, old["text"][:60], new["x"] - old["x"], new["y"] - old["y"])
                        print("PROBLEM: " + msg)
                        problems.append(msg)
                        allowed.append(old["bbox"])
                        allowed.append(new["bbox"])
                continue
            olds, news = la[i1:i2], lb[j1:j2]
            for line in olds + news:
                allowed.append(line["bbox"])
            # Pair each old line with the new line most like it; what is left
            # over on either side was removed or added.
            pairs, used = [], set()
            for old in olds:
                best, best_ratio = None, 0.6
                for idx, new in enumerate(news):
                    if idx in used:
                        continue
                    ratio = difflib.SequenceMatcher(a=old["text"], b=new["text"], autojunk=False).ratio()
                    if ratio > best_ratio:
                        best, best_ratio = idx, ratio
                if best is not None:
                    used.add(best)
                    pairs.append((old, news[best]))
            paired_old = {id(o) for o, _ in pairs}
            olds = [o for o in olds if id(o) not in paired_old]
            news = [n for idx, n in enumerate(news) if idx not in used]
            if pairs:
                for old, new in pairs:
                    changed_total += 1
                    expected = bool(expects) and apply_expectations(old["text"], expects) == new["text"]
                    label = "changed as asked" if expected else ("changed" if not expects else "UNEXPECTED change")
                    print("%s: %s\n    - %s\n    + %s" % (tag, label, old["text"], new["text"]))
                    if expects and not expected:
                        problems.append("%s: a line changed in a way that was not asked for: \"%s\" -> \"%s\"" % (
                            tag, old["text"][:60], new["text"][:60]))
                    if not look(new["styles"]) <= look(old["styles"]):
                        msg = "%s: the new text does not look like the old: was %s, now %s (\"%s\")" % (
                            tag, style_str(old["styles"]), style_str(new["styles"]), new["text"][:60])
                        print("PROBLEM: " + msg)
                        problems.append(msg)
                    if abs(old["x"] - new["x"]) > 1.0 or abs(old["y"] - new["y"]) > 1.0:
                        msg = "%s: the changed line moved by (%.1f, %.1f) pt (\"%s\")" % (
                            tag, new["x"] - old["x"], new["y"] - old["y"], new["text"][:60])
                        print("PROBLEM: " + msg)
                        problems.append(msg)
                    if new["bbox"][2] > pb.rect.width - 6:
                        msg = "%s: the changed line now runs off the right edge of the page (\"%s\")" % (
                            tag, new["text"][:60])
                        print("PROBLEM: " + msg)
                        problems.append(msg)
            for old in olds:
                changed_total += 1
                msg = "%s: line MISSING from the edited file: \"%s\"" % (tag, old["text"][:80])
                print("PROBLEM: " + msg)
                problems.append(msg)
            for new in news:
                changed_total += 1
                msg = "%s: line ADDED in the edited file: \"%s\" (%s)" % (
                    tag, new["text"][:80], style_str(new["styles"]))
                if expects:
                    print("PROBLEM: " + msg)
                    problems.append(msg)
                else:
                    print(msg)

        # The text can match while the page does not: a deleted image, a line
        # of a table gone, a white box over something.
        pix_a = pa.get_pixmap(matrix=fitz.Matrix(ZOOM, ZOOM), alpha=False)
        pix_b = pb.get_pixmap(matrix=fitz.Matrix(ZOOM, ZOOM), alpha=False)
        regions = diff_regions(pix_a, pix_b)
        if regions is None:
            continue
        allowed_px = [tuple(v * ZOOM for v in r) for r in allowed]
        stray = [r for r in regions if not any(overlaps(r, al, 4 * ZOOM) for al in allowed_px)]
        if stray:
            for x0, y0, x1, y1 in stray[:6]:
                msg = "%s: the page looks different outside the edited lines, around x=%d..%d y=%d..%d pt" % (
                    tag, x0 / ZOOM, x1 / ZOOM, y0 / ZOOM, y1 / ZOOM)
                print("PROBLEM: " + msg)
                problems.append(msg)
        elif regions:
            print("%s: pixels differ only where lines changed" % tag)
        else:
            print("%s: renders identically" % tag)

    for old, new in expects:
        if seen_new[new] == 0:
            msg = "\"%s\" was to become \"%s\", but \"%s\" is nowhere in the edited file" % (old, new, new)
            print("PROBLEM: " + msg)
            problems.append(msg)
        if left_old.get(old, 0) > 0:
            msg = "\"%s\" is still in the edited file (%d time(s))" % (old, left_old[old])
            print("PROBLEM: " + msg)
            problems.append(msg)
    if changed_total == 0:
        print("no text line differs between the two files")

    if png_dir:
        for p in save_pngs(a, png_dir, "original") + save_pngs(b, png_dir, "edited"):
            print("rendered: " + p)
    return problems


def parse_expect(values):
    out = []
    for v in values or []:
        if "=>" not in v:
            sys.exit("--expect takes 'OLD=>NEW', got: " + v)
        old, new = v.split("=>", 1)
        out.append((old, new))
    return out


def finish(problems):
    if problems:
        print("RESULT: %d problem(s)" % len(problems))
        return 1
    print("RESULT: OK")
    return 0


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("files", nargs="+", help="FILE, or ORIGINAL EDITED")
    ap.add_argument("--expect", action="append", metavar="OLD=>NEW",
                    help="a change that was asked for; anything else that changed is a problem")
    ap.add_argument("--allow-pages", action="store_true", help="the page count was meant to change")
    ap.add_argument("--png", metavar="DIR", help="also write every page as a PNG into DIR")
    args = ap.parse_args()
    if len(args.files) == 1:
        return finish(describe(args.files[0], args.png))
    if len(args.files) == 2:
        return finish(compare(args.files[0], args.files[1], parse_expect(args.expect), args.allow_pages, args.png))
    ap.error("give one file, or the original and the edited file")


if __name__ == "__main__":
    sys.exit(main())
