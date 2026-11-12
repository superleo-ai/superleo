#!/usr/bin/env python3
"""Build a .pptx from a JSON description of its slides.

    deck_build.py SPEC.json OUT.pptx

You write what each slide says; this decides where it goes. Every slide gets
the same margins, type sizes and colours, charts are real PowerPoint charts the
user can click and edit, and text is measured before it is placed: a slide
whose text cannot fit is refused, by number, instead of being written with the
words running off the bottom.

SPEC.json:

{
  "title": "2026 年中国储能行业研究报告",      deck title (also the first slide if none is given)
  "subtitle": "...", "author": "...", "date": "2026-11",
  "theme": "light",                             "light" (default) or "dark"
  "accent": "#2563EB",                          one accent colour
  "font": "...",                                optional; Arial + Microsoft YaHei when left out
  "slides": [ ... ]
}

Slide types (every slide takes "title", and optional "notes" for the speaker):

  {"type": "title", "title": "...", "subtitle": "...", "meta": "author · date"}
  {"type": "section", "title": "...", "subtitle": "..."}
  {"type": "bullets", "title": "...", "bullets": ["point", {"text": "point", "sub": ["detail"]}],
   "takeaway": "one sentence the slide proves", "source": "..."}
  {"type": "chart", "title": "...", "takeaway": "...",
   "chart": {"kind": "column|bar|line|pie|stacked|area", "categories": ["2022", "2023"],
             "series": [{"name": "新增装机 (GW)", "values": [7.3, 21.5]}],
             "number_format": "0.0", "unit": "GW"},
   "bullets": ["optional reading of the chart, shown beside it"],
   "source": "CNESA《储能产业研究白皮书 2025》"}
  {"type": "table", "title": "...", "columns": ["企业", "出货量"], "rows": [["宁德时代", "93 GWh"]],
   "source": "..."}
  {"type": "stats", "title": "...", "stats": [{"value": "73.8 GW", "label": "累计装机", "note": "同比 +130%"}],
   "source": "..."}
  {"type": "two_column", "title": "...", "left": {"heading": "机会", "bullets": [...]},
   "right": {"heading": "风险", "bullets": [...]}, "source": "..."}
  {"type": "sources", "title": "数据来源", "items": ["机构, 《报告》, 年份, URL"]}

Bold a few words with **double asterisks**.

Rules it enforces, because a deck that breaks them is not finished:
  * chart, table and stats slides carry a "source": where the numbers come from.
  * a chart has as many values in each series as it has categories, all numbers.
  * if no "sources" slide is given, one is added at the end from the slides' sources.
"""
import json
import math
import os
import re
import sys
import unicodedata

try:
    from pptx import Presentation
    from pptx.chart.data import CategoryChartData
    from pptx.dml.color import RGBColor
    from pptx.enum.chart import XL_CHART_TYPE, XL_LEGEND_POSITION, XL_LABEL_POSITION
    from pptx.enum.shapes import MSO_SHAPE
    from pptx.enum.text import MSO_ANCHOR, PP_ALIGN
    from pptx.oxml.ns import qn
    from pptx.util import Emu, Inches, Pt
except ImportError:
    sys.exit("python-pptx is missing. Run this through docpy (see the skill), which installs it.")

# 16:9, in inches.
W, H = 13.333, 7.5
MX = 0.75                  # left and right margin
TITLE_Y, TITLE_H = 0.5, 0.95
BODY_Y, BODY_BOTTOM = 1.75, 6.55
FOOT_Y = 6.9

THEMES = {
    "light": {"bg": "FFFFFF", "ink": "111827", "muted": "6B7280", "line": "E5E7EB", "panel": "F3F6FB"},
    "dark": {"bg": "0F172A", "ink": "F1F5F9", "muted": "94A3B8", "line": "334155", "panel": "1E293B"},
}
# Series colours after the accent: distinct in hue, even in weight.
PALETTE = ["0EA5A4", "F59E0B", "7C3AED", "EF4444", "64748B", "10B981", "DB2777"]


def rgb(hexstr):
    return RGBColor.from_string(hexstr.strip().lstrip("#").upper())


def is_wide(ch):
    return unicodedata.east_asian_width(ch) in ("W", "F")


def has_cjk(text):
    return any(is_wide(c) for c in text)


def units(text):
    """Width of text in ems, near enough to wrap by."""
    total = 0.0
    for ch in text:
        if is_wide(ch):
            total += 1.0
        elif ch == " ":
            total += 0.3
        elif ch.isupper() or ch.isdigit():
            total += 0.62
        elif ch in "il.,:;'|!":
            total += 0.3
        else:
            total += 0.53
    return total


