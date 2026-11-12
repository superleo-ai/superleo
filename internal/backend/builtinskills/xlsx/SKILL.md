---
name: xlsx
description: Build, clean and check Excel workbooks (.xlsx) with the built-in excel_workbook tool — real formulas, number formats, conditional formats, charts, frozen headers; messy csv/xlsx exports cleaned into typed data with a formula summary; every formula recalculated and checked without Excel.
when_to_use: Excel, XLSX, CSV, spreadsheet, workbook, budget, formulas, pivot, summary, clean data, deduplicate. Excel表格 电子表格 工作簿 做表 预算表 报表 台账 公式 合计 汇总 透视表 数据清洗 去重 日期格式 条件格式 图表 销售数据 导出数据
---

# Excel workbooks

Workbooks are made, cleaned and checked with the `excel_workbook` tool. It is part of SuperLeo: nothing to install, the same on macOS, Linux and Windows. Do not write a script that builds an .xlsx yourself — there may be no interpreter on the machine, and a hand-made workbook is where totals turn out to be typed-in numbers and nobody recalculated anything.

| you want to | action | arguments |
|---|---|---|
| make a workbook | `build` | `path`, `spec` |
| clean / summarise an export | `clean` | `source` (csv or xlsx), `path`, optional `key`, `group`, `sum`, `by_month` |
| read one, or verify one | `check` | `path`, optional `source` (the data it came from) |

Paths are relative to the workspace. Every answer ends with `RESULT: OK` or `RESULT: N problem(s)`.

## The one rule

**A number that comes from other cells is a formula.** A total is `=SUM(B2:B13)`, a balance is `=B2-C2`, a rate is `=D2/B2`, a figure on a summary sheet is `='预算'!B14`. Never work the number out yourself and type it in: the workbook is something the user will change, and a typed-in total is wrong the moment they do. The checker reports a number sitting where a formula belongs.

## 1. A file someone gave you: read it first

`{"action": "check", "path": "sales.xlsx"}` (or on a workbook you made from a csv) prints each sheet's size, header, first rows with formulas shown as `=formula → value`, its charts and what they are bound to. Column names for `clean` come from what you see here; for a csv, read its first lines with the file tool.

## 2. Make a workbook — `build`

Decide the sheets first: one table per sheet, one row per thing, a header row, no blank rows inside the data, no merged cells in it. Inputs in plain cells, everything derived in formulas, a summary sheet that reads from the data sheets.

A workbook is handed over with numbers in it. When the user gave the figures, use them. When they asked for a template or a budget and gave none, fill the input cells with realistic example figures for the case — a household, a small team, a project, whichever they mean — so the formulas, the highlighting and the chart show something, say in a `notes` line and in your answer that they are examples to overwrite, and include at least one case the highlighting is there for (a month that overspends). Never hand over a sheet of zeros: the checker reports a chart that draws nothing.

```json
{
  "action": "build", "path": "finance/budget-2026.xlsx",
  "spec": {"sheets": [
    {
      "name": "月度预算",
      "columns": [
        {"header": "月份", "width": 10},
        {"header": "收入", "format": "money"},
        {"header": "支出", "format": "money"},
        {"header": "结余", "format": "money"},
        {"header": "结余率", "format": "percent"}
      ],
      "rows": [
        ["1月", 52000, 41000, "=B2-C2", "=D2/B2"],
        ["2月", 48000, 52000, "=B3-C3", "=D3/B3"]
      ],
      "total": {"label": "合计", "sum": ["B", "C", "D"]},
      "conditional": [
        {"range": "D2:D13", "when": "<0", "fill": "red"},
        {"range": "B2:B13", "type": "data_bar"}
      ],
      "charts": [
        {"type": "column", "title": "月度收支", "categories": "A2:A13",
         "series": [{"name": "B1", "values": "B2:B13"}, {"name": "C1", "values": "C2:C13"}],
         "y_title": "元"}
      ],
      "notes": ["数据为预算口径，实际发生额另表登记。"]
    },
    {
      "name": "汇总",
      "columns": ["指标", {"header": "数值", "format": "money"}],
      "rows": [["全年收入", "='月度预算'!B14"], ["全年结余", "='月度预算'!D14"], ["最高月支出", "=MAX('月度预算'!C2:C13)"]]
    }
  ]}
}
```

