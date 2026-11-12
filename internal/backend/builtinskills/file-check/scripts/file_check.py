#!/usr/bin/env python3
"""Check a file you produced — any file.

    file_check.py FILE [--against ORIGINAL] [--png DIR]

What the file is, is read from its bytes, not from its name. Then:

  * it is opened the way its kind is opened — a PDF as pages, a presentation as
    slides, a document or workbook as its text and sheets, an image as pixels,
    JSON / XML / CSV by parsing, a script by a syntax check, anything else as
    text or bytes — and what is in it is printed, so it can be read back;
  * whatever is wrong at that level is reported: does not open, does not parse,
    empty, blank page, text outside its box, a row with the wrong number of
    cells;
  * with --against ORIGINAL (the file this one was made from) the two are
    compared and every difference is listed, so "only what was asked for
    changed" is something read off a report and not assumed.

Formats with a checker of their own (see the pdf and ppt skills) are handed to
it; this is the front door for all of them and the fallback for the rest.

The last line is "RESULT: OK" or "RESULT: N problem(s)"; exit status 0 or 1.
OK means the file is sound as a file. Whether it says what the user asked for
is still yours to read from the printout.
"""
import argparse
import csv
import difflib
import hashlib
import io
import json
import os
import re
import subprocess
import sys
import zipfile
from xml.etree import ElementTree

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))
for sub in ("pdf", "ppt"):
    sys.path.insert(0, os.path.join(ROOT, sub, "scripts"))

W_NS = "{http://schemas.openxmlformats.org/wordprocessingml/2006/main}"
S_NS = "{http://schemas.openxmlformats.org/spreadsheetml/2006/main}"


def sniff(path):
    """The kind of file, from its content."""
    with open(path, "rb") as f:
        head = f.read(4096)
    if not head:
        return "empty"
    if head.startswith(b"%PDF"):
        return "pdf"
    if head.startswith(b"PK\x03\x04"):
        try:
            names = zipfile.ZipFile(path).namelist()
        except zipfile.BadZipFile:
            return "broken-zip"
        if any(n.startswith("ppt/") for n in names):
            return "pptx"
        if any(n.startswith("word/") for n in names):
            return "docx"
        if any(n.startswith("xl/") for n in names):
            return "xlsx"
        return "zip"
    if head.startswith((b"\x89PNG", b"\xff\xd8\xff", b"GIF8", b"BM")) or (head[:4] == b"RIFF" and head[8:12] == b"WEBP"):
        return "image"
    try:
        text = head.decode("utf-8")
    except UnicodeDecodeError:
        try:
            # A cut in the middle of a character at the end of the sample is not binary.
            text = head[:-3].decode("utf-8")
        except UnicodeDecodeError:
            return "binary"
    if "\x00" in text:
        return "binary"
    stripped = text.lstrip()
    if stripped.startswith("#!"):
        return "script"
    if stripped[:1] in "{[":
        return "json"
    if stripped.startswith("<"):
        return "markup"
    return "text"


def read_text(path):
    with open(path, "rb") as f:
        raw = f.read()
    try:
        return raw.decode("utf-8"), None
    except UnicodeDecodeError as e:
        return raw.decode("utf-8", "replace"), "it is not valid UTF-8 (first bad byte at offset %d)" % e.start


def show(lines, limit=200):
    for line in lines[:limit]:
        print("    | " + line.rstrip("\n")[:160])
    if len(lines) > limit:
        print("    | ... %d more line(s)" % (len(lines) - limit))


def docx_lines(path):
    z = zipfile.ZipFile(path)
    root = ElementTree.fromstring(z.read("word/document.xml"))
    out = []
    for p in root.iter(W_NS + "p"):
        text = "".join(t.text or "" for t in p.iter(W_NS + "t"))
        if text.strip():
            out.append(text)
    return out