def plain(text):
    return str(text).replace("**", "")


def line_count(text, size_pt, width_in):
    per_line = max(1.0, width_in * 72.0 / size_pt * 0.96)
    return max(1, int(math.ceil(units(plain(text)) / per_line)))


def norm_bullets(items):
    """[(level, text)] from strings, {"text","sub"} objects or nested lists."""
    out = []
    for item in items or []:
        if isinstance(item, dict):
            out.append((0, str(item.get("text", ""))))
            for sub in item.get("sub") or []:
                out.append((1, str(sub)))
        elif isinstance(item, (list, tuple)):
            for sub in item:
                out.append((1, str(sub)))
        else:
            out.append((0, str(item)))
    return [(lvl, t.strip()) for lvl, t in out if t and t.strip()]


def bullets_height(bullets, size, width_in):
    """Height in inches the bullet list needs at this size."""
    total = 0.0
    for level, text in bullets:
        s = size if level == 0 else size - 2
        w = width_in - (0.36 if level == 0 else 0.7)
        total += line_count(text, s, w) * s * 1.28 / 72.0 + s * 0.5 / 72.0
    return total


def fit_bullets(bullets, width_in, height_in, largest=20, smallest=14):
    """The largest size at which the list fits, or None when none does."""
    for size in range(largest, smallest - 1, -1):
        if bullets_height(bullets, size, width_in) <= height_in:
            return size
    return None


def nice_bounds(lo, hi, from_zero):
    """Axis limits a little outside the data, on round numbers."""
    if from_zero and lo >= 0:
        lo = 0.0
    span = (hi - lo) or abs(hi) or 1.0
    step = 10 ** math.floor(math.log10(span / 4.0))
    for m in (1, 2, 2.5, 5, 10):
        if span / (step * m) <= 6:
            step *= m
            break
    low = lo if (from_zero and lo == 0) else math.floor((lo - span * 0.08) / step) * step
    if lo >= 0 and low < 0:
        low = 0.0
    # Room above the tallest point for its label.
    high = math.ceil((hi + span * 0.15) / step) * step
    return round(low, 10), round(high, 10)