- A cell is a value, or a string starting with `=` — a formula, written as in Excel with English function names and commas. Row 1 is the header, so data starts at row 2; with 12 rows of data the `total` row is row 14.
- `format`: `money`, `number`, `int`, `percent`, `date` (give dates as `"2026-03-05"`), `text`, or any Excel number format (`"0.00%"`, `"#,##0\" 元\""`). Percentages are fractions: 0.085 shows as 8.5%.
- `total` adds a totals row of formulas: `sum`, `average`, `max`, `min`, `count`, each a list of column letters.
- `conditional`: `when` is a comparison (`"<0"`, `">=10000"`) or a formula (`"=$E2>0.2"`), with `fill` red / green / yellow / blue; or `"type": "data_bar"` / `"color_scale"`.
- `charts`: `column`, `bar`, `line`, `pie`, `area`. `categories` and each series' `values` are ranges on the same sheet; a series `name` is the header cell (`"B1"`) or text. A chart is placed to the right of the table unless `anchor` says where.
- The header is frozen and filterable by default (`"freeze": "A2"`, `"filter": true`).
- A spec that would make a broken workbook is refused, by sheet and cell: a row with the wrong number of cells, a formula pointing at a sheet that does not exist or evaluating to an error, a chart or rule over nothing. If the answer starts `NOT WRITTEN`, fix those and build again.

Every formula is worked out when the file is written and its value stored beside it, so the numbers also show in a preview that does not calculate (Quick Look, a phone).

## 3. Clean and summarise an export — `clean`

```json
{
  "action": "clean", "source": "exports/sales-raw.csv", "path": "exports/sales-clean.xlsx",
  "key": ["订单号"], "group": ["地区", "产品"], "sum": ["金额", "数量"], "by_month": "日期"
}
```

What it does, and writes down row by row in a log sheet:

- trims spaces, drops blank rows and columns;
- reads each column as what most of it is: `2026/3/5`, `5 Mar 2026` and `2026年3月5日` become the same real date, `¥1,200.50` a number; a cell it cannot read that way is left as it was, marked yellow, and listed;
- makes spellings that differ only by case or spacing one spelling;
- removes duplicate rows — by `key` when one column identifies a row (an order number, an id), else only rows identical in every column.

The workbook has the cleaned data, a summary made of formulas over it (`SUMIFS` / `COUNTIFS`, one block per `group` column and one by month when `by_month` is given, with a chart), and the log. Choose `group`, `sum` and `by_month` from what the user wants to see; if they did not say, group by the categorical columns a reader would ask about (region, product, person) and sum the money and quantity columns.

It does not guess at meaning: a date like `03/04/2026` that could be either order is reported, and you pass `"day_first": true` if it is day first. It does not fill in missing values or fix wrong ones — those it lists.

## 4. Check before you say it is done

`build` and `clean` end with the check of what they wrote; read it. For any other workbook run `check`.

- The check recalculates every formula itself, without Excel, and reports `#REF!`, `#DIV/0!`, `#NAME?`, `#VALUE!`, `#N/A` with the cell; a stored value that no longer matches its formula; a number where its neighbours are formulas; a total typed in; a formula or chart pointing at a sheet or range that is not there or is empty; numbers stored as text.
- Formulas using a function its evaluator does not know are listed as **not evaluated here**. That is a gap in the check, not a fault: say in your answer that those cells were not verified.
- After `clean`, the comparison with the source gives rows in and out and the sum of each numeric column before and after. The difference must be what the log says was removed.
- `RESULT: N problem(s)`: each `PROBLEM:` line is a fault in the workbook, or something the user has to decide (an unreadable date). Fix what is yours and run again; put the rest first in your answer.
- `RESULT: OK` is what lets you say the workbook is right.

The check does not see the workbook as drawn: column widths, colours, the chart's appearance. Do not claim it does.

## 5. The answer

Give the path, the sheets and what each holds, and the headline numbers read from the check (not from memory). Say what was checked — "all 41 formulas recalculated, no errors" — and what was not. After a clean-up, give rows in, rows out, and what was removed or could not be read.
