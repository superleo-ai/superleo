#!/usr/bin/env python3
"""Change words in a PDF and leave everything else as it was.

    pdf_edit.py IN.pdf OUT.pdf --sub 'OLD=>NEW' [--sub 'OLD=>NEW' ...]
                [--pages 1,3] [--all] [--png DIR]

Each OLD must be found exactly once (give more of the surrounding words to make
it unique), or pass --all to change every occurrence.

How it keeps the layout:
  * Only the matched characters, and the rest of that one line when the new
    text is wider or narrower, are taken out. Neighbouring lines, pictures and
    drawings are not touched, and nothing is painted over the page.
  * The new text is set on the same baseline, in the same size and colour, in
    the font the old text used: the font embedded in the file when it has the
    letters needed, else the same font from this machine, else the closest
    standard one — and it says which, because the last is a visible change.

When it has written OUT.pdf it compares it with IN.pdf (pdf_check.py) and prints
what changed. Read that report: "RESULT: OK" is the only outcome that lets you
say the rest is untouched.

It does not re-wrap paragraphs. If the new text is longer than the room on its
line the report says so; shorten the wording or tell the user.
"""
import argparse
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

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import pdf_check  # noqa: E402

FONT_DIRS = [
    "/System/Library/Fonts", "/System/Library/Fonts/Supplemental", "/Library/Fonts",
    os.path.expanduser("~/Library/Fonts"),
    "/usr/share/fonts", "/usr/local/share/fonts", os.path.expanduser("~/.fonts"),
    os.path.expanduser("~/.local/share/fonts"),
    "C:/Windows/Fonts",
]


font_key = pdf_check.font_key


_system_index = None


def system_fonts():
    """{font_key: path} for the font files on this machine, by file name."""
    global _system_index
    if _system_index is not None:
        return _system_index
    index = {}
    for root in FONT_DIRS:
        if not os.path.isdir(root):
            continue
        for dirpath, _, files in os.walk(root):
            for f in files:
                if f.lower().endswith((".ttf", ".otf")):
                    index.setdefault(font_key(os.path.splitext(f)[0]), os.path.join(dirpath, f))
    _system_index = index
    return index


def has_all(font, text):
    return all(ch.isspace() or font.has_glyph(ord(ch)) for ch in text)


def standard_font(flags, text):
    """The closest of the standard fonts, from what the old span says it was."""
    if any(ord(ch) > 0x2E7F for ch in text):
        return fitz.Font("cjk"), "Droid Sans Fallback (CJK)"
    italic, serif, mono, bold = bool(flags & 2), bool(flags & 4), bool(flags & 8), bool(flags & 16)
    if mono:
        code = {(0, 0): "cour", (1, 0): "cobo", (0, 1): "coit", (1, 1): "cobi"}[(bold, italic)]
    elif serif:
        code = {(0, 0): "tiro", (1, 0): "tibo", (0, 1): "tiit", (1, 1): "tibi"}[(bold, italic)]
    else:
        code = {(0, 0): "helv", (1, 0): "hebo", (0, 1): "heit", (1, 1): "hebi"}[(bold, italic)]
    font = fitz.Font(code)
    return font, font.name


def resolve_font(doc, page, span_font, flags, text):
    """(Font, where it came from, exact?) for setting `text` like `span_font`."""
    want = font_key(span_font)
    # 1. The font inside the file. Usually a subset: only good if it happens to
    #    hold every letter the new text needs.
    for entry in page.get_fonts(full=True):
        xref, base = entry[0], entry[3]
        if font_key(base) != want or xref <= 0:
            continue
        try:
            _, ext, _, buf = doc.extract_font(xref)
            if buf and ext not in ("n/a", ""):
                font = fitz.Font(fontbuffer=buf)
                if has_all(font, text):
                    return font, "the font embedded in the file", True
        except Exception:
            pass
    # 2. The same font installed on this machine.
    path = system_fonts().get(want)
    if path:
        try:
            font = fitz.Font(fontfile=path)
            if has_all(font, text):
                return font, "the same font from this machine (%s)" % path, True
        except Exception:
            pass
    # 3. The closest standard font. A visible change.
    font, name = standard_font(flags, text)
    if font_key(name) == want:
        # The file uses a standard font without embedding it; this is that font.
        return font, "the standard font the file names (%s)" % name, True
    return font, "a SUBSTITUTE, %s — %s is not in the file with the needed letters, nor on this machine" % (
        name, pdf_check.norm_font(span_font)), False


