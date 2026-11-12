---
name: file-check
description: Check any produced or changed file before reporting done — open, read back, compare with the original, list every difference.
when_to_use: Wrote, generated, exported, converted or edited files (document, spreadsheet, Excel, Word, slides, PDF, image, data, config, script); verify, compare, diff files. 检查 核对 验证 校验 对比 比较 生成的文件 交付物 产出 导出 转换 保存 存成 是否正确 有没有改错
---

# Check what you made

A step that wrote a file and exited 0 has told you the file exists. It has not told you the file is right. Before any file is reported as done, it is opened and read back; a file made by changing another is compared with that other. This holds for every kind of file. The script below does it for any file; formats with a deeper checker of their own are handed to it.

```
{{SKILLS_ROOT}}/docpy {{SKILLS_ROOT}}/file-check/scripts/file_check.py FILE
{{SKILLS_ROOT}}/docpy {{SKILLS_ROOT}}/file-check/scripts/file_check.py FILE --against ORIGINAL
```

`docpy` is the Python these skills run on; its first call builds it (about a minute, needs the network), nothing has to be installed beforehand. Copy the paths as they are. If `docpy` cannot run, install uv (https://docs.astral.sh/uv/getting-started/installation/), `uv venv <dir> --python 3.12`, `VIRTUAL_ENV=<dir> uv pip install pymupdf python-pptx`, and use `<dir>/bin/python`.

## What it does

- Works out what the file is from its bytes, not its name.
- Opens it as that: PDF pages, slides, a document's paragraphs, a workbook's rows and formulas, an image's pixels, JSON / XML / CSV by parsing, a script by a syntax check, anything else as text.
- Prints what is in it. Read the printout against the request, point by point: this is the part no script can do for you.
- Reports what is wrong at the level of the file: does not open, does not parse, empty, blank page, text outside its box, a row with the wrong number of cells, an error value in a cell.
- With `--against`, lists every difference from the original. Each listed difference must be one that was asked for.

## The three checks, whatever the file

1. **Does it open and hold something?** `RESULT: OK` from the script, or the program the file is for opens it without complaint.
2. **Does it say what was asked?** Read the printed content. Count what was to be counted (pages, slides, rows, sections). Look for each thing the user named.
3. **Did only what was asked change?** For an edit, `--against ORIGINAL`. Unrequested differences are faults, not details.

A file that is meant to run has a fourth: run it, and read the output. A syntax check is not a run.

## Reading the result

- `RESULT: OK` — sound as a file. Checks 2 and 3 are still yours.
- `RESULT: N problem(s)` — each `PROBLEM:` line is a fault in the file. Fix it, run the check again. What cannot be fixed goes into the answer, first and in plain words.

In the answer, say what you checked and what the check showed. Never write "everything else is unchanged" or "the file is complete" without a comparison or a read-back that says so.

## Formats with their own skill

When one of these fits, use it; it makes the file as well as checking it, and its checker is stricter than this one.

- **pdf** — read, change words keeping the layout, merge, watermark, number, sign.
- **ppt** — build a slide deck from an outline, with charts and sources.

A format gets its own skill when making it well takes more than a few lines of script. Each follows the same shape: read first, make or change with the skill's script, check with its checker, report what the check showed.
