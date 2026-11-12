#!/usr/bin/env python3
"""Read a .pptx back and say what is wrong with it.

    deck_check.py DECK.pptx [--slides N] [--png DIR] [--no-render]

Prints the deck as an outline — every slide's title, text, charts with their
numbers, tables — so what was built can be compared with what was meant, then
checks:

  * the slide count against --slides N (two either side is "about N");
  * slides with no title, nothing on them, or the same title as another;
  * text that does not fit its box, and shapes that hang off the slide;
  * charts with no data or with a series of the wrong length;
  * charts, tables and figures with no source line;
  * that the file renders. With LibreOffice on the machine the deck is converted
    by it and every page checked for being blank. Without it, each slide is
    drawn by this script from the shapes in the file — text wrapped with real
    font metrics, tables, charts from their numbers — which is enough to see
    overlap and overflow, and is not what PowerPoint will show to the pixel.
    --png DIR keeps the pages as images either way, for a person to open.

LibreOffice is optional. To get it: macOS `brew install --cask libreoffice`,
Debian/Ubuntu `sudo apt-get install -y libreoffice-impress`.

The last line is "RESULT: OK" or "RESULT: N problem(s)"; exit status 0 or 1.
"""
import argparse
import math
import os
import shutil
import subprocess
import sys
import tempfile

try:
    from pptx import Presentation
    from pptx.util import Emu
except ImportError:
    sys.exit("python-pptx is missing. Run this through docpy (see the skill), which installs it.")

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from deck_build import plain  # noqa: E402

EMU_IN = 914400.0


def load_fitz():
    """PyMuPDF under its current name, or its old one."""
    try:
        import pymupdf
        return pymupdf
    except ImportError:
        import fitz
        return fitz


def find_soffice():
    """A LibreOffice that actually runs.

    Being on PATH is not enough: Homebrew leaves its `soffice` wrapper behind
    when the app is deleted, and the wrapper then fails on every call.
    """
    candidates = [shutil.which("soffice"), shutil.which("libreoffice"),
                  "/Applications/LibreOffice.app/Contents/MacOS/soffice",
                  "/usr/local/bin/soffice", "/usr/bin/soffice",
                  "C:/Program Files/LibreOffice/program/soffice.exe"]
    for path in candidates:
        if not path or not os.path.exists(path):
            continue
        try:
            done = subprocess.run([path, "--version"], stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30)
            if done.returncode == 0:
                return path
        except Exception:
            continue
    return None


# --- drawing a slide from its shapes ---------------------------------------
#
# Used when there is no LibreOffice, and always for measuring text: the same
# wrapping that draws the preview says whether the words fit their box.

_fonts = {}


def draw_font(text):
    fitz = load_fitz()
    key = "cjk" if any(ord(c) > 0x2E7F for c in text) else "helv"
    if key not in _fonts:
        _fonts[key] = fitz.Font(key)
    return _fonts[key]


def wrap(text, font, size, width):
    """Break text into lines no wider than width (points)."""
    lines, cur = [], ""
    # Latin breaks between words, CJK between any two characters.
    tokens = []
    word = ""
    for ch in text:
        if ord(ch) > 0x2E7F or ch == " ":
            if word:
                tokens.append(word)
                word = ""
            tokens.append(ch)
        else:
            word += ch
    if word:
        tokens.append(word)
    for tok in tokens:
        if font.text_length(cur + tok, fontsize=size) <= width or not cur.strip():
            cur += tok
            continue
        lines.append(cur.rstrip())
        cur = tok.lstrip()
    if cur.strip() or not lines:
        lines.append(cur.rstrip())
    return lines


def para_props(p):
    sizes = [r.font.size.pt for r in p.runs if r.font.size is not None]
    size = max(sizes) if sizes else 18.0
    indent, bullet = 0.0, ""
    ppr = p._p.pPr
    if ppr is not None:
        if ppr.get("marL"):
            indent = int(ppr.get("marL")) / 12700.0
        for child in ppr:
            if child.tag.endswith("}buChar"):
                bullet = child.get("char", "")
    colour = (0.07, 0.09, 0.15)
    for r in p.runs:
        try:
            if r.font.color is not None and r.font.color.type is not None:
                c = r.font.color.rgb
                colour = (c[0] / 255.0, c[1] / 255.0, c[2] / 255.0)
                break
        except Exception:
            pass
    return size, indent, bullet, colour


