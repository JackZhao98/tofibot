#!/usr/bin/env python3
"""Convert an office file with headless LibreOffice into a new folder and print what was written.

Uses a throwaway LibreOffice profile so a stuck or concurrent instance cannot block it. The input is never changed.
"""
from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys
import tempfile


def main():
    parser = argparse.ArgumentParser(description="soffice --headless --convert-to wrapper with a private profile and a timeout.")
    parser.add_argument("input", help="file to convert (left unchanged)")
    parser.add_argument("target", help="target format passed to --convert-to: pdf, docx, odt, xlsx, pptx, html, txt, or a filter form like pdf:writer_pdf_Export")
    parser.add_argument("--outdir", required=True, help="folder for the result; created when missing and must not be the input's folder")
    parser.add_argument("--timeout", type=int, default=180, help="seconds before giving up (default 180)")
    args = parser.parse_args()
    program = shutil.which("soffice") or shutil.which("libreoffice")
    if program is None:
        sys.exit("soffice is not installed on this computer")
    if not os.path.isfile(args.input):
        sys.exit("input file not found: %s" % args.input)
    if os.path.abspath(args.outdir) == os.path.dirname(os.path.abspath(args.input)):
        sys.exit("--outdir must differ from the input's folder so the original stays untouched")
    os.makedirs(args.outdir, exist_ok=True)
    before = set(os.listdir(args.outdir))
    with tempfile.TemporaryDirectory() as profile:
        command = [program, "-env:UserInstallation=file://" + profile, "--headless", "--convert-to", args.target, "--outdir", args.outdir, args.input]
        try:
            result = subprocess.run(command, capture_output=True, text=True, timeout=args.timeout)
        except subprocess.TimeoutExpired:
            sys.exit("soffice did not finish within %d s" % args.timeout)
    if result.returncode != 0:
        sys.exit("soffice failed (%d): %s" % (result.returncode, (result.stderr or result.stdout).strip()[-1500:]))
    written = sorted(set(os.listdir(args.outdir)) - before) or [n for n in os.listdir(args.outdir) if os.path.splitext(n)[0] == os.path.splitext(os.path.basename(args.input))[0]]
    if not written:
        sys.exit("soffice reported success but wrote nothing: %s" % result.stdout.strip())
    for name in written:
        path = os.path.join(args.outdir, name)
        print("wrote %s (%d bytes)" % (path, os.path.getsize(path)))


if __name__ == "__main__":
    main()
