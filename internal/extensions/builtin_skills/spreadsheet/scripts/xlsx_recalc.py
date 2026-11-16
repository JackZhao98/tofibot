#!/usr/bin/env python3
"""Calculate the formulas of an XLSX workbook and fail when any cell holds an error.

  xlsx_recalc.py in.xlsx out.xlsx   LibreOffice recalculates a copy of in.xlsx and
                                    saves it as out.xlsx. Formulas stay formulas and
                                    every formula cell gets its calculated value, so
                                    viewers that do not calculate show numbers.
  xlsx_recalc.py --check book.xlsx  Only reads book.xlsx as it is: every formula cell
                                    must already hold a calculated value, and none may
                                    hold an error.

The input is never edited. Exit status: 0 clean; 1 an error cell (#REF!, #DIV/0!,
#VALUE!, #NAME?, #N/A, #NULL!, #NUM!, Err:nnn), a formula that lost its text, or a
formula with no calculated value; 2 the work could not be done (unreadable file, no
LibreOffice, timeout). A clean exit means the workbook evaluates; it does not prove
the formulas point at the right cells.

Adapted in part from Anthropic's xlsx Skill (Apache-2.0), see NOTICE.
"""
from __future__ import annotations

import argparse
import os
import re
import shutil
import subprocess
import sys
import tempfile

ERROR_TOKENS = ("#REF!", "#DIV/0!", "#VALUE!", "#NAME?", "#N/A", "#NULL!", "#NUM!", "Err:")
# Functions newer than Excel 2007 that a file must store with the _xlfn. prefix.
PREFIXED = ("TEXTJOIN", "CONCAT", "IFS", "SWITCH", "MAXIFS", "MINIFS")
# Functions the Bot computer's LibreOffice cannot be relied on to evaluate.
AVOID = ("XLOOKUP", "XMATCH", "SORT", "FILTER", "UNIQUE", "SEQUENCE", "LET")


def is_error(value):
    return isinstance(value, str) and value.startswith(ERROR_TOKENS)


def function_warnings(formula):
    warnings = []
    for name in PREFIXED:
        if re.search(r"(?<![A-Za-z0-9_.])" + name + r"\(", formula):
            warnings.append("%s needs the _xlfn. prefix" % name)
    for name in AVOID:
        if re.search(r"(?<![A-Za-z0-9_.])(?:_xlfn\.)?(?:_xlws\.)?" + name + r"\(", formula):
            warnings.append("%s may not be calculable here; use INDEX/MATCH or prepare the data in Python" % name)
    return warnings


def formulas_of(path):
    import openpyxl
    book = openpyxl.load_workbook(path, data_only=False)
    return {(ws.title, c.coordinate): c.value for ws in book.worksheets for row in ws.iter_rows()
            for c in row if isinstance(c.value, str) and c.value.startswith("=")}


def inspect(path, expected=None):
    """Return (formula_count, problems, warnings) for a workbook file as saved."""
    import openpyxl
    formulas = formulas_of(path)
    values = openpyxl.load_workbook(path, data_only=True)
    problems, warnings = [], []
    for (sheet, coord), text in sorted(formulas.items()):
        shown = values[sheet][coord].value
        if is_error(shown):
            problems.append("%s!%s %s = %s" % (sheet, coord, text, shown))
        elif shown is None:
            problems.append("%s!%s %s has no calculated value" % (sheet, coord, text))
        for warn in function_warnings(text):
            warnings.append("%s!%s %s" % (sheet, coord, warn))
    for ws in values.worksheets:
        for row in ws.iter_rows():
            for c in row:
                if is_error(c.value) and (ws.title, c.coordinate) not in formulas:
                    problems.append("%s!%s holds the error value %s" % (ws.title, c.coordinate, c.value))
    if expected is not None:
        for key, text in sorted(expected.items()):
            if key not in formulas:
                problems.append("%s!%s lost its formula %s" % (key[0], key[1], text))
    return len(formulas), problems, warnings


def recalculate(source, target, timeout):
    soffice = shutil.which("soffice") or shutil.which("libreoffice")
    if not soffice:
        raise RuntimeError("soffice is not installed, so nothing was recalculated")
    with tempfile.TemporaryDirectory(prefix="xlsx-recalc-") as work:
        copy_in = os.path.join(work, "in", os.path.basename(source))
        os.makedirs(os.path.dirname(copy_in))
        shutil.copy(source, copy_in)
        out_dir = os.path.join(work, "out")
        os.makedirs(out_dir)
        profile = "file://" + os.path.join(work, "profile")
        done = subprocess.run([soffice, "-env:UserInstallation=" + profile, "--headless", "--calc",
                               "--convert-to", "xlsx:Calc MS Excel 2007 XML", "--outdir", out_dir, copy_in],
                              capture_output=True, text=True, timeout=timeout)
        produced = os.path.join(out_dir, os.path.basename(source))
        if done.returncode != 0 or not os.path.exists(produced):
            raise RuntimeError("LibreOffice produced no file: %s" % (done.stderr or done.stdout).strip())
        os.makedirs(os.path.dirname(os.path.abspath(target)), exist_ok=True)
        shutil.copy(produced, target)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--check", action="store_true", help="read the workbook as saved; no LibreOffice, no output file")
    parser.add_argument("--timeout", type=int, default=120)
    parser.add_argument("paths", nargs="+", help="in.xlsx out.xlsx, or with --check: book.xlsx")
    args = parser.parse_args(argv)
    try:
        if args.check:
            if len(args.paths) != 1:
                parser.error("--check takes one file")
            source, expected = args.paths[0], None
        else:
            if len(args.paths) != 2:
                parser.error("give the input file and the output file")
            source, target = args.paths
            if os.path.abspath(source) == os.path.abspath(target):
                print("refusing to overwrite the input; give a different output path", file=sys.stderr)
                return 2
            expected = formulas_of(source)
            recalculate(source, target, args.timeout)
            print("wrote %s" % target)
        count, problems, warnings = inspect(args.paths[-1], expected)
    except (OSError, RuntimeError, subprocess.TimeoutExpired, ImportError, ValueError, KeyError) as exc:
        print("cannot recalculate: %s" % exc, file=sys.stderr)
        return 2
    except Exception as exc:  # a file openpyxl cannot parse raises many kinds
        print("cannot read the workbook: %s" % exc, file=sys.stderr)
        return 2
    print("%d formulas checked" % count)
    for text in warnings:
        print("WARN: " + text)
    for text in problems:
        print("FAIL: " + text)
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