def layout_frame(tf, width_pt):
    """[(text, size, x_offset, bullet, colour, align, gap_after)] line by line, and the height."""
    out, height = [], 0.0
    inner = width_pt - (tf.margin_left + tf.margin_right) / 12700.0
    for p in tf.paragraphs:
        text = "".join(r.text for r in p.runs)
        size, indent, bullet, colour = para_props(p)
        if not text.strip():
            height += size * 1.2
            continue
        font = draw_font(text)
        spacing = p.line_spacing if isinstance(p.line_spacing, float) else 1.0
        lines = wrap(text, font, size, max(20.0, inner - indent))
        after = p.space_after.pt if p.space_after is not None else 0.0
        for k, line in enumerate(lines):
            out.append((line, size, indent, bullet if k == 0 else "", colour, p.alignment,
                        after if k == len(lines) - 1 else 0.0, spacing))
            height += size * 1.2 * spacing
        height += after
    return out, height


def frame_overflow(shape):
    """Inches by which a text box's text exceeds the box, or 0."""
    tf = shape.text_frame
    _, need = layout_frame(tf, shape.width / 12700.0)
    box = shape.height / 12700.0
    # Half a line of slack: other renderers space lines a little differently.
    return max(0.0, (need - box - 9.0) / 72.0)


def fill_colour(fill):
    try:
        if fill.type == 1:
            c = fill.fore_color.rgb
            return (c[0] / 255.0, c[1] / 255.0, c[2] / 255.0)
    except Exception:
        pass
    return None


def draw_frame(page, tf, x, y, w, h):
    fitz = load_fitz()
    lines, need = layout_frame(tf, w)
    x += tf.margin_left / 12700.0
    w -= (tf.margin_left + tf.margin_right) / 12700.0
    anchor = str(tf.vertical_anchor or "")
    cy = y + tf.margin_top / 12700.0
    if "MIDDLE" in anchor:
        cy = y + max(0.0, (h - need) / 2.0)
    elif "BOTTOM" in anchor:
        cy = y + max(0.0, h - need)
    for text, size, indent, bullet, colour, align, after, spacing in lines:
        font = draw_font(text)
        cy += size * 1.2 * spacing
        base = cy - size * 0.28
        tw = fitz.TextWriter(page.rect, color=colour)
        width = font.text_length(text, fontsize=size)
        tx = x + indent
        if "RIGHT" in str(align or ""):
            tx = x + w - width
        elif "CENTER" in str(align or ""):
            tx = x + (w - width) / 2.0
        if bullet:
            bf = draw_font(bullet)
            tw.append(fitz.Point(x + indent - 18.0, base), bullet, font=bf, fontsize=size)
        if text:
            tw.append(fitz.Point(tx, base), text, font=font, fontsize=size)
        tw.write_text(page)
        cy += after


