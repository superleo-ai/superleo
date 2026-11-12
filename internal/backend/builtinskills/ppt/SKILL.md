---
name: ppt
description: Build a PowerPoint deck (.pptx) from an outline — research reports, briefings, proposals — with charts, tables, sourced figures and consistent layout, then check every slide.
when_to_use: PPT, PPTX, slide deck, slides, presentation, report delivered as slides. 做PPT 写PPT 做幻灯片 做一个幻灯片 演示文稿 汇报材料 行业研究报告 研究报告 产品介绍 路演 方案 页左右 图表 数据来源
---

# Slide decks

You have no presentation tool; you have a shell, a web search and these scripts. That is enough, and it is how decks are made here. You write the content as a JSON outline; `deck_build.py` lays it out; `deck_check.py` reads the result back and tells you what is wrong.

```
{{SKILLS_ROOT}}/docpy {{SKILLS_ROOT}}/ppt/scripts/deck_build.py SPEC.json OUT.pptx
{{SKILLS_ROOT}}/docpy {{SKILLS_ROOT}}/ppt/scripts/deck_check.py OUT.pptx --slides N
```

`docpy` is the Python these skills run on; its first call builds it (about a minute, needs the network), nothing has to be installed beforehand. Copy the paths as they are. If `docpy` cannot run, install uv (https://docs.astral.sh/uv/getting-started/installation/), `uv venv <dir> --python 3.12`, `VIRTUAL_ENV=<dir> uv pip install python-pptx pymupdf`, and use `<dir>/bin/python`.

Keep SPEC.json and any notes in a temp folder (`mktemp -d`); only the .pptx goes where the user asked.

## 1. Get the facts before the slides

A deck is its content. If the user gave the material, use it. If the deck needs facts you were not given — a market, a company, a technology, anything with numbers — look them up first, and do not write a slide until you have them.

- Search for each figure the deck will rest on: size, growth, shares, prices, dates, names. Open the pages and read the numbers there; a search snippet is not a source.
- For every figure keep four things: the number, its unit, the period it is for, and where it is from (organisation, document, year, URL).
- Prefer primary sources: regulators, statistics offices, industry associations, company filings. Then established research firms and the trade press quoting them.
- Two sources that disagree: use the more authoritative, or show the range and name both. Never average them and never pick the rounder one.
- A number you could not find is not in the deck. Do not estimate it from memory and do not write "about". If the story needs it, say on the slide that no public figure was found, or leave the point out.
- Forecasts are labelled as forecasts, with whose forecast it is.
- A chart shows numbers that have a source. Scores, ratings or weights you made up to have something to draw are not data: say it in words or in a table, not as a chart.

Ten to twenty sourced figures carry a fifteen-slide report. If the search comes back thin, tell the user what you could and could not source rather than padding.

## 2. Write the outline

One message per slide, and the title says it: "2025 年新增装机 X GW，同比翻倍", not "市场规模". If you cannot put the slide's point in its title, it is not a slide yet.

A report of about N slides usually runs: cover · summary of the three or four conclusions · sections, each opening with a section slide · closing outlook or recommendations · sources. Stay within two of the number asked for. The sources slide is added for you from the slides' `source` fields; write your own `sources` slide when you want full references with URLs.

Pick the slide type by what the content is:

| the content is | type |
|---|---|
| numbers over time, or across a few items | `chart` (`column`, `bar`, `line`, `stacked`, `area`) |
| parts of one whole, at most 6 | `chart` with `pie` or `doughnut` |
| 2–4 headline figures | `stats` |
| items compared on several attributes | `table` |
| two sides: before/after, opportunity/risk | `two_column` |
| an argument in 3–5 points | `bullets` |

At least a third of the content slides of a research report are charts, tables or stats. A deck of bullet slides only is a document cut into pages.

Limits the builder enforces: a title is one line; bullets are 3–5 per slide, each one or two lines; a table is at most about 8 rows and 6 columns; a pie has at most 8 slices; every chart, table and stats slide has a `source`. Run `deck_build.py` with no arguments for the full SPEC format; the short form:

```json
{
  "title": "…", "subtitle": "…", "author": "…", "date": "2026-11",
  "slides": [
    {"type": "title"},
    {"type": "bullets", "title": "…", "takeaway": "…", "bullets": ["…", {"text": "…", "sub": ["…"]}]},
    {"type": "section", "title": "…", "subtitle": "…"},
    {"type": "chart", "title": "…", "takeaway": "…",
     "chart": {"kind": "column", "categories": ["2023", "2024"], "unit": "GW",
               "series": [{"name": "新增装机", "values": [21.5, 42.4]}]},
     "bullets": ["how to read it"], "source": "机构《报告》, 年份"},
    {"type": "stats", "title": "…", "stats": [{"value": "73.8 GW", "label": "…", "note": "…"}], "source": "…"},
    {"type": "table", "title": "…", "columns": ["…"], "rows": [["…"]], "source": "…"},
    {"type": "two_column", "title": "…", "left": {"heading": "…", "bullets": ["…"]}, "right": {"heading": "…", "bullets": ["…"]}},
    {"type": "sources", "title": "数据来源", "items": ["机构, 《文件》, 年份, URL"]}
  ]
}
```

Chart values are numbers (`42.4`, not `"42.4 GW"`); the unit goes in `unit` or the series name. `**bold**` works in any text. The deck is light by default; `"theme": "dark"` only when asked.

## 3. Build

Write SPEC.json with the file-writing tool, then run `deck_build.py`. If it prints `NOT WRITTEN`, it lists what to fix by slide number — too much text, a chart with mismatched values, data without a source. Fix the outline (cut words, split the slide, add the source) and run it again. Do not work around it by shrinking the content into one long bullet.

## 4. Check before you say it is done

Run `deck_check.py OUT.pptx --slides N`. It prints the deck as an outline — every title, every line, every chart's numbers — and then its findings.

- Read the outline against the request: the number of slides, every section the user named, and each chart's numbers against your notes from step 1. A wrong digit is found here or not at all.
- `RESULT: N problem(s)`: each `PROBLEM:` line is a fault — an empty slide, text past its box, a chart without data, data without a source. Fix the outline, rebuild, check again.
- `RESULT: OK` is what lets you report the deck as done.

Add `--png DIR` (a temp folder) to get the slides as images for a person to open.

## 5. The answer

Give the path, the number of slides, and in one or two lines what the deck argues. Then say what you checked, and anything the user should know: figures you could not source, forecasts, a section left thinner than asked. List the main sources. Do not describe the deck as verified beyond what the check showed.

## Changing an existing .pptx

To change wording or numbers in a deck someone else made, do not rebuild it: open it with python-pptx (`from pptx import Presentation`), change the runs in place so their formatting stays, save under a new name, then run `deck_check.py` on it and `{{SKILLS_ROOT}}/docpy {{SKILLS_ROOT}}/file-check/scripts/file_check.py NEW.pptx --against OLD.pptx` to see that only what was asked for differs.