def xlsx_lines(path):
    """One line per non-empty row of every sheet: 'Sheet!A1: a | b | c'."""
    z = zipfile.ZipFile(path)
    shared = []
    if "xl/sharedStrings.xml" in z.namelist():
        for si in ElementTree.fromstring(z.read("xl/sharedStrings.xml")).iter(S_NS + "si"):
            shared.append("".join(t.text or "" for t in si.iter(S_NS + "t")))
    names = [s.get("name") for s in ElementTree.fromstring(z.read("xl/workbook.xml")).iter(S_NS + "sheet")]
    sheets = sorted((n for n in z.namelist() if re.match(r"xl/worksheets/sheet\d+\.xml$", n)),
                    key=lambda n: int(re.findall(r"\d+", n)[-1]))
    out = []
    for idx, sheet in enumerate(sheets):
        name = names[idx] if idx < len(names) else sheet
        for row in ElementTree.fromstring(z.read(sheet)).iter(S_NS + "row"):
            cells = []
            for c in row.iter(S_NS + "c"):
                v = c.find(S_NS + "v")
                f = c.find(S_NS + "f")
                val = v.text if v is not None and v.text is not None else ""
                if c.get("t") == "s" and val.isdigit() and int(val) < len(shared):
                    val = shared[int(val)]
                elif c.get("t") == "inlineStr":
                    val = "".join(t.text or "" for t in c.iter(S_NS + "t"))
                if c.get("t") == "e":
                    val = "ERROR " + val
                if f is not None and f.text:
                    val = "%s (=%s)" % (val, f.text)
                cells.append(val)
            if any(cells):
                out.append("%s!%s: %s" % (name, row.get("r", "?"), " | ".join(cells)))
    return out


def content_lines(path, kind):
    """The file as lines of text, for printing and for comparing."""
    if kind == "docx":
        return docx_lines(path)
    if kind == "xlsx":
        return xlsx_lines(path)
    if kind in ("text", "json", "markup", "script"):
        return read_text(path)[0].splitlines()
    return None