def draw_chart(page, chart, x, y, w, h):
    fitz = load_fitz()
    palette = [(0.15, 0.39, 0.92), (0.05, 0.65, 0.64), (0.96, 0.62, 0.04), (0.49, 0.23, 0.93),
               (0.94, 0.27, 0.27), (0.39, 0.45, 0.55), (0.06, 0.73, 0.51), (0.86, 0.15, 0.47)]
    plot = chart.plots[0]
    cats = [str(c) for c in plot.categories]
    series = [(s.name, [v or 0 for v in s.values]) for s in plot.series]
    kind = str(chart.chart_type)
    grey = (0.42, 0.45, 0.5)
    small = 9.0

    def label(px, py, text, colour=grey, size=small, centre=True):
        font = draw_font(text)
        tw = fitz.TextWriter(page.rect, color=colour)
        tx = px - font.text_length(text, fontsize=size) / 2.0 if centre else px
        tw.append(fitz.Point(tx, py), text, font=font, fontsize=size)
        tw.write_text(page)

    if not cats or not series:
        label(x + w / 2, y + h / 2, "[chart with no data]")
        return
    if "PIE" in kind or "DOUGHNUT" in kind:
        vals = series[0][1]
        total = sum(vals) or 1.0
        r = min(w * 0.3, h * 0.42)
        centre = fitz.Point(x + w * 0.4, y + h / 2)
        start = fitz.Point(centre.x, centre.y - r)
        for i, v in enumerate(vals):
            sweep = -360.0 * v / total
            if abs(sweep) >= 359.99:
                page.draw_circle(centre, r, color=None, fill=palette[i % len(palette)])
                break
            start = page.draw_sector(centre, start, sweep, color=(1, 1, 1), fill=palette[i % len(palette)], width=0.8)
        for i, c in enumerate(cats):
            ly = y + h * 0.25 + i * 16
            page.draw_rect(fitz.Rect(x + w * 0.78, ly - 8, x + w * 0.78 + 9, ly + 1), color=None, fill=palette[i % len(palette)])
            label(x + w * 0.78 + 14, ly, "%s  %.1f%%" % (c, 100.0 * vals[i] / total), centre=False, size=10)
        return
    legend_h = 18.0 if len(series) > 1 else 0.0
    left, right, top, bottom = x + 34, x + w - 8, y + 16, y + h - 22 - legend_h
    stacked = "STACKED" in kind
    horizontal = "BAR" in kind
    if stacked:
        peak = max(sum(vs[i] for _, vs in series) for i in range(len(cats)))
    else:
        peak = max(max(vs) for _, vs in series)
    low = min(0.0, min(min(vs) for _, vs in series))
    span = (peak - low) or 1.0
    span *= 1.12
    for g in range(5):
        gy = bottom - (bottom - top) * g / 4.0
        if not horizontal:
            page.draw_line(fitz.Point(left, gy), fitz.Point(right, gy), color=(0.9, 0.91, 0.92), width=0.6)
            label(left - 4 - draw_font("0").text_length("%g" % round(low + span * g / 4.0, 2), fontsize=small),
                  gy + 3, "%g" % round(low + span * g / 4.0, 2), centre=False)
    n = len(cats)
    if horizontal:
        band = (bottom - top) / n
        for i, c in enumerate(cats):
            for k, (_, vs) in enumerate(series):
                bh = band * 0.7 / len(series)
                by = top + band * i + band * 0.15 + bh * k
                bw = (right - left - 60) * (vs[i] - low) / span
                page.draw_rect(fitz.Rect(left + 50, by, left + 50 + bw, by + bh), color=None, fill=palette[k % len(palette)])
                label(left + 54 + bw, by + bh * 0.75, "%g" % vs[i], centre=False)
            label(left - 30, top + band * i + band * 0.6, c[:10], centre=False)
    else:
        band = (right - left) / n
        if "LINE" in kind or "AREA" in kind:
            for k, (_, vs) in enumerate(series):
                pts = [fitz.Point(left + band * (i + 0.5), bottom - (bottom - top) * (vs[i] - low) / span) for i in range(n)]
                for a, b in zip(pts, pts[1:]):
                    page.draw_line(a, b, color=palette[k % len(palette)], width=2.0)
                for i, pt in enumerate(pts):
                    page.draw_circle(pt, 2.5, color=palette[k % len(palette)], fill=palette[k % len(palette)])
                    if n * len(series) <= 24:
                        label(pt.x, pt.y - 6, "%g" % vs[i], colour=(0.07, 0.09, 0.15))
        else:
            for i in range(n):
                acc = 0.0
                for k, (_, vs) in enumerate(series):
                    hgt = (bottom - top) * (vs[i] - (0 if stacked else low)) / span
                    if stacked:
                        bw = band * 0.6
                        bx = left + band * i + band * 0.2
                        page.draw_rect(fitz.Rect(bx, bottom - acc - hgt, bx + bw, bottom - acc), color=None, fill=palette[k % len(palette)])
                        acc += hgt
                    else:
                        bw = band * 0.7 / len(series)
                        bx = left + band * i + band * 0.15 + bw * k
                        page.draw_rect(fitz.Rect(bx, bottom - hgt, bx + bw, bottom), color=None, fill=palette[k % len(palette)])
                        if n * len(series) <= 24:
                            label(bx + bw / 2, bottom - hgt - 3, "%g" % vs[i], colour=(0.07, 0.09, 0.15))
        for i, c in enumerate(cats):
            label(left + band * (i + 0.5), bottom + 12, c[:12])
    if legend_h:
        lx = left
        for k, (name, _) in enumerate(series):
            page.draw_rect(fitz.Rect(lx, y + h - 12, lx + 9, y + h - 3), color=None, fill=palette[k % len(palette)])
            label(lx + 13, y + h - 4, str(name), centre=False, size=10)
            lx += 26 + draw_font(str(name)).text_length(str(name), fontsize=10)