def line_chars(page):
    """Every text line of the page as a list of characters with their styling."""
    lines = []
    for block in page.get_text("rawdict").get("blocks", []):
        for line in block.get("lines", []):
            chars = []
            for span in line.get("spans", []):
                for ch in span.get("chars", []):
                    chars.append({
                        "c": ch["c"], "bbox": fitz.Rect(ch["bbox"]), "origin": ch["origin"],
                        "font": span["font"], "size": span["size"], "color": span["color"],
                        "flags": span["flags"],
                    })
            if chars:
                lines.append({"chars": chars, "dir": line.get("dir", (1, 0)),
                              "text": "".join(c["c"] for c in chars)})
    return lines


def find_hits(page, old):
    """(line, start, end) for every place `old` is written on the page."""
    hits = []
    for line in line_chars(page):
        start = 0
        while True:
            i = line["text"].find(old, start)
            if i < 0:
                break
            hits.append((line, i, i + len(old)))
            start = i + len(old)
    return hits


def rgb(color):
    return ((color >> 16) & 255) / 255.0, ((color >> 8) & 255) / 255.0, (color & 255) / 255.0


def band(chars):
    """A thin strip through the middle of these characters.

    Text is taken out by the area it touches, and line boxes in a PDF overlap:
    the box of one line reaches into the lines above and below. Taking out the
    whole box is what deletes the heading under a changed sentence. A strip
    through the middle of the letters touches this line only.
    """
    x0 = min(c["bbox"].x0 for c in chars) + 0.2
    x1 = max(c["bbox"].x1 for c in chars) - 0.2
    base = chars[0]["origin"][1]
    size = chars[0]["size"]
    return fitz.Rect(x0, base - 0.45 * size, max(x1, x0 + 0.1), base - 0.2 * size)


