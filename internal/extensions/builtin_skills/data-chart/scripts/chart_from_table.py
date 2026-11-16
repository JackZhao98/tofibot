"""Draw one chart from a table file (CSV, TSV, XLSX or JSON) with matplotlib and print the numbers behind it."""
from __future__ import annotations

import argparse
import sys


def load_table(path, sheet):
    import pandas as pd
    lower = path.lower()
    if lower.endswith((".xlsx", ".xlsm")):
        return pd.read_excel(path, sheet_name=sheet if sheet is not None else 0)
    if lower.endswith(".json"):
        return pd.read_json(path)
    if lower.endswith((".tsv", ".tab")):
        return pd.read_csv(path, sep="\t")
    return pd.read_csv(path)


def cjk_font():
    from matplotlib import font_manager
    wanted = ("Noto Sans CJK SC", "Noto Sans CJK JP", "Noto Serif CJK SC", "WenQuanYi Zen Hei", "WenQuanYi Micro Hei",
              "Source Han Sans SC", "PingFang SC", "Hiragino Sans GB", "Heiti SC", "Microsoft YaHei", "SimHei")
    installed = {f.name for f in font_manager.fontManager.ttflist}
    for name in wanted:
        if name in installed:
            return name
    for name in sorted(installed):
        if "CJK" in name:
            return name
    return None


def has_wide_text(*texts):
    return any(ord(ch) > 0x2E7F for text in texts for ch in str(text or ""))


def fail(message):
    print("error: " + message, file=sys.stderr)
    sys.exit(1)


def main():
    parser = argparse.ArgumentParser(description="Draw a line, bar, horizontal bar, scatter or histogram chart from a table and print the plotted numbers. "
                                     "Refuses an empty table, a missing column and a series that is empty or not numeric.")
    parser.add_argument("table", help="CSV, TSV, XLSX or JSON file; never modified")
    parser.add_argument("--out", required=True, help="PNG path to write")
    parser.add_argument("--kind", choices=["line", "bar", "barh", "scatter", "hist"], default="line")
    parser.add_argument("--x", help="column on the x axis (not used by hist)")
    parser.add_argument("--y", action="append", default=[], help="numeric column to plot; repeat for several series")
    parser.add_argument("--group", help="column whose values become one series each (long table); needs one --y")
    parser.add_argument("--sheet", help="sheet name of an XLSX file (first sheet by default)")
    parser.add_argument("--title", required=True, help="chart title; state the finding")
    parser.add_argument("--xlabel", help="x axis label with its unit")
    parser.add_argument("--ylabel", help="y axis label with its unit")
    parser.add_argument("--table-out", help="optional CSV path for the plotted numbers")
    parser.add_argument("--dpi", type=int, default=150)
    args = parser.parse_args()

    import pandas as pd
    try:
        frame = load_table(args.table, args.sheet)
    except (OSError, ValueError) as err:
        fail("cannot read %s: %s" % (args.table, err))
    if frame.empty:
        fail("the table has no rows")
    for name in [args.x, args.group] + args.y:
        if name is not None and name not in frame.columns:
            fail("column %r not in the table; columns are: %s" % (name, ", ".join(map(str, frame.columns))))
    if not args.y:
        fail("give at least one --y column")
    if args.kind != "hist" and not args.x:
        fail("--x is required for this kind")
    if args.group and len(args.y) != 1:
        fail("--group needs exactly one --y")

    for name in args.y:
        converted = pd.to_numeric(frame[name], errors="coerce")
        bad = int(converted.isna().sum() - frame[name].isna().sum())
        if converted.notna().sum() == 0:
            fail("column %r has no numeric values" % name)
        if bad:
            fail("column %r has %d value(s) that are not numbers; clean them first" % (name, bad))
        frame[name] = converted

    if args.group:
        plotted = frame.pivot_table(index=args.x, columns=args.group, values=args.y[0], aggfunc="sum", sort=False)
    elif args.kind == "hist":
        plotted = frame[args.y]
    else:
        plotted = frame.set_index(args.x)[args.y]
    if plotted.empty or plotted.notna().sum().sum() == 0:
        fail("nothing to plot after reshaping")

    import matplotlib
    matplotlib.use("Agg")
    import matplotlib.pyplot as plt
    label_text = [args.title, args.xlabel, args.ylabel] + [str(v) for v in plotted.index[:200]] + [str(c) for c in plotted.columns]
    if has_wide_text(*label_text):
        font = cjk_font()
        if font is None:
            fail("labels contain Chinese, Japanese or Korean text and no CJK font is installed (fc-list :lang=zh)")
        plt.rcParams["font.sans-serif"] = [font, "DejaVu Sans"]
    plt.rcParams["axes.unicode_minus"] = False

    fig, ax = plt.subplots(figsize=(9, 5.2))
    if args.kind == "hist":
        for column in plotted.columns:
            ax.hist(plotted[column].dropna(), bins="auto", alpha=0.75, label=str(column))
    elif args.kind == "scatter":
        for column in plotted.columns:
            ax.scatter(plotted.index, plotted[column], label=str(column))
    elif args.kind == "line":
        plotted.plot(ax=ax, marker="o", linewidth=2)
    elif args.kind == "bar":
        plotted.plot.bar(ax=ax, rot=0 if len(plotted) <= 8 else 45)
    else:
        plotted.plot.barh(ax=ax)
    ax.set_title(args.title, fontsize=14, loc="left")
    ax.set_xlabel(args.xlabel or (args.x if args.kind != "hist" else ""))
    ax.set_ylabel(args.ylabel or (", ".join(args.y) if len(args.y) == 1 else ""))
    ax.grid(axis="y" if args.kind != "barh" else "x", alpha=0.3)
    for side in ("top", "right"):
        ax.spines[side].set_visible(False)
    if len(plotted.columns) > 1:
        ax.legend(frameon=False)
    elif ax.get_legend():
        ax.get_legend().remove()
    fig.tight_layout()
    try:
        fig.savefig(args.out, dpi=args.dpi)
    except OSError as err:
        fail("cannot write %s: %s" % (args.out, err))
    print("wrote %s" % args.out)
    if args.table_out:
        plotted.to_csv(args.table_out)
        print("wrote %s" % args.table_out)
    print("plotted numbers (%d rows):" % len(plotted))
    print(plotted.to_string())


if __name__ == "__main__":
    main()
