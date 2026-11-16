"""Turn an HTML table of a web page into an XLSX workbook with a Source sheet holding the page URL and the time it was read."""
from __future__ import annotations

import argparse
import datetime
import io
import re
import sys
import urllib.request


def fail(message):
    print("error: " + message, file=sys.stderr)
    sys.exit(1)


def read_source(source):
    if re.match(r"^https?://", source):
        request = urllib.request.Request(source, headers={"User-Agent": "Mozilla/5.0 (compatible; TOFI-bot-computer)"})
        try:
            with urllib.request.urlopen(request, timeout=60) as response:
                raw = response.read()
                charset = response.headers.get_content_charset() or "utf-8"
        except OSError as err:
            fail("cannot fetch %s: %s" % (source, err))
        return raw.decode(charset, errors="replace")
    try:
        with open(source, "rb") as handle:
            return handle.read().decode("utf-8", errors="replace")
    except OSError as err:
        fail("cannot read %s: %s" % (source, err))


def page_tables(html):
    """Return (frames, row_counts): the tables pandas reads and, for the same tables, the data rows with some text counted straight from the markup."""
    import pandas as pd
    from lxml import html as lhtml
    frames = pd.read_html(io.StringIO(html))
    root = lhtml.fromstring(html)
    counts = []
    for table in root.xpath("//table"):
        rows = [tr for tr in table.iter("tr") if tr.xpath("ancestor::table[1]")[0] is table and tr.xpath("./td")
                and any(td.text_content().strip() for td in tr.xpath("./td"))]
        counts.append(len(rows))
    return frames, counts


NOTE = re.compile(r"\s*\[[0-9a-zA-Z ]{1,4}\]")
CURRENCY = re.compile(r"[$€£¥₩]|US\$|CN¥|RMB|USD|EUR|GBP")
DATE_FORMS = [re.compile(r"^\d{4}-\d{1,2}-\d{1,2}$"), re.compile(r"^\d{4}/\d{1,2}/\d{1,2}$"),
              re.compile(r"^\d{1,2} [A-Z][a-z]+ \d{4}$"), re.compile(r"^[A-Z][a-z]+ \d{1,2}, \d{4}$"), re.compile(r"^\d{4}年\d{1,2}月\d{1,2}日$")]


def flat_headers(columns):
    names = []
    for column in columns:
        parts = column if isinstance(column, tuple) else (column,)
        kept = []
        for part in parts:
            text = NOTE.sub("", str(part)).strip()
            if text and not text.startswith("Unnamed") and text not in kept:
                kept.append(text)
        names.append(" / ".join(kept) or "column")
    seen = {}
    unique = []
    for name in names:
        seen[name] = seen.get(name, 0) + 1
        unique.append(name if seen[name] == 1 else "%s (%d)" % (name, seen[name]))
    return unique


def parse_number(text):
    cleaned = CURRENCY.sub("", text).replace(",", "").replace("−", "-").replace(" ", "").replace(" ", "")
    percent = cleaned.endswith("%")
    if percent:
        cleaned = cleaned[:-1]
    if re.fullmatch(r"\(\d+(\.\d+)?\)", cleaned):
        cleaned = "-" + cleaned[1:-1]
    if not re.fullmatch(r"[+-]?\d+(\.\d+)?", cleaned):
        raise ValueError(text)
    value = float(cleaned)
    if percent:
        return value / 100.0, "0.0%"
    return (int(value) if value == int(value) and "." not in cleaned else value), None


def clean_frame(frame):
    """Strip footnote marks, give numbers and dates their types. Returns the frame and a number format per column."""
    import pandas as pd
    frame = frame.copy()
    frame.columns = flat_headers(frame.columns)
    formats = {}
    empty = [name for name in frame.columns if frame[name].isna().all()]
    frame = frame.drop(columns=empty)
    for name in frame.columns:
        series = frame[name]
        if series.dtype != object:
            if str(series.dtype).startswith("float") and series.notna().any() and (series.dropna() % 1 == 0).all():
                frame[name] = series.map(lambda v: None if pd.isna(v) else int(v)).astype(object)
            continue
        texts = series.map(lambda v: None if pd.isna(v) else NOTE.sub("", str(v)).strip())
        texts = texts.map(lambda v: None if v in (None, "", "-", "—", "N/A", "n/a") else v)
        present = texts.dropna()
        converted = texts
        if len(present):
            try:
                pairs = [parse_number(v) for v in present]
                fmts = {f for _, f in pairs}
                if len(fmts) == 1:
                    converted = texts.copy()
                    converted.loc[present.index] = [v for v, _ in pairs]
                    converted = converted.astype(object)
                    formats[name] = fmts.pop()
            except ValueError:
                if all(any(p.match(v) for p in DATE_FORMS) for v in present):
                    parsed = pd.to_datetime(present, errors="coerce")
                    if parsed.notna().all():
                        converted = texts.copy().astype(object)
                        converted.loc[present.index] = parsed.dt.to_pydatetime()
                        formats[name] = "yyyy-mm-dd"
        frame[name] = converted
    return frame, formats, empty