class Deck:
    def __init__(self, spec):
        self.spec = spec
        theme = THEMES.get(str(spec.get("theme", "light")).lower(), THEMES["light"])
        self.c = dict(theme)
        self.c["accent"] = str(spec.get("accent", "2563EB")).lstrip("#")
        everything = json.dumps(spec, ensure_ascii=False)
        # Two faces: Latin letters and digits in one every machine has, Chinese
        # in the one PowerPoint and WPS ship on both Windows and macOS. Naming
        # the Chinese face for Latin too turns every number into a serif on a
        # machine without it.
        self.font = spec.get("font") or "Arial"
        self.font_ea = spec.get("font") or ("Microsoft YaHei" if has_cjk(everything) else "Arial")
        self.prs = Presentation()
        self.prs.slide_width = Inches(W)
        self.prs.slide_height = Inches(H)
        self.blank = self.prs.slide_layouts[6]
        self.errors = []
        self.n = 0
        self.sources = []

    # --- primitives ---------------------------------------------------------

    def err(self, msg):
        self.errors.append("slide %d: %s" % (self.n, msg))

    def new_slide(self, notes=None):
        self.n += 1
        slide = self.prs.slides.add_slide(self.blank)
        bg = slide.background.fill
        bg.solid()
        bg.fore_color.rgb = rgb(self.c["bg"])
        if notes:
            slide.notes_slide.notes_text_frame.text = str(notes)
        return slide

    def style(self, run, size, color=None, bold=False):
        f = run.font
        f.size = Pt(size)
        f.bold = bold
        f.name = self.font
        f.color.rgb = rgb(color or self.c["ink"])
        # The East Asian face is a separate attribute; without it Chinese text
        # falls back to whatever the viewer picks.
        rpr = run._r.get_or_add_rPr()
        for tag in ("a:ea", "a:cs"):
            el = rpr.find(qn(tag))
            if el is None:
                el = rpr.makeelement(qn(tag), {})
                rpr.append(el)
            el.set("typeface", self.font_ea)

    def runs(self, para, text, size, color=None, bold=False):
        """Write text into a paragraph, **bold** spans included."""
        for i, part in enumerate(re.split(r"\*\*", str(text))):
            if not part:
                continue
            self.style(para.add_run(), size, color, bold or i % 2 == 1)
            para.runs[-1].text = part

    def box(self, slide, x, y, w, h, anchor=MSO_ANCHOR.TOP):
        tb = slide.shapes.add_textbox(Inches(x), Inches(y), Inches(w), Inches(h))
        tf = tb.text_frame
        tf.word_wrap = True
        tf.vertical_anchor = anchor
        tf.margin_left = tf.margin_right = tf.margin_top = tf.margin_bottom = 0
        return tf

    def text(self, slide, x, y, w, h, text, size, color=None, bold=False, align=PP_ALIGN.LEFT,
             anchor=MSO_ANCHOR.TOP):
        tf = self.box(slide, x, y, w, h, anchor)
        p = tf.paragraphs[0]
        p.alignment = align
        self.runs(p, text, size, color, bold)
        return tf

    def rect(self, slide, x, y, w, h, color):
        shape = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(x), Inches(y), Inches(w), Inches(h))
        shape.fill.solid()
        shape.fill.fore_color.rgb = rgb(color)
        shape.line.fill.background()
        shape.shadow.inherit = False
        return shape

    def title(self, slide, text):
        text = str(text or "").strip()
        if not text:
            self.err("no title")
            return
        size = None
        for s in (28, 26, 24, 22):
            if line_count(text, s, W - 2 * MX) <= (1 if s >= 26 else 2):
                size = s
                break
        if size is None:
            self.err("the title is too long (%d characters); a title is one line" % len(text))
            size = 22
        self.text(slide, MX, TITLE_Y, W - 2 * MX, TITLE_H, text, size, bold=True, anchor=MSO_ANCHOR.MIDDLE)
        self.rect(slide, MX, TITLE_Y + TITLE_H + 0.02, 0.6, 0.045, self.c["accent"])

    def footer(self, slide, source=None):
        if source:
            src = "来源：" if has_cjk(str(source) + self.spec.get("title", "")) else "Source: "
            text = src + str(source)
            if line_count(text, 10, W - 2 * MX - 1.0) > 2:
                self.err("the source line is too long for the footer; keep it to the organisation, report and year "
                         "and put the full reference on the sources slide")
            self.text(slide, MX, FOOT_Y - 0.12, W - 2 * MX - 1.0, 0.45, text, 10, self.c["muted"])
            if str(source) not in self.sources:
                self.sources.append(str(source))
        self.text(slide, W - MX - 0.8, FOOT_Y, 0.8, 0.3, str(self.n), 10, self.c["muted"], align=PP_ALIGN.RIGHT)

    def takeaway(self, slide, text, y):
        """The sentence the slide proves; returns the y where the body starts."""
        if not text:
            return y
        if line_count(text, 16, W - 2 * MX) > 2:
            self.err("the takeaway is too long; it is one sentence")
        lines = line_count(text, 16, W - 2 * MX)
        self.text(slide, MX, y, W - 2 * MX, 0.35 * lines, text, 16, self.c["accent"], bold=True)
        return y + 0.36 * lines + 0.18

    def bullets(self, slide, items, x, y, w, h, largest=20):
        bullets = norm_bullets(items)
        if not bullets:
            return
        size = fit_bullets(bullets, w, h, largest)
        if size is None:
            need = bullets_height(bullets, 14, w)
            self.err("too much text: the list needs %.1f in even at 14pt and the slide has %.1f in. "
                     "Cut it, or split it over two slides." % (need, h))
            size = 14
        tf = self.box(slide, x, y, w, h)
        first = True
        for level, text in bullets:
            p = tf.paragraphs[0] if first else tf.add_paragraph()
            first = False
            s = size if level == 0 else size - 2
            # A hanging indent: the bullet a little inside the title's left
            # edge, the text of every line of the point under itself.
            indent = 0.36 if level == 0 else 0.7
            hang = 0.26
            ppr = p._p.get_or_add_pPr()
            ppr.set("marL", str(int(Inches(indent))))
            ppr.set("indent", str(-int(Inches(hang))))
            for old in ppr.findall(qn("a:buNone")):
                ppr.remove(old)
            bu_clr = ppr.makeelement(qn("a:buClr"), {})
            srgb = bu_clr.makeelement(qn("a:srgbClr"), {"val": self.c["accent"] if level == 0 else self.c["muted"]})
            bu_clr.append(srgb)
            ppr.append(bu_clr)
            ppr.append(ppr.makeelement(qn("a:buFont"), {"typeface": "Arial"}))
            ppr.append(ppr.makeelement(qn("a:buChar"), {"char": "•" if level == 0 else "–"}))
            p.space_after = Pt(s * 0.5)
            p.line_spacing = 1.12
            self.runs(p, text, s, self.c["ink"] if level == 0 else self.c["muted"])

    # --- slide types --------------------------------------------------------

    def s_title(self, s):
        slide = self.new_slide(s.get("notes"))
        title = str(s.get("title") or self.spec.get("title") or "").strip()
        if not title:
            self.err("no title")
        size = 44
        for size in (44, 40, 36, 32):
            if line_count(title, size, W - 2 * MX - 1.5) <= 2:
                break
        else:
            self.err("the deck title is too long for the cover")
        self.rect(slide, MX, 2.35, 0.9, 0.07, self.c["accent"])
        lines = line_count(title, size, W - 2 * MX - 1.5)
        title_h = lines * size * 1.2 / 72.0
        self.text(slide, MX, 2.65, W - 2 * MX - 1.5, title_h + 0.1, title, size, bold=True)
        y = 2.65 + title_h + 0.3
        sub = s.get("subtitle") or self.spec.get("subtitle")
        if sub:
            self.text(slide, MX, y, W - 2 * MX - 1.5, 0.9, sub, 20, self.c["muted"])
            y += 0.45 * line_count(str(sub), 20, W - 2 * MX - 1.5) + 0.2
        meta = s.get("meta") or " · ".join(str(v) for v in (self.spec.get("author"), self.spec.get("date")) if v)
        if meta:
            self.text(slide, MX, 6.3, W - 2 * MX, 0.4, meta, 14, self.c["muted"])

    def s_section(self, s):
        slide = self.new_slide(s.get("notes"))
        self.rect(slide, 0, 0, W, H, self.c["panel"])
        self.sections = getattr(self, "sections", 0) + 1
        self.text(slide, MX, 2.3, 3, 1.0, "%02d" % self.sections, 54, self.c["accent"], bold=True)
        title = str(s.get("title") or "").strip()
        if not title:
            self.err("no title")
        if line_count(title, 36, W - 2 * MX) > 2:
            self.err("the section title is too long")
        self.text(slide, MX, 3.4, W - 2 * MX, 1.3, title, 36, bold=True)
        if s.get("subtitle"):
            y = 3.4 + 0.62 * line_count(title, 36, W - 2 * MX) + 0.15
            self.text(slide, MX, y, W - 2 * MX, 0.9, s["subtitle"], 18, self.c["muted"])
        self.footer(slide)

    def s_bullets(self, s):
        slide = self.new_slide(s.get("notes"))
        self.title(slide, s.get("title"))
        y = self.takeaway(slide, s.get("takeaway"), BODY_Y)
        if not norm_bullets(s.get("bullets")):
            self.err("no bullets: a bullets slide with nothing on it")
        self.bullets(slide, s.get("bullets"), MX, y, W - 2 * MX, BODY_BOTTOM - y)
        self.footer(slide, s.get("source"))

    def s_chart(self, s):
        slide = self.new_slide(s.get("notes"))
        self.title(slide, s.get("title"))
        y = self.takeaway(slide, s.get("takeaway"), BODY_Y)
        spec = s.get("chart") or {}
        cats = [str(c) for c in spec.get("categories") or []]
        series = spec.get("series") or []
        ok = True
        if not cats or not series:
            self.err("the chart has no data: it needs \"categories\" and \"series\"")
            ok = False
        for sr in series:
            vals = sr.get("values") or []
            if len(vals) != len(cats):
                self.err("series \"%s\" has %d values for %d categories" % (sr.get("name", ""), len(vals), len(cats)))
                ok = False
            if any(not isinstance(v, (int, float)) or isinstance(v, bool) for v in vals):
                self.err("series \"%s\" has values that are not numbers; put units in the series name" % sr.get("name", ""))
                ok = False
        if not s.get("source"):
            self.err("the chart has no \"source\": say where the numbers come from")
        side = norm_bullets(s.get("bullets"))
        chart_w = (W - 2 * MX) * (0.62 if side else 1.0)
        if side:
            bx = MX + chart_w + 0.4
            self.bullets(slide, s.get("bullets"), bx, y + 0.1, W - MX - bx, BODY_BOTTOM - y - 0.1, largest=16)
        if ok:
            self.chart(slide, spec, cats, series, MX, y, chart_w, BODY_BOTTOM - y)
        self.footer(slide, s.get("source"))

    def chart(self, slide, spec, cats, series, x, y, w, h):
        kind = str(spec.get("kind", "column")).lower()
        types = {
            "column": XL_CHART_TYPE.COLUMN_CLUSTERED, "bar": XL_CHART_TYPE.BAR_CLUSTERED,
            "line": XL_CHART_TYPE.LINE_MARKERS, "pie": XL_CHART_TYPE.PIE,
            "stacked": XL_CHART_TYPE.COLUMN_STACKED, "area": XL_CHART_TYPE.AREA,
            "doughnut": XL_CHART_TYPE.DOUGHNUT,
        }
        if kind not in types:
            self.err("unknown chart kind \"%s\"; use one of %s" % (kind, ", ".join(sorted(types))))
            return
        round_kind = kind in ("pie", "doughnut")
        if round_kind:
            if len(series) != 1:
                self.err("a %s chart shows one series" % kind)
                return
            self.pie(slide, spec, cats, list(series[0].get("values") or []), x, y, w, h, kind == "doughnut")
            return
        data = CategoryChartData()
        data.categories = cats
        for sr in series:
            data.add_series(str(sr.get("name", "")), list(sr.get("values") or []))
        frame = slide.shapes.add_chart(types[kind], Inches(x), Inches(y), Inches(w), Inches(h), data)
        chart = frame.chart
        chart.has_title = False
        chart.font.size = Pt(12)
        chart.font.name = self.font
        chart.font.color.rgb = rgb(self.c["ink"])
        colours = [self.c["accent"]] + PALETTE
        fmt = spec.get("number_format") or ("0.0" if any(isinstance(v, float) and v != int(v)
                                                         for sr in series for v in sr.get("values") or []) else "0")
        points = len(cats) * len(series)
        plot = chart.plots[0]
        chart.has_legend = len(series) > 1
        if chart.has_legend:
            chart.legend.position = XL_LEGEND_POSITION.BOTTOM
            chart.legend.include_in_layout = False
            chart.legend.font.size = Pt(12)
        for i, sr in enumerate(plot.series):
            colour = rgb(colours[i % len(colours)])
            if kind == "line":
                sr.format.line.color.rgb = colour
                sr.format.line.width = Pt(2.5)
                sr.smooth = False
                sr.marker.format.fill.solid()
                sr.marker.format.fill.fore_color.rgb = colour
                sr.marker.format.line.color.rgb = colour
            else:
                sr.format.fill.solid()
                sr.format.fill.fore_color.rgb = colour
        if kind in ("column", "bar", "stacked"):
            plot.gap_width = 80
            if kind != "stacked":
                plot.overlap = -10
        if points <= 24:
            plot.has_data_labels = True
            labels = plot.data_labels
            labels.number_format = fmt
            labels.number_format_is_linked = False
            labels.font.size = Pt(11)
            labels.font.color.rgb = rgb(self.c["ink"] if kind != "stacked" else "FFFFFF")
            if kind in ("column", "bar"):
                labels.position = XL_LABEL_POSITION.OUTSIDE_END
            elif kind == "line":
                labels.position = XL_LABEL_POSITION.ABOVE
        cat_axis, val_axis = chart.category_axis, chart.value_axis
        # The scale is set here, not left to the viewer: programs disagree
        # about where an automatic axis starts, and a line of values between
        # 0.45 and 0.62 drawn on an axis from 0 to 4 is a flat line.
        if kind == "stacked":
            tops = [sum(sr.get("values")[i] for sr in series) for i in range(len(cats))]
            lo, hi = nice_bounds(min(0.0, min(tops)), max(tops), True)
        else:
            flat = [v for sr in series for v in sr.get("values")]
            lo, hi = nice_bounds(min(flat), max(flat), kind != "line")
        val_axis.minimum_scale = lo
        val_axis.maximum_scale = hi
        cat_axis.tick_labels.font.size = Pt(12)
        cat_axis.format.line.color.rgb = rgb(self.c["line"])
        cat_axis.has_major_gridlines = False
        val_axis.tick_labels.font.size = Pt(11)
        val_axis.tick_labels.font.color.rgb = rgb(self.c["muted"])
        val_axis.tick_labels.number_format = fmt
        val_axis.tick_labels.number_format_is_linked = False
        val_axis.has_major_gridlines = True
        val_axis.major_gridlines.format.line.color.rgb = rgb(self.c["line"])
        val_axis.major_gridlines.format.line.width = Pt(0.75)
        val_axis.format.line.fill.background()
        if spec.get("unit"):
            val_axis.has_title = True
            val_axis.axis_title.text_frame.text = str(spec["unit"])
            for p in val_axis.axis_title.text_frame.paragraphs:
                for r in p.runs:
                    r.font.size = Pt(11)
                    r.font.bold = False
                    r.font.color.rgb = rgb(self.c["muted"])

    def pie(self, slide, spec, cats, values, x, y, w, h, doughnut):
        """A pie drawn from slice shapes, with its own legend.

        Not a chart object, on purpose. Apple's viewer — Quick Look, and so
        Mail and Files on the phone — draws a chart-object pie in one flat
        colour, or not at all once it has a legend. Bars and lines survive
        there; pies do not. Slices are plain shapes and look the same
        everywhere.
        """
        if any(v < 0 for v in values) or sum(values) <= 0:
            self.err("a pie needs values that are positive and add up to something")
            return
        if len(cats) > 8:
            self.err("a pie with %d slices cannot be read; keep the largest and fold the rest into one \"other\" slice" % len(cats))
            return
        colours = [self.c["accent"]] + PALETTE
        total = float(sum(values))
        d = min(h * 0.94, w * 0.5)
        cx, cy = x + 0.15, y + (h - d) / 2.0
        angle = 270.0  # twelve o'clock; angles run clockwise from three
        for i, v in enumerate(values):
            sweep = 360.0 * v / total
            if sweep <= 0:
                continue
            # Each slice is a polygon: the centre, then the arc as short
            # chords. The ready-made "pie" shape would be less code, and
            # Quick Look does not draw it.
            r = d / 2.0
            steps = max(2, int(sweep / 3.0))
            pts = []
            for k in range(steps + 1):
                a = math.radians(angle + sweep * k / steps)
                pts.append((Inches(cx + r + r * math.cos(a)), Inches(cy + r + r * math.sin(a))))
            if sweep >= 359.99:
                builder = slide.shapes.build_freeform(pts[0][0], pts[0][1])
                builder.add_line_segments(pts[1:], close=True)
            else:
                builder = slide.shapes.build_freeform(Inches(cx + r), Inches(cy + r))
                builder.add_line_segments(pts, close=True)
            shape = builder.convert_to_shape()
            shape.name = "chart:pie %s" % cats[i]
            shape.fill.solid()
            shape.fill.fore_color.rgb = rgb(colours[i % len(colours)])
            shape.line.color.rgb = rgb(self.c["bg"])
            shape.line.width = Pt(1.5)
            shape.shadow.inherit = False
            angle += sweep
        if doughnut:
            hole = d * 0.52
            shape = slide.shapes.add_shape(MSO_SHAPE.OVAL, Inches(cx + (d - hole) / 2), Inches(cy + (d - hole) / 2),
                                           Inches(hole), Inches(hole))
            shape.name = "chart:pie hole"
            shape.fill.solid()
            shape.fill.fore_color.rgb = rgb(self.c["bg"])
            shape.line.fill.background()
            shape.shadow.inherit = False
        # Legend: colour, name, share — and the value when it is not itself a share.
        lx = cx + d + 0.5
        lw = x + w - lx
        row = min(0.5, (h - 0.2) / max(1, len(cats)))
        size = 15 if row >= 0.45 else 13
        ly = y + (h - row * len(cats)) / 2.0
        shares_only = abs(total - 100.0) < 0.6 or abs(total - 1.0) < 0.006
        fmt = spec.get("number_format") or "0.0"
        decimals = len(fmt.split(".")[1]) if "." in fmt else 0
        unit = str(spec.get("unit") or "")
        for i, c in enumerate(cats):
            self.rect(slide, lx, ly + row * i + row / 2 - 0.09, 0.18, 0.18, colours[i % len(colours)]).name = "chart:pie key"
            text = "%s  **%.1f%%**" % (c, 100.0 * values[i] / total)
            if not shares_only:
                text += "  (%.*f%s)" % (decimals, values[i], (" " + unit) if unit else "")
            if line_count(text, size, lw - 0.35) > 1:
                self.err("the pie label \"%s\" is too long for the legend" % c)
            self.text(slide, lx + 0.32, ly + row * i, lw - 0.35, row, text, size, anchor=MSO_ANCHOR.MIDDLE)

    def s_table(self, s):
        slide = self.new_slide(s.get("notes"))
        self.title(slide, s.get("title"))
        y = self.takeaway(slide, s.get("takeaway"), BODY_Y)
        cols = [str(c) for c in s.get("columns") or []]
        rows = [[str(v) for v in r] for r in s.get("rows") or []]
        if not cols or not rows:
            self.err("the table needs \"columns\" and \"rows\"")
            self.footer(slide, s.get("source"))
            return
        if any(len(r) != len(cols) for r in rows):
            self.err("every table row needs %d cells, one per column" % len(cols))
            self.footer(slide, s.get("source"))
            return
        if not s.get("source"):
            self.err("the table has no \"source\": say where the numbers come from")
        width = W - 2 * MX
        # Column widths follow the longest cell, within reason.
        longest = [max(units(plain(c)) for c in [cols[i]] + [r[i] for r in rows]) for i in range(len(cols))]
        longest = [min(max(v, 4.0), 30.0) for v in longest]
        widths = [width * v / sum(longest) for v in longest]
        avail = BODY_BOTTOM - y
        size = None
        for cand in (14, 13, 12, 11):
            row_h = []
            for r in [cols] + rows:
                lines = max(line_count(r[i], cand, widths[i] - 0.2) for i in range(len(cols)))
                row_h.append(lines * cand * 1.25 / 72.0 + 0.16)
            if sum(row_h) <= avail:
                size = cand
                break
        if size is None:
            self.err("the table does not fit: %d rows of this length need more than the slide has at 11pt. "
                     "Fewer rows, shorter cells, or two slides." % len(rows))
            size = 11
        shape = slide.shapes.add_table(len(rows) + 1, len(cols), Inches(MX), Inches(y), Inches(width),
                                       Inches(min(avail, sum(row_h))))
        table = shape.table
        # No built-in style: its banding and colours are not this deck's.
        tbl_pr = shape._element.graphic.graphicData.tbl.tblPr
        tbl_pr.set("bandRow", "0")
        tbl_pr.set("firstRow", "0")
        for i, wv in enumerate(widths):
            table.columns[i].width = Inches(wv)
        for ri, r in enumerate([cols] + rows):
            table.rows[ri].height = Inches(row_h[ri])
            for ci, value in enumerate(r):
                cell = table.cell(ri, ci)
                cell.fill.solid()
                if ri == 0:
                    cell.fill.fore_color.rgb = rgb(self.c["accent"])
                else:
                    cell.fill.fore_color.rgb = rgb(self.c["panel"] if ri % 2 == 0 else self.c["bg"])
                cell.margin_left = cell.margin_right = Inches(0.1)
                cell.margin_top = cell.margin_bottom = Inches(0.05)
                cell.vertical_anchor = MSO_ANCHOR.MIDDLE
                tf = cell.text_frame
                tf.word_wrap = True
                p = tf.paragraphs[0]
                numeric = ri > 0 and ci > 0 and re.match(r"^[\s+\-~≈<>]*[\d.,]+", value) is not None
                p.alignment = PP_ALIGN.RIGHT if numeric else PP_ALIGN.LEFT
                self.runs(p, value, size, "FFFFFF" if ri == 0 else self.c["ink"], bold=(ri == 0))
        self.footer(slide, s.get("source"))

    def s_stats(self, s):
        slide = self.new_slide(s.get("notes"))
        self.title(slide, s.get("title"))
        y = self.takeaway(slide, s.get("takeaway"), BODY_Y)
        stats = s.get("stats") or []
        if not 2 <= len(stats) <= 4:
            self.err("a stats slide shows 2 to 4 figures, this one has %d" % len(stats))
            stats = stats[:4]
        if not s.get("source"):
            self.err("the figures have no \"source\": say where the numbers come from")
        if stats:
            gap = 0.35
            card_w = (W - 2 * MX - gap * (len(stats) - 1)) / len(stats)
            card_h = min(3.2, BODY_BOTTOM - y - 0.2)
            if not s.get("bullets"):
                y += max(0.0, (BODY_BOTTOM - y - card_h) / 2.0 - 0.35)
            for i, st in enumerate(stats):
                x = MX + i * (card_w + gap)
                self.rect(slide, x, y + 0.1, card_w, card_h, self.c["panel"])
                self.rect(slide, x, y + 0.1, 0.06, card_h, self.c["accent"])
                value = str(st.get("value", ""))
                size = 40
                for size in (40, 36, 32, 28, 24):
                    if line_count(value, size, card_w - 0.6) == 1:
                        break
                else:
                    self.err("the figure \"%s\" is too long for its card; a figure is a number and a unit" % value)
                self.text(slide, x + 0.35, y + 0.45, card_w - 0.6, 0.9, value, size, self.c["accent"], bold=True)
                label = str(st.get("label", ""))
                if line_count(label, 16, card_w - 0.6) > 2:
                    self.err("the label \"%s\" is too long for its card" % label)
                self.text(slide, x + 0.35, y + 1.45, card_w - 0.6, 0.7, label, 16, bold=True)
                if st.get("note"):
                    if line_count(str(st["note"]), 13, card_w - 0.6) > 3:
                        self.err("the note under \"%s\" is too long for its card" % label)
                    self.text(slide, x + 0.35, y + 2.2, card_w - 0.6, 0.9, st["note"], 13, self.c["muted"])
            if s.get("bullets"):
                by = y + 0.1 + card_h + 0.3
                self.bullets(slide, s.get("bullets"), MX, by, W - 2 * MX, BODY_BOTTOM - by, largest=16)
        self.footer(slide, s.get("source"))

    def s_two_column(self, s):
        slide = self.new_slide(s.get("notes"))
        self.title(slide, s.get("title"))
        y = self.takeaway(slide, s.get("takeaway"), BODY_Y)
        gap = 0.6
        col_w = (W - 2 * MX - gap) / 2
        for i, key in enumerate(("left", "right")):
            col = s.get(key) or {}
            x = MX + i * (col_w + gap)
            cy = y
            if not norm_bullets(col.get("bullets")):
                self.err("the %s column is empty" % key)
            if col.get("heading"):
                self.text(slide, x, cy, col_w, 0.45, col["heading"], 18, self.c["accent"], bold=True)
                self.rect(slide, x, cy + 0.48, col_w, 0.015, self.c["line"])
                cy += 0.7
            self.bullets(slide, col.get("bullets"), x, cy, col_w, BODY_BOTTOM - cy, largest=18)
        self.footer(slide, s.get("source"))

    def s_sources(self, s):
        items = [str(i).strip() for i in s.get("items") or [] if str(i).strip()]
        if not items:
            self.n += 1
            self.err("the sources slide lists nothing")
            self.n -= 1
            return
        # As many slides as the list needs: a reference list is never cut.
        per = 9
        for start in range(0, len(items), per):
            slide = self.new_slide(s.get("notes"))
            title = str(s.get("title") or ("数据来源" if has_cjk(json.dumps(self.spec, ensure_ascii=False)) else "Sources"))
            if start:
                title += "（续）" if has_cjk(title) else " (cont.)"
            self.title(slide, title)
            chunk = items[start:start + per]
            tf = self.box(slide, MX, BODY_Y, W - 2 * MX, BODY_BOTTOM - BODY_Y)
            size = 13
            while size > 10 and sum(line_count(t, size, W - 2 * MX - 0.4) * size * 1.3 / 72 + size * 0.5 / 72
                                    for t in chunk) > BODY_BOTTOM - BODY_Y:
                size -= 1
            for k, item in enumerate(chunk):
                p = tf.paragraphs[0] if k == 0 else tf.add_paragraph()
                p.space_after = Pt(size * 0.5)
                self.runs(p, "%d. %s" % (start + k + 1, item), size, self.c["ink"])
            self.footer(slide)

    # --- assembly -----------------------------------------------------------

    def build(self):
        slides = list(self.spec.get("slides") or [])
        if not slides:
            self.errors.append("the spec has no slides")
            return
        if str(slides[0].get("type", "")).lower() != "title":
            slides.insert(0, {"type": "title"})
        handlers = {
            "title": self.s_title, "section": self.s_section, "bullets": self.s_bullets,
            "agenda": self.s_bullets, "summary": self.s_bullets, "chart": self.s_chart,
            "table": self.s_table, "stats": self.s_stats, "two_column": self.s_two_column,
            "sources": self.s_sources,
        }
        has_sources = False
        for s in slides:
            kind = str(s.get("type", "bullets")).lower().replace("-", "_")
            if kind not in handlers:
                self.n += 1
                self.err("unknown slide type \"%s\"; use one of %s" % (kind, ", ".join(sorted(handlers))))
                continue
            if kind == "sources":
                has_sources = True
            handlers[kind](s)
        if not has_sources and self.sources:
            self.s_sources({"items": self.sources})


def main():
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    spec_path, out = sys.argv[1], sys.argv[2]
    try:
        with open(spec_path, encoding="utf-8") as f:
            spec = json.load(f)
    except json.JSONDecodeError as e:
        sys.exit("%s is not valid JSON: %s" % (spec_path, e))
    deck = Deck(spec)
    deck.build()
    if deck.errors:
        print("NOT WRITTEN: %d thing(s) to fix in %s" % (len(deck.errors), spec_path))
        for e in deck.errors:
            print("  " + e)
        return 1
    os.makedirs(os.path.dirname(os.path.abspath(out)) or ".", exist_ok=True)
    deck.prs.core_properties.title = str(spec.get("title", ""))
    deck.prs.core_properties.author = str(spec.get("author", ""))
    deck.prs.save(out)
    print("wrote %s: %d slides" % (out, deck.n))
    print("next: deck_check.py %s" % out)
    return 0


if __name__ == "__main__":
    sys.exit(main())