def draw_slide(doc, prs, slide):
    """One slide as a PDF page; returns problems seen while drawing."""
    fitz = load_fitz()
    problems = []
    sw, sh = prs.slide_width / 12700.0, prs.slide_height / 12700.0
    page = doc.new_page(width=sw, height=sh)
    bg = fill_colour(slide.background.fill)
    if bg:
        page.draw_rect(page.rect, color=None, fill=bg)
    for shape in slide.shapes:
        if shape.left is None or shape.width is None:
            continue
        x, y, w, h = (v / 12700.0 for v in (shape.left, shape.top, shape.width, shape.height))
        if getattr(shape, "has_chart", False) and shape.has_chart:
            try:
                draw_chart(page, shape.chart, x, y, w, h)
            except Exception as e:
                problems.append("a chart could not be drawn: %s" % e)
            continue
        if getattr(shape, "has_table", False) and shape.has_table:
            table = shape.table
            cy = y
            for row in table.rows:
                cx = x
                need = row.height / 12700.0
                for ci, cell in enumerate(row.cells):
                    cw = table.columns[ci].width / 12700.0
                    _, th = layout_frame(cell.text_frame, cw)
                    need = max(need, th + (cell.margin_top + cell.margin_bottom) / 12700.0)
                for ci, cell in enumerate(row.cells):
                    cw = table.columns[ci].width / 12700.0
                    fc = fill_colour(cell.fill)
                    page.draw_rect(fitz.Rect(cx, cy, cx + cw, cy + need), color=(0.9, 0.91, 0.92), fill=fc, width=0.5)
                    draw_frame(page, cell.text_frame, cx, cy, cw, need)
                    cx += cw
                cy += need
            if cy > sh - 36:
                problems.append("the table runs to the bottom edge of the slide")
            continue
        if shape.shape_type == 13:
            try:
                page.insert_image(fitz.Rect(x, y, x + w, y + h), stream=shape.image.blob)
            except Exception:
                page.draw_rect(fitz.Rect(x, y, x + w, y + h), color=(0.6, 0.6, 0.6))
            continue
        if shape.shape_type == 5:
            # A freeform (a pie slice): its outline is in the shape's own units.
            fc = fill_colour(shape.fill)
            try:
                geom = shape._element.spPr.custGeom
                path = geom.find(".//{http://schemas.openxmlformats.org/drawingml/2006/main}path")
                pw, ph = float(path.get("w")), float(path.get("h"))
                pts = [fitz.Point(x + w * float(pt.get("x")) / pw, y + h * float(pt.get("y")) / ph)
                       for pt in path.iter("{http://schemas.openxmlformats.org/drawingml/2006/main}pt")]
                if fc and len(pts) >= 3:
                    page.draw_polyline(pts + [pts[0]], color=(1, 1, 1), fill=fc, width=1.0)
            except Exception:
                pass
            continue
        if shape.shape_type == 1:
            fc = fill_colour(shape.fill)
            kind = str(shape.auto_shape_type)
            if fc and "PIE" in kind:
                start, end = shape.adjustments[0] / 0.6, shape.adjustments[1] / 0.6
                sweep = (end - start) % 360.0
                centre = fitz.Point(x + w / 2, y + h / 2)
                rad = math.radians(start)
                first = fitz.Point(centre.x + w / 2 * math.cos(rad), centre.y + h / 2 * math.sin(rad))
                page.draw_sector(centre, first, -sweep, color=(1, 1, 1), fill=fc, width=1.0)
            elif fc and "OVAL" in kind:
                page.draw_oval(fitz.Rect(x, y, x + w, y + h), color=None, fill=fc)
            elif fc:
                page.draw_rect(fitz.Rect(x, y, x + w, y + h), color=None, fill=fc)
        if shape.has_text_frame and shape.text_frame.text.strip():
            draw_frame(page, shape.text_frame, x, y, w, h)
    return problems