def write_workbook(path, frame, formats, url, read_at, table_index, row_count, caption):
    import openpyxl
    from openpyxl.styles import Font
    from openpyxl.utils import get_column_letter
    book = openpyxl.Workbook()
    sheet = book.active
    sheet.title = "Data"
    sheet.append(list(frame.columns))
    for record in frame.itertuples(index=False):
        sheet.append([None if (v is None or v != v) else v for v in record])
    for cell in sheet[1]:
        cell.font = Font(bold=True)
    sheet.freeze_panes = "A2"
    sheet.auto_filter.ref = sheet.dimensions
    for position, name in enumerate(frame.columns, start=1):
        letter = get_column_letter(position)
        longest = max([len(str(name))] + [len(str(c.value)) for c in sheet[letter][1:] if c.value is not None])
        sheet.column_dimensions[letter].width = min(max(10, longest + 2), 60)
        fmt = formats.get(name)
        if fmt:
            for cell in sheet[letter][1:]:
                cell.number_format = fmt
    source = book.create_sheet("Source")
    for label, value in [("Page URL", url), ("Read at (UTC)", read_at), ("Table on the page", table_index + 1),
                         ("Caption", caption or ""), ("Data rows", row_count), ("Columns", len(frame.columns))]:
        source.append([label, value])
    source.column_dimensions["A"].width = 22
    source.column_dimensions["B"].width = 80
    if re.match(r"^https?://", url):
        source["B1"].hyperlink = url
    for cell in source["A"]:
        cell.font = Font(bold=True)
    try:
        book.save(path)
    except OSError as err:
        fail("cannot write %s: %s" % (path, err))


def main():
    parser = argparse.ArgumentParser(description="Read the HTML tables of a page (URL or saved HTML file), print a list of them, or write one as XLSX "
                                     "with a Data sheet (header row, typed numbers and dates) and a Source sheet (page URL, read time, table number). "
                                     "The row count written is checked against the rows counted in the page markup; a difference exits 1.")
    parser.add_argument("source", help="page URL, or the path of an HTML file saved from it")
    parser.add_argument("--url", help="page URL to record when the source is a file")
    parser.add_argument("--list", action="store_true", help="list the tables found (number, size, first headers) and stop")
    parser.add_argument("--table", type=int, help="table number to write, counted from 1 as in --list; pages often start with layout tables, so look at --list first")
    parser.add_argument("--out", help="XLSX path to write")
    args = parser.parse_args()

    is_url = bool(re.match(r"^https?://", args.source))
    url = args.url or (args.source if is_url else None)
    if not args.list and not url:
        fail("a file source needs --url so the workbook can name the page it came from")
    if not args.list and not args.out:
        fail("--out is required")
    if not args.list and not args.table:
        fail("--table is required; list the tables with --list and pick the one the request means")
    html = read_source(args.source)
    read_at = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%d %H:%M:%S")
    try:
        frames, counts = page_tables(html)
    except ValueError as err:
        fail("no table found in the page: %s" % err)
    if args.list:
        for number, frame in enumerate(frames, start=1):
            print("table %d: %d rows x %d columns; %s" % (number, len(frame), len(frame.columns), " | ".join(flat_headers(frame.columns)[:6])))
        return
    if not 1 <= args.table <= len(frames):
        fail("table %d does not exist; the page has %d table(s)" % (args.table, len(frames)))
    raw = frames[args.table - 1]
    if raw.empty:
        fail("table %d has no rows" % args.table)
    cleaned, formats, empty = clean_frame(raw)
    cleaned = cleaned.dropna(how="all")
    write_workbook(args.out, cleaned, formats, url, read_at, args.table - 1, len(cleaned), "")
    print("wrote %s: %d data rows, %d columns, from table %d of %s, read at %s UTC" % (args.out, len(cleaned), len(cleaned.columns), args.table, url, read_at))
    print("rows dropped as empty: %d; columns dropped as empty: %s" % (len(raw) - len(cleaned), ", ".join(map(str, empty)) or "none"))
    print("typed columns: %s" % (", ".join("%s (%s)" % (k, v) for k, v in formats.items()) or "none"))
    if len(counts) == len(frames):
        expected = counts[args.table - 1]
        print("rows counted in the page markup: %d; rows in the workbook: %d" % (expected, len(cleaned)))
        if expected != len(cleaned):
            print("error: row counts differ; check the page and the workbook before delivering", file=sys.stderr)
            sys.exit(1)
    else:
        print("note: the page markup count could not be matched to the tables pandas read (%d vs %d); compare the row count with the page by hand" % (len(counts), len(frames)))


if __name__ == "__main__":
    main()