def check_one(path, kind, png):
    problems = []
    size = os.path.getsize(path)
    print("%s: %s, %d bytes" % (path, kind, size))
    if kind == "empty":
        return ["the file is empty"]
    if kind == "broken-zip":
        return ["the file starts like a zip archive (docx, xlsx, pptx are zips) but does not open: it was cut off or corrupted"]
    if kind == "pdf":
        import pdf_check
        return pdf_check.describe(path, png)
    if kind == "pptx":
        import deck_check
        return deck_check.check(path, None, png, True)
    if kind in ("docx", "xlsx", "zip"):
        bad = zipfile.ZipFile(path).testzip()
        if bad:
            problems.append("the archive is damaged at %s" % bad)
        if kind == "zip":
            names = zipfile.ZipFile(path).namelist()
            show(names)
            if not names:
                problems.append("the archive has nothing in it")
            return problems
        try:
            lines = content_lines(path, kind)
        except Exception as e:
            return problems + ["its content could not be read: %s" % e]
        print("  %d %s" % (len(lines), "paragraph(s) of text" if kind == "docx" else "non-empty row(s)"))
        show(lines)
        if not lines:
            problems.append("there is no text in it" if kind == "docx" else "every sheet is empty")
        for line in lines:
            if kind == "xlsx" and "ERROR " in line:
                problems.append("a cell holds an error value: " + line[:100])
        return problems
    if kind == "image":
        try:
            try:
                import pymupdf as fitz
            except ImportError:
                import fitz
            pix = fitz.Pixmap(path)
            print("  %d x %d px" % (pix.width, pix.height))
            samples, n = pix.samples, pix.n
            step = max(n, (len(samples) // 5000) // n * n)
            if all(samples[k:k + n] == samples[:n] for k in range(0, len(samples) - n, step)):
                problems.append("the image is one flat colour: nothing was drawn")
        except Exception as e:
            problems.append("the image does not open: %s" % e)
        return problems
    if kind == "binary":
        with open(path, "rb") as f:
            print("  sha256 %s" % hashlib.sha256(f.read()).hexdigest())
        print("  a binary file this script has no reader for: open it with the program it is meant for and read what that says")
        return problems

    text, bad = read_text(path)
    if bad:
        problems.append(bad)
    lines = text.splitlines()
    print("  %d line(s)" % len(lines))
    if not text.strip():
        problems.append("the file holds only whitespace")
    ext = os.path.splitext(path)[1].lower()
    if kind == "json" or ext == ".json":
        try:
            data = json.loads(text)
            shape = "%d key(s)" % len(data) if isinstance(data, dict) else "%d item(s)" % len(data) if isinstance(data, list) else type(data).__name__
            print("  parses as JSON: " + shape)
        except ValueError as e:
            if ext in (".json", ""):
                problems.append("it does not parse as JSON: %s" % e)
    elif kind == "markup" and ext in (".xml", ".svg", ".xhtml", ".plist"):
        try:
            ElementTree.fromstring(text.encode("utf-8"))
            print("  parses as XML")
        except ElementTree.ParseError as e:
            problems.append("it does not parse as XML: %s" % e)
    elif ext in (".csv", ".tsv"):
        rows = list(csv.reader(io.StringIO(text), delimiter="\t" if ext == ".tsv" else ","))
        widths = {}
        for r in rows:
            widths[len(r)] = widths.get(len(r), 0) + 1
        print("  %d row(s), %s column(s)" % (len(rows), "/".join(str(w) for w in sorted(widths))))
        if len(widths) > 1:
            usual = max(widths, key=widths.get)
            odd = [i + 1 for i, r in enumerate(rows) if len(r) != usual][:5]
            problems.append("rows do not all have %d cells; see row(s) %s" % (usual, ", ".join(map(str, odd))))
    elif kind == "script" or ext in (".py", ".sh", ".bash"):
        first = lines[0] if lines else ""
        cmd = None
        if ext == ".py" or "python" in first:
            cmd = [sys.executable, "-c", "import ast,sys; ast.parse(open(sys.argv[1], encoding='utf-8').read(), sys.argv[1])", path]
        elif ext in (".sh", ".bash") or re.search(r"\b(ba|z|da)?sh\b", first):
            cmd = ["sh", "-n", path]
        if cmd:
            done = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=60)
            if done.returncode != 0:
                problems.append("it has a syntax error: " + done.stdout.decode("utf-8", "replace").strip()[-300:])
            else:
                print("  syntax is valid (it was not run: running it is the real test)")
    show(lines)
    return problems


def compare(orig, new, kind, png):
    okind = sniff(orig)
    print("")
    print("--- compared with %s ---" % orig)
    if okind != kind:
        print("the original is %s and this is %s: they are compared as far as their text goes" % (okind, kind))
    if kind == "pdf" and okind == "pdf":
        import pdf_check
        return pdf_check.compare(orig, new, [], png_dir=png)
    with open(orig, "rb") as f1, open(new, "rb") as f2:
        if f1.read() == f2.read():
            print("the two files are identical, byte for byte: nothing was changed")
            return []
    a, b = None, None
    try:
        a, b = content_lines(orig, okind), content_lines(new, kind)
        if okind == "pptx" or kind == "pptx":
            a = a if a is not None else pptx_lines(orig)
            b = b if b is not None else pptx_lines(new)
    except Exception as e:
        return ["the two files could not be compared: %s" % e]
    if a is None or b is None:
        print("no text to compare in this kind of file; sizes: %d -> %d bytes" % (os.path.getsize(orig), os.path.getsize(new)))
        return []
    diff = [l for l in difflib.unified_diff(a, b, "original", "this file", lineterm="", n=0)
            if not l.startswith(("---", "+++", "@@"))]
    removed = sum(1 for l in diff if l.startswith("-"))
    added = sum(1 for l in diff if l.startswith("+"))
    print("%d line(s) removed or changed, %d line(s) added or changed, %d unchanged" % (
        removed, added, len(a) - removed))
    for l in diff[:120]:
        print("    " + l[:170])
    if len(diff) > 120:
        print("    ... %d more" % (len(diff) - 120))
    print("Every line above is a difference. Each one has to be something that was asked for.")
    return []


def pptx_lines(path):
    from pptx import Presentation
    out = []
    for n, slide in enumerate(Presentation(path).slides, 1):
        for shape in slide.shapes:
            if shape.has_text_frame:
                for line in shape.text_frame.text.split("\n"):
                    if line.strip():
                        out.append("slide %d: %s" % (n, line.strip()))
    return out


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("file")
    ap.add_argument("--against", metavar="ORIGINAL", help="the file this one was made from")
    ap.add_argument("--png", metavar="DIR", help="for pages and slides: also write them as images into DIR")
    args = ap.parse_args()
    if not os.path.isfile(args.file):
        print("PROBLEM: %s does not exist" % args.file)
        print("RESULT: 1 problem(s)")
        return 1
    kind = sniff(args.file)
    try:
        problems = list(check_one(args.file, kind, args.png) or [])
    except Exception as e:
        problems = ["it could not be opened as %s: %s" % (kind, e)]
    if args.against:
        if not os.path.isfile(args.against):
            problems.append("the original %s does not exist" % args.against)
        else:
            problems += compare(args.against, args.file, kind, args.png)
    # The format checkers print their own PROBLEM lines; print the rest here.
    if kind not in ("pdf", "pptx"):
        for p in problems:
            print("PROBLEM: " + p)
    if problems:
        print("RESULT: %d problem(s)" % len(problems))
        return 1
    print("RESULT: OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
