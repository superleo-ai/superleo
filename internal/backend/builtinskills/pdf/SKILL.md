---
name: pdf
description: Read, edit and assemble PDF files with self-checking scripts — change words keeping the layout, merge, reorder pages, watermark, number pages, sign or fill a line.
when_to_use: PDF files — read, change, replace text, merge, split, watermark, number pages, sign, fill; contracts, invoices, forms. 读取 修改 编辑 合并 拆分 PDF 合同 发票 表单 改金额 改日期 改期限 加水印 加页码 签名 签字 版式保持一致 其他不动
---

# PDF

You have no PDF tool; you have a shell and these scripts. They are how PDF work is done here. Do not write a one-off PyMuPDF script for a job one of them covers: the one-off is what changed a font and deleted a heading and reported "nothing else changed".

Everything runs through `docpy`, which is the Python with the libraries in it. The first call builds it (about a minute, needs the network); nothing has to be installed beforehand.

```
{{SKILLS_ROOT}}/docpy {{SKILLS_ROOT}}/pdf/scripts/pdf_check.py ...     # read, compare
{{SKILLS_ROOT}}/docpy {{SKILLS_ROOT}}/pdf/scripts/pdf_edit.py ...      # change words
{{SKILLS_ROOT}}/docpy {{SKILLS_ROOT}}/pdf/scripts/pdf_tools.py ...     # merge, pages, watermark, number, write
```

Copy these paths as they are; they are absolute and work from any folder.

If `docpy` cannot run (no `sh`, or it reports it could not fetch `uv`): install uv from https://docs.astral.sh/uv/getting-started/installation/, then `uv venv <dir> --python 3.12` and `VIRTUAL_ENV=<dir> uv pip install pymupdf`, and run the scripts with `<dir>/bin/python`.

Paths the user gives are relative to the workspace. Never write over the file you were given: write a new file, and if no name was asked for, add `-edited` before `.pdf`.

## 1. Read it first

```
{{SKILLS_ROOT}}/docpy {{SKILLS_ROOT}}/pdf/scripts/pdf_check.py FILE.pdf
```

Prints every page: size, fonts, and each line of text as the file holds it. This is also how you answer "what does this PDF say". A page reported as `no text layer` is a scan: its words can be read only by OCR and cannot be replaced by the edit script. Say so instead of guessing.

## 2. Change words — `pdf_edit.py`

```
{{SKILLS_ROOT}}/docpy {{SKILLS_ROOT}}/pdf/scripts/pdf_edit.py IN.pdf OUT.pdf --sub 'USD 500=>USD 800' --sub '6 months=>12 months'
```

- `OLD` is copied from a line `pdf_check.py` printed. It must match once; if the script says it is written several times, give more of the surrounding words (`'runs for 6 months=>runs for 12 months'`) or pass `--all` when every one should change.
- It keeps the baseline, size, colour and font of the old text, moves the rest of the line when the new text is wider, and touches no other line, picture or drawing.
- It then compares OUT with IN and prints the report described in step 4.

It does not re-wrap paragraphs. When the new wording is much longer than the old, the report says the line no longer fits: use shorter wording, or tell the user that this change needs the source document (Word, Pages, LaTeX) and not the PDF.

## 3. Other jobs — `pdf_tools.py`

```
{{SKILLS_ROOT}}/docpy {{SKILLS_ROOT}}/pdf/scripts/pdf_tools.py merge OUT.pdf A.pdf B.pdf
{{SKILLS_ROOT}}/docpy {{SKILLS_ROOT}}/pdf/scripts/pdf_tools.py pages IN.pdf OUT.pdf 1-3,7
{{SKILLS_ROOT}}/docpy {{SKILLS_ROOT}}/pdf/scripts/pdf_tools.py watermark IN.pdf OUT.pdf DRAFT            # light grey, diagonal, under the text
{{SKILLS_ROOT}}/docpy {{SKILLS_ROOT}}/pdf/scripts/pdf_tools.py number IN.pdf OUT.pdf --format '{n} / {total}'
{{SKILLS_ROOT}}/docpy {{SKILLS_ROOT}}/pdf/scripts/pdf_tools.py write IN.pdf OUT.pdf 'Liang Li' --after 'Signed:'
```

Several steps chain through intermediate files in a temp folder (`mktemp -d`), not in the workspace; only the file the user asked for lands there.

For something none of these cover (filling AcroForm fields, extracting tables or images, converting to images, OCR), write a short PyMuPDF script and run it with `docpy`; then check it as in step 4. For a file of another kind, the `file-check` skill checks any file the same way.

## 4. Check before you say it is done

Every script ends with a report and a last line. Read it.

- `RESULT: OK` — what was asked for is there and nothing else differs. Only now may you say the rest is unchanged.
- `RESULT: N problem(s)` — each `PROBLEM:` line is a fault in the file you just wrote: a line that vanished, text in a substitute font, a line that moved, pixels that changed away from the edit. Fix it and run again. What you cannot fix goes into your answer in plain words ("the new amount is set in Courier because the original font is not available"). Never answer "everything else is unchanged" over a report that says otherwise.

For a file you made some other way, run the comparison yourself:

```
{{SKILLS_ROOT}}/docpy {{SKILLS_ROOT}}/pdf/scripts/pdf_check.py ORIGINAL.pdf EDITED.pdf --expect 'OLD=>NEW'
{{SKILLS_ROOT}}/docpy {{SKILLS_ROOT}}/pdf/scripts/pdf_check.py RESULT.pdf            # merged / watermarked / numbered: pages and text as expected?
```

Add `--png DIR` (a temp folder) to any of these to get the pages as images. You cannot see them, the user can: mention the folder only if they ask to look.

## 5. The answer

Say what changed, in the user's words, and what you checked ("compared with the original: only those two lines differ, same font and size"). Give the output path. If a problem remains, it comes first, not last.
