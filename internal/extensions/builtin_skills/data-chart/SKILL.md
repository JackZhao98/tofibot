---
name: data-chart
description: Analyze a table (CSV, XLSX, JSON, pasted) with pandas and draw a chart; check numbers, view the PNG.
requires: [computer]
kind: deliverable
---

# Data analysis and charts

## What good looks like

- The table is checked before anything is computed: column types, missing values, duplicate rows, and totals that should match each other or a stated figure. Problems found are named in the reply with what was done about them.
- The answer to the question asked is computed from the data and stated as numbers with units, base period and the method (growth as a percentage change between named periods, share of a total, average per group).
- The chart fits the data: lines for change over time, bars for a comparison between groups, horizontal bars for long labels, a histogram for a distribution, a scatter plot for a relation between two measures. Pies are avoided; a part-to-whole comparison with more than three parts is a sorted bar chart.
- The title states the finding. Each axis has a label with its unit, the legend names every series, and nothing overlaps or is cut off. Labels in Chinese, Japanese or Korean render as text and not as empty boxes.
- The numbers behind the chart go with it, so the person can check the picture against the data.

## Loading and checking

- Read the file with pandas: `read_csv`, `read_excel` (name the sheet, and list the sheets when the file has several) or `read_json`. A table pasted in the message is parsed from the message text with `io.StringIO`; the pasted values are not retyped by hand.
- Print `df.dtypes`, `df.describe(include='all')`, `df.isna().sum()` and the row count. Dates are converted with `pd.to_datetime` and numbers stored as text with `pd.to_numeric` after the thousands separators, currency signs and percent signs are removed. A value that does not convert is looked at and reported, not dropped silently.
- Compare computed totals with any total row or stated figure in the source, and say whether they agree.
- Keep the original file as it is. Work on a copy of the data in memory and write results to new files.

## Computing

- Use groupby, pivot_table and pct_change for the question asked. Report growth with both the start and end values, so a large percentage from a small base is visible as such.
- Ask which measure the question means when two readings give different answers; when the request leaves no room to ask, compute both and say which one the chart shows.

## Drawing

- `scripts/chart_from_table.py` draws line, bar, barh, scatter and histogram charts from a table file with a title, axis labels and series chosen on the command line, prints the plotted numbers, and refuses an empty table, a missing column and a series that is empty or not numeric. `--help` describes it. Its output is a start; write matplotlib code directly when the chart needs annotations, two axes or subplots.
- For Chinese, Japanese or Korean labels find a font with `fc-list :lang=zh family` (Noto Sans CJK is installed) and set `plt.rcParams['font.sans-serif']` to it and `plt.rcParams['axes.unicode_minus'] = False`.
- Use the `Agg` backend, `figsize` near 9 by 5.2 inches, `dpi` 150, `tight_layout()`, a colour-blind safe palette, and direct value labels on bars when there are few of them.

## Verifying

- View the saved PNG: the computer_files tool's files.view action returns a PNG or JPEG as an image, while a path or a listing shows nothing. Check that every label is readable and inside the picture, that the unit is on the axis, that the title matches the data, and that no glyph is an empty box. Fix what you see, draw again and view again.
- Read the plotted numbers back from the data and compare them with the picture: the tallest bar, the last point and the ranking the title claims.
- Every claim in the reply is true of the delivered file. The figures in the reply are the figures behind the chart, and omissions, dropped rows and assumptions are stated.
- Publish only the final chart, and the table behind it when the person can use it as a file; scratch files stay on the Bot computer.

## Delivery

- Send the PNG as a send_message attachment (path), with the answer in the message text: the finding in one sentence, then the small table of numbers behind it, then the checks made and anything that could not be verified.
- When the person wants the numbers to work with, send a CSV or XLSX of the table behind the chart as well; follow the spreadsheet Skill for a workbook.

## When something cannot be done

- A table with no numeric column, an empty file, or a question the data cannot answer is reported plainly with what the data does contain. A guess is not charted.