def preview(path, png_dir):
    """Draw every slide with this script's own renderer."""
    fitz = load_fitz()
    problems = []
    prs = Presentation(path)
    doc = fitz.open()
    for n, slide in enumerate(prs.slides, 1):
        for msg in draw_slide(doc, prs, slide):
            print("PROBLEM: slide %d: %s" % (n, msg))
            problems.append("slide %d: %s" % (n, msg))
    if png_dir:
        os.makedirs(png_dir, exist_ok=True)
        for i, page in enumerate(doc, 1):
            page.get_pixmap(matrix=fitz.Matrix(1.5, 1.5), alpha=False).save(os.path.join(png_dir, "slide-%02d.png" % i))
    print("drew %d slide(s) with the built-in preview (no LibreOffice here; PowerPoint's own rendering may differ slightly)%s" % (
        len(doc), (": " + png_dir) if png_dir else ""))
    return problems


def check(path, want_slides=None, png_dir=None, render=True):
    problems = []
    try:
        prs = Presentation(path)
    except Exception as e:
        print("PROBLEM: %s does not open as a presentation: %s" % (path, e))
        return ["the file does not open"]
    sw, sh = prs.slide_width, prs.slide_height
    slides = list(prs.slides)
    print("%s: %d slides, %.2f x %.2f in" % (path, len(slides), sw / EMU_IN, sh / EMU_IN))

    def problem(msg):
        print("    PROBLEM: " + msg)
        problems.append(msg)

    titles = {}
    data_slides = 0
    for n, slide in enumerate(slides, 1):
        texts, charts, tables, pictures = [], [], [], 0
        drawn = 0  # charts built from shapes (pies): named "chart:..." by deck_build
        for shape in slide.shapes:
            if (shape.name or "").startswith("chart:"):
                drawn += 1
            if shape.shape_type == 13:
                pictures += 1
            if getattr(shape, "has_chart", False) and shape.has_chart:
                charts.append(shape.chart)
            if getattr(shape, "has_table", False) and shape.has_table:
                tables.append(shape.table)
            if shape.has_text_frame and shape.text_frame.text.strip():
                sizes = [r.font.size.pt for p in shape.text_frame.paragraphs for r in p.runs if r.font.size]
                texts.append((shape, shape.text_frame.text.strip(), max(sizes) if sizes else 0))
        # The title is the largest text near the top; a cover has it lower.
        title = ""
        if texts:
            top = [t for t in texts if t[0].top / EMU_IN < 2.0 and t[2] >= 20] or texts
            title = max(top, key=lambda t: t[2])[1].split("\n")[0]
        print("  %2d. %s" % (n, title or "(no title)"))
        for shape, text, size in texts:
            if text.split("\n")[0] == title:
                continue
            for line in text.split("\n"):
                if line.strip():
                    print("        %s" % line.strip()[:110])
        for chart in charts:
            try:
                plot = chart.plots[0]
                cats = [str(c) for c in plot.categories]
                print("        [chart] categories: %s" % ", ".join(cats)[:110])
                if not cats:
                    problem("slide %d: a chart has no categories" % n)
                for s in plot.series:
                    vals = list(s.values)
                    print("        [chart] %s: %s" % (s.name, ", ".join("%g" % v if v is not None else "-" for v in vals))[:120])
                    if not vals or all(v is None for v in vals):
                        problem("slide %d: chart series \"%s\" has no numbers" % (n, s.name))
                    elif len(vals) != len(cats):
                        problem("slide %d: chart series \"%s\" has %d values for %d categories" % (n, s.name, len(vals), len(cats)))
            except Exception as e:
                problem("slide %d: a chart could not be read: %s" % (n, e))
        for table in tables:
            for r in table.rows:
                print("        [table] " + " | ".join(c.text.strip() for c in r.cells)[:110])
        if pictures:
            print("        [%d picture(s)]" % pictures)
        if drawn:
            print("        [pie chart drawn from %d shapes; its numbers are the legend lines above]" % drawn)

        if not texts and not charts and not tables and not pictures and not drawn:
            problem("slide %d is empty" % n)
            continue
        if not title:
            problem("slide %d has no title" % n)
        elif title in titles and n > 1:
            problem("slides %d and %d have the same title: \"%s\"" % (titles[title], n, title[:50]))
        titles.setdefault(title, n)
        if len(texts) <= 2 and not charts and not tables and not pictures and not drawn and 1 < n < len(slides):
            body = " ".join(t[1] for t in texts if t[1].split("\n")[0] != title)
            if len(plain(body)) < 12 and all(t[2] < 30 for t in texts):
                problem("slide %d has a title and almost nothing under it" % n)
        for shape in slide.shapes:
            if shape.left is None or shape.width is None:
                continue
            if shape.left < -Emu(9144) or shape.top < -Emu(9144) or \
                    shape.left + shape.width > sw + Emu(9144) or shape.top + shape.height > sh + Emu(9144):
                problem("slide %d: \"%s\" hangs off the slide" % (n, (shape.name or "a shape")))
            if shape.has_text_frame and shape.text_frame.text.strip():
                over = frame_overflow(shape)
                if over > 0:
                    problem("slide %d: text runs %.1f in past its box: \"%s...\"" % (
                        n, over, shape.text_frame.text.strip().replace("\n", " ")[:40]))
        if charts or tables or drawn:
            data_slides += 1
            all_text = " ".join(t[1] for t in texts)
            if "来源" not in all_text and "source" not in all_text.lower():
                problem("slide %d shows data with no source line" % n)

    if want_slides:
        if abs(len(slides) - want_slides) > 2:
            msg = "%d slides were asked for and the deck has %d" % (want_slides, len(slides))
            print("PROBLEM: " + msg)
            problems.append(msg)
        else:
            print("slide count: %d (asked for about %d)" % (len(slides), want_slides))
    print("slides with charts or tables: %d" % data_slides)

    if render:
        problems += render_check(path, len(slides), png_dir)
    return problems