def edit(args):
    doc = fitz.open(args.input)
    if doc.needs_pass:
        sys.exit("the file is encrypted; it needs its password before it can be edited")
    subs = pdf_check.parse_expect(args.sub)
    pages = None
    if args.pages:
        pages = {int(p) for p in re.split(r"[,\s]+", args.pages.strip()) if p}

    # Find everything first: nothing is written unless every change can be made.
    plan = {}  # page index -> [(line, start, end, old, new)]
    for old, new in subs:
        found = []
        for i, page in enumerate(doc):
            if pages and (i + 1) not in pages:
                continue
            for line, s, e in find_hits(page, old):
                found.append((i, line, s, e))
        if not found:
            scanned = all(not p.get_text().strip() for p in doc)
            hint = ("The file has no text layer (it is a scan); its words cannot be replaced this way."
                    if scanned else
                    "It may be split across lines, or written differently. Run pdf_check.py on the file to see its lines.")
            sys.exit("\"%s\" was not found. %s" % (old, hint))
        if len(found) > 1 and not args.all:
            where = "\n".join("  page %d: %s" % (i + 1, line["text"].strip()[:90]) for i, line, _, _ in found)
            sys.exit("\"%s\" is written %d times:\n%s\nGive more of the surrounding words so it matches once, "
                     "or pass --all to change every one." % (old, len(found), where))
        for i, line, s, e in found:
            plan.setdefault(i, []).append((line, s, e, old, new))

    notes, warnings = [], []
    for i, items in sorted(plan.items()):
        page = doc[i]
        writes = []
        for line, s, e, old, new in items:
            if abs(line["dir"][0] - 1) > 0.01 or abs(line["dir"][1]) > 0.01:
                sys.exit("\"%s\" on page %d is rotated text; this tool only sets horizontal text." % (old, i + 1))
            chars = line["chars"]
            first = chars[s]
            tail = chars[e:]
            while tail and tail[-1]["c"].isspace():
                tail = tail[:-1]
            tail_text = "".join(c["c"] for c in tail)
            font, source, exact = resolve_font(doc, page, first["font"], first["flags"], new + tail_text)
            size = first["size"]
            old_width = (chars[e]["origin"][0] if e < len(chars) else chars[e - 1]["bbox"].x1) - first["origin"][0]
            new_width = font.text_length(new, fontsize=size)
            same_width = abs(new_width - old_width) < 0.05 * size
            # Same width and the real font: the rest of the line can stay as it
            # is. Otherwise the rest of the line is set again after the new
            # words, so it follows them instead of overlapping or leaving a gap.
            if same_width and exact:
                page.add_redact_annot(band(chars[s:e]), fill=False)
                writes.append((first["origin"], new, font, size, first["color"]))
            else:
                page.add_redact_annot(band(chars[s:e] + tail), fill=False)
                x, y = first["origin"]
                writes.append(((x, y), new, font, size, first["color"]))
                x += new_width
                # The rest keeps its own styling, run by run.
                run_start = 0
                for k in range(1, len(tail) + 1):
                    if k == len(tail) or (tail[k]["font"], tail[k]["size"], tail[k]["color"]) != (
                            tail[run_start]["font"], tail[run_start]["size"], tail[run_start]["color"]):
                        run = tail[run_start:k]
                        text = "".join(c["c"] for c in run)
                        if (run[0]["font"], run[0]["size"]) == (first["font"], first["size"]):
                            rfont = font
                        else:
                            rfont, rsource, rexact = resolve_font(doc, page, run[0]["font"], run[0]["flags"], text)
                            if not rexact:
                                warnings.append("page %d: the words after \"%s\" were set again in %s" % (i + 1, new, rsource))
                        writes.append(((x, y), text, rfont, run[0]["size"], run[0]["color"]))
                        x += rfont.text_length(text, fontsize=run[0]["size"])
                        run_start = k
                if x > page.rect.width - 6:
                    warnings.append("page %d: after \"%s\" the line runs to x=%.0f pt, past the right edge of the page (%.0f pt). "
                                    "Paragraphs are not re-wrapped: use shorter wording, or tell the user." % (
                                        i + 1, new, x, page.rect.width))
                elif tail and x > line["chars"][-1]["bbox"].x1 + 40:
                    warnings.append("page %d: after \"%s\" the line is %.0f pt longer than it was; check it still fits its column." % (
                        i + 1, new, x - line["chars"][-1]["bbox"].x1))
            notes.append("page %d: \"%s\" -> \"%s\" in %s, %gpt" % (i + 1, old, new, source, round(size, 1)))
            if not exact:
                warnings.append("page %d: \"%s\" is set in %s. It will not look the same as the text around it." % (i + 1, new, source))

        # Text only: pictures and line art under the strip stay.
        page.apply_redactions(images=fitz.PDF_REDACT_IMAGE_NONE, graphics=fitz.PDF_REDACT_LINE_ART_NONE)
        for origin, text, font, size, color in writes:
            if not text:
                continue
            tw = fitz.TextWriter(page.rect, color=rgb(color))
            tw.append(fitz.Point(origin), text, font=font, fontsize=size)
            tw.write_text(page)

    try:
        doc.subset_fonts()
    except Exception:
        pass
    if os.path.abspath(args.input) == os.path.abspath(args.output):
        sys.exit("OUT must not be the input file: the original is needed to check the result against.")
    os.makedirs(os.path.dirname(os.path.abspath(args.output)) or ".", exist_ok=True)
    doc.save(args.output, garbage=3, deflate=True)
    doc.close()

    print("wrote " + args.output)
    for n in notes:
        print("  " + n)
    print("")
    print("--- check against the original ---")
    problems = pdf_check.compare(args.input, args.output, subs, png_dir=args.png)
    for w in warnings:
        print("PROBLEM: " + w)
        problems.append(w)
    return pdf_check.finish(problems)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("input")
    ap.add_argument("output")
    ap.add_argument("--sub", action="append", required=True, metavar="OLD=>NEW")
    ap.add_argument("--pages", help="only these pages, e.g. 1,3")
    ap.add_argument("--all", action="store_true", help="change every occurrence of each OLD")
    ap.add_argument("--png", metavar="DIR", help="also write the pages of both files as PNGs into DIR")
    return edit(ap.parse_args())


if __name__ == "__main__":
    sys.exit(main())
