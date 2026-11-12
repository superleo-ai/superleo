#!/usr/bin/env python3
"""The everyday PDF jobs that are not "change these words".

    pdf_tools.py merge OUT.pdf A.pdf B.pdf [...]
    pdf_tools.py pages IN.pdf OUT.pdf 1-3,7          keep these pages, in this order
    pdf_tools.py watermark IN.pdf OUT.pdf TEXT [--gray 0.85] [--size 72] [--angle 45]
    pdf_tools.py number IN.pdf OUT.pdf [--format '{n} / {total}'] [--start 1] [--size 9]
    pdf_tools.py write IN.pdf OUT.pdf --after 'Signed:' TEXT [--page N] [--dx 6]
        Put TEXT on the line of an anchor, right after it, in the anchor's size
        and colour: a name on a signature line, a date after "Date:".

Each one ends by describing the file it wrote (pdf_check.py), so the result is
on screen before anything is reported as done. Add --png DIR to also get the
pages as images for a person to open.
"""
import argparse
import os
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
import pdf_edit  # noqa: E402


def save(doc, out):
    os.makedirs(os.path.dirname(os.path.abspath(out)) or ".", exist_ok=True)
    doc.save(out, garbage=3, deflate=True)
    doc.close()


def report(out, png):
    print("wrote " + out)
    return pdf_check.finish(pdf_check.describe(out, png))


def parse_pages(spec, total):
    out = []
    for part in spec.replace(" ", "").split(","):
        if not part:
            continue
        if "-" in part:
            a, b = part.split("-", 1)
            a, b = int(a or 1), int(b or total)
            out.extend(range(a, b + 1) if a <= b else range(a, b - 1, -1))
        else:
            out.append(int(part))
    bad = [p for p in out if p < 1 or p > total]
    if bad:
        sys.exit("the file has %d page(s); there is no page %s" % (total, ", ".join(map(str, bad))))
    return out


def cmd_merge(a):
    doc = fitz.open()
    for path in a.inputs:
        src = fitz.open(path)
        doc.insert_pdf(src)
        print("  + %s (%d page(s))" % (path, len(src)))
        src.close()
    save(doc, a.output)
    return report(a.output, a.png)


def cmd_pages(a):
    src = fitz.open(a.input)
    doc = fitz.open()
    for p in parse_pages(a.spec, len(src)):
        doc.insert_pdf(src, from_page=p - 1, to_page=p - 1)
    save(doc, a.output)
    return report(a.output, a.png)


def cmd_watermark(a):
    doc = fitz.open(a.input)
    font = fitz.Font("cjk") if any(ord(c) > 0x2E7F for c in a.text) else fitz.Font("hebo")
    for page in doc:
        r = page.rect
        # Never wider than the page's diagonal can hold.
        size = min(a.size, 0.8 * (r.width ** 2 + r.height ** 2) ** 0.5 / max(1, font.text_length(a.text, fontsize=1)))
        width = font.text_length(a.text, fontsize=size)
        centre = fitz.Point(r.width / 2, r.height / 2)
        tw = fitz.TextWriter(r, color=(a.gray, a.gray, a.gray))
        tw.append(fitz.Point(centre.x - width / 2, centre.y + size * 0.35), a.text, font=font, fontsize=size)
        # overlay=False: under the page's own content, so it never hides a word.
        tw.write_text(page, morph=(centre, fitz.Matrix(a.angle)), overlay=False)
    save(doc, a.output)
    return report(a.output, a.png)


def cmd_number(a):
    doc = fitz.open(a.input)
    font = fitz.Font("helv")
    total = len(doc)
    for i, page in enumerate(doc):
        text = a.format.format(n=i + a.start, total=total + a.start - 1)
        r = page.rect
        width = font.text_length(text, fontsize=a.size)
        tw = fitz.TextWriter(r, color=(0.35, 0.35, 0.35))
        tw.append(fitz.Point((r.width - width) / 2, r.height - 28), text, font=font, fontsize=a.size)
        tw.write_text(page)
    save(doc, a.output)
    return report(a.output, a.png)


def cmd_write(a):
    doc = fitz.open(a.input)
    hits = []
    for i, page in enumerate(doc):
        if a.page and i + 1 != a.page:
            continue
        for line, s, e in pdf_edit.find_hits(page, a.after):
            hits.append((i, line, s, e))
    if not hits:
        sys.exit("\"%s\" was not found. Run pdf_check.py on the file to see its lines." % a.after)
    if len(hits) > 1:
        where = "\n".join("  page %d: %s" % (i + 1, line["text"].strip()[:90]) for i, line, _, _ in hits)
        sys.exit("\"%s\" is written %d times:\n%s\nGive a longer anchor or --page N." % (a.after, len(hits), where))
    i, line, s, e = hits[0]
    page = doc[i]
    last = line["chars"][e - 1]
    font, source, _ = pdf_edit.resolve_font(doc, page, last["font"], last["flags"], a.text)
    x = last["bbox"].x1 + a.dx
    y = last["origin"][1]
    width = font.text_length(a.text, fontsize=last["size"])
    if x + width > page.rect.width - 6:
        sys.exit("\"%s\" does not fit after \"%s\": it would run off the page." % (a.text, a.after))
    tw = fitz.TextWriter(page.rect, color=pdf_edit.rgb(last["color"]))
    # Slightly above the baseline when it sits on a rule of underscores, so the
    # name stands on the line instead of being struck through by it.
    rest = line["text"][e:].strip()
    lift = 0.15 * last["size"] if rest.startswith("_") else 0
    tw.append(fitz.Point(x, y - lift), a.text, font=font, fontsize=last["size"])
    tw.write_text(page)
    print("page %d: wrote \"%s\" after \"%s\" in %s, %gpt" % (i + 1, a.text, a.after, source, round(last["size"], 1)))
    save(doc, a.output)
    print("wrote " + a.output)
    print("--- check against the original ---")
    problems = pdf_check.compare(a.input, a.output, [], png_dir=a.png)
    return pdf_check.finish(problems)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("merge")
    p.add_argument("output")
    p.add_argument("inputs", nargs="+")
    p.set_defaults(fn=cmd_merge)

    p = sub.add_parser("pages")
    p.add_argument("input")
    p.add_argument("output")
    p.add_argument("spec")
    p.set_defaults(fn=cmd_pages)

    p = sub.add_parser("watermark")
    p.add_argument("input")
    p.add_argument("output")
    p.add_argument("text")
    p.add_argument("--gray", type=float, default=0.85)
    p.add_argument("--size", type=float, default=72)
    p.add_argument("--angle", type=float, default=45)
    p.set_defaults(fn=cmd_watermark)

    p = sub.add_parser("number")
    p.add_argument("input")
    p.add_argument("output")
    p.add_argument("--format", default="{n} / {total}")
    p.add_argument("--start", type=int, default=1)
    p.add_argument("--size", type=float, default=9)
    p.set_defaults(fn=cmd_number)

    p = sub.add_parser("write")
    p.add_argument("input")
    p.add_argument("output")
    p.add_argument("text")
    p.add_argument("--after", required=True)
    p.add_argument("--page", type=int)
    p.add_argument("--dx", type=float, default=6)
    p.set_defaults(fn=cmd_write)

    for name in sub.choices:
        sub.choices[name].add_argument("--png", metavar="DIR")
    args = ap.parse_args()
    if getattr(args, "input", None) and os.path.abspath(args.input) == os.path.abspath(args.output):
        sys.exit("OUT must not be the input file.")
    return args.fn(args)


if __name__ == "__main__":
    sys.exit(main())