def render_check(path, slide_count, png_dir):
    problems = []
    soffice = find_soffice()
    try:
        fitz = load_fitz()
    except ImportError:
        print("not rendered: PyMuPDF is missing")
        return problems
    if not soffice:
        return preview(path, png_dir)
    tmp = tempfile.mkdtemp(prefix="deckcheck-")
    try:
        # Its own profile folder: a LibreOffice the user has open would
        # otherwise swallow the command and convert nothing.
        profile = "-env:UserInstallation=file://" + os.path.join(tmp, "profile").replace("\\", "/")
        proc = subprocess.run([soffice, profile, "--headless", "--convert-to", "pdf", "--outdir", tmp, path],
                              stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=240)
        pdf = os.path.join(tmp, os.path.splitext(os.path.basename(path))[0] + ".pdf")
        if not os.path.exists(pdf):
            msg = "LibreOffice could not convert the deck: " + proc.stdout.decode("utf-8", "replace").strip()[-200:]
            print("PROBLEM: " + msg)
            return [msg]
        doc = fitz.open(pdf)
        if len(doc) != slide_count:
            msg = "the deck has %d slides but renders as %d pages" % (slide_count, len(doc))
            print("PROBLEM: " + msg)
            problems.append(msg)
        if png_dir:
            os.makedirs(png_dir, exist_ok=True)
        for i, page in enumerate(doc, 1):
            pix = page.get_pixmap(matrix=fitz.Matrix(1.5, 1.5), alpha=False)
            if png_dir:
                pix.save(os.path.join(png_dir, "slide-%02d.png" % i))
            # A page that is one flat colour rendered nothing.
            samples = pix.samples
            step = max(pix.n, (len(samples) // 4000) // pix.n * pix.n)
            first = samples[:pix.n]
            if all(samples[k:k + pix.n] == first for k in range(0, len(samples) - pix.n, step)):
                msg = "slide %d renders blank" % i
                print("PROBLEM: " + msg)
                problems.append(msg)
        print("rendered %d page(s) with LibreOffice%s" % (len(doc), (": " + png_dir) if png_dir else ""))
    except subprocess.TimeoutExpired:
        print("not rendered: LibreOffice took too long")
    finally:
        shutil.rmtree(tmp, ignore_errors=True)
    return problems


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("deck")
    ap.add_argument("--slides", type=int, help="how many slides were asked for")
    ap.add_argument("--png", metavar="DIR", help="keep the rendered slides as PNGs in DIR")
    ap.add_argument("--no-render", action="store_true")
    args = ap.parse_args()
    problems = check(args.deck, args.slides, args.png, not args.no_render)
    if problems:
        print("RESULT: %d problem(s)" % len(problems))
        return 1
    print("RESULT: OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
