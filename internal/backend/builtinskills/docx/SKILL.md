---
name: docx
description: Write, edit and check Word documents (.docx) with the built-in word_document tool — long documents with real headings, a table of contents, tables, figures and page numbers; edits that keep formatting, as tracked changes when asked.
when_to_use: Word, DOCX, document, report, proposal, plan, contract, tracked changes, redline. Word文档 写文档 项目方案 方案 报告 标书 制度 合同 协议 修订 修订模式 留痕 批注 目录 页眉页脚 页码 改条款 改合同
---

# Word documents

Word files are made and changed with the `word_document` tool. It is part of SuperLeo: nothing to install, the same on macOS, Linux and Windows. Do not write a script that builds a .docx yourself — not in Python, not in anything: there may be no interpreter on the machine, and a hand-made document is where headings turn out to be bold paragraphs and an edit loses its formatting.

Three actions, one tool:

| you want to | action | arguments |
|---|---|---|
| write a new document | `build` | `path`, `spec`, optional `pages` |
| change an existing one | `edit` | `source` (the original), `path` (the new file), `changes`, optional `track`, `author` |
| read one, or verify one | `check` | `path`, optional `source` + `changes` (compare with the original), `pages` |

Paths are relative to the workspace. The original is never written over: an edit goes to a new file, and when no name was asked for, add `-edited` or `-v2` before `.docx`.

Every answer ends with `RESULT: OK` or `RESULT: N problem(s)`. Step 4 says what to do with it.

## 1. A document someone gave you: read it first

`{"action": "check", "path": "contract.docx"}` prints the outline, every table's first rows, the header and footer, the fonts, and any tracked changes already in it. This is also how you answer "what does this document say". Copy the words you will change from what it prints, exactly.

## 2. Write a new document — `build`

Think about the document before the spec. Who reads it and what do they decide from it; what sections does that kind of document always have; what does each section need to say. If it rests on facts you were not given — a market, prices, regulations — look them up first and keep where each came from. A plan with invented figures is worse than a shorter one.

Length: a page of body text is about 550 Chinese characters or 450 English words. "About 10 pages" of a plan is some 8–10 sections with real paragraphs under each, a few tables, and the cover and contents on top. Write to that length; do not pad with repeated sentences, and do not hand over three pages with headings as "about 10". Pass `"pages": 10` and the tool tells you how long it came out.

```json
{
  "action": "build", "path": "docs/plan.docx", "pages": 10,
  "spec": {
    "title": "智慧园区能耗管理平台项目方案",
    "subtitle": "…", "author": "…", "date": "2026-11",
    "content": [
      {"h": 1, "text": "项目背景"},
      {"p": "正文段落。一段说一件事，写完整的句子。**加粗**用两个星号。"},
      {"h": 2, "text": "现状问题"},
      {"bullets": ["要点一", "要点二"]},
      {"numbered": ["第一步", "第二步"]},
      {"table": {"caption": "项目里程碑", "columns": ["阶段", "时间", "交付物"],
                 "rows": [["需求调研", "第 1–2 周", "需求规格说明书"]], "widths": [3, 3, 6]}},
      {"image": {"path": "arch.png", "width_cm": 14, "caption": "系统总体架构"}},
      {"note": "一段说明或提示，缩进显示"},
      {"page_break": true}
    ]
  }
}
```

- Headings are `{"h": 1|2|3, "text": …}`. They become real heading styles and are numbered 1 / 1.1 / 1.1.1 for you — do not type the numbers. Levels do not skip (no 3 under a 1), and every heading has something under it.
- A cover page, a table of contents, a header with the title and "第 X 页 / 共 Y 页" in the footer are there by default. Turn off with `"cover": false`, `"toc": false`, `"footer": "none"`; set `"header": "…"`.
- Fonts: Chinese text is set in 宋体 with 黑体 headings, Latin in Times New Roman, 12 pt (小四), 1.5 line spacing — the usual form of a Chinese business document. Change with `"fonts": {"body": "…", "heading": "…", "latin": "…"}` and `"size"` only when the user asks for a house style.
- Tables are for things with the same attributes — phases, roles, costs, risks. Put numbers in tables, not in sentences. Captions are numbered for you (表 1, 图 1).
- Images are files that exist in the workspace. Do not reference one you have not made or been given.
- Everything is refused, by item number, that would not be a sound document. If the answer starts `NOT WRITTEN`, fix those items and build again.

The table of contents lists every heading and links to it. Its page numbers are filled in by Word when the file is opened (Word asks to update fields — the answer is yes); until then, and in a phone's preview, the entries show without numbers. Say this in your answer when the document has a contents page.

## 3. Change an existing document — `edit`

```json
{
  "action": "edit", "source": "contract.docx", "path": "contract-v2.docx",
  "track": true, "author": "Liang Li",
  "changes": [
    {"replace": "服务费用为 USD 500", "with": "服务费用为 USD 800"},
    {"replace": "有效期为 6 个月", "with": "有效期为 12 个月"},
    {"insert_after": "任何一方可提前 30 日书面通知", "text": "续约须经双方书面确认。"},
    {"delete": "本条款仅适用于试用期"}
  ]
}
```

- `replace` must match exactly once. If the tool answers that the words are written several times, it shows each place: give more of the surrounding words, or add `"all": true` when every one should change. Only the words that differ are replaced, and they keep the formatting of the words they replace.
- `insert_after` adds a paragraph after the one containing those words, in that paragraph's style. `delete` removes the paragraph containing the words.
- **`"track": true`** makes every change a tracked change (修订): the old words stay, struck through, the new ones are marked, and the reader accepts or rejects each one in Word. Use it whenever the user says 修订 / 留痕 / tracked changes / redline / "让我看到改了哪里", and for a contract or any document that someone else will review, unless they ask for a clean copy. `author` is whose changes they are — the user's name if you know it.
- Nothing else is touched: other paragraphs, tables, pictures, styles, headers and footers are carried over as they are.

For a change this does not cover — restructuring sections, changing a table's rows, restyling — say so; do not rebuild the document from scratch and call it an edit.

## 4. Check before you say it is done

`build` and `edit` end with the check of what they wrote; read it. For any other .docx, run `check` yourself — with `source` and the `changes` you made, to compare with the original.

- The outline is the document as Word will see it. Read it against the request: every section asked for is there, in order, with text under it.
- After an edit, the comparison lists each paragraph that differs. With tracked changes it also confirms that **rejecting every change gives back the original, word for word**. Each difference must be one you were asked for.
- `RESULT: N problem(s)`: each `PROBLEM:` line is a fault — a heading with nothing under it, Chinese text with no font named, a paragraph that changed without being asked, far fewer pages than requested. Fix it and run again. What you cannot fix goes first in your answer, in plain words.
- `RESULT: OK` is what lets you say the document is done, or that nothing else changed.

The check reads the file's structure. It does not see the pages: where lines break, where a table splits, how a font is drawn. The page count is laid out by LibreOffice when it is installed and is otherwise an estimate from the amount of text — the report says which. Do not claim more than it shows.

## 5. The answer

Give the path, what the document contains (or what changed, in the user's words), and what the check showed. For a tracked edit, say that the changes are tracked and how many there are. Mention what the user must do once: let Word update the contents. Anything unverified or left out — say it.
