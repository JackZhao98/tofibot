#!/usr/bin/env python3
"""Run a command and record exactly what happened.

Executes the command after `--` (no shell unless you pass `sh -c ...`) in
--cwd, with a --timeout, and writes a transcript: the exact command line, the
working directory, the exit code, the duration, stdout and stderr. The
transcript is printed and, with --out, saved. The exit status is the command's
own, so the record and the result cannot disagree. Output beyond --max-chars
keeps its head and tail with the cut marked.
Usage: run_and_record.py [--cwd DIR] [--timeout 120] [--out transcript.txt] -- COMMAND [ARGS...]
"""
from __future__ import annotations

import argparse
import shlex
import subprocess
import sys
import time


def clip(text, limit):
    if len(text) <= limit:
        return text
    half = limit // 2
    return text[:half] + "\n[... %d characters cut ...]\n" % (len(text) - limit) + text[-half:]


def run(command, cwd, timeout, max_chars):
    started = time.time()
    try:
        done = subprocess.run(command, cwd=cwd, capture_output=True, text=True, timeout=timeout, errors="replace")
        code, out, err = done.returncode, done.stdout, done.stderr
    except subprocess.TimeoutExpired as exc:
        code = 124
        out = (exc.stdout.decode("utf-8", "replace") if isinstance(exc.stdout, bytes) else exc.stdout) or ""
        err = ((exc.stderr.decode("utf-8", "replace") if isinstance(exc.stderr, bytes) else exc.stderr) or "") + "\n[timed out after %ss]" % timeout
    except FileNotFoundError as exc:
        code, out, err = 127, "", "command not found: %s" % exc.filename
    lines = [
        "$ " + " ".join(shlex.quote(part) for part in command),
        "cwd: %s" % (cwd or "."),
        "exit code: %d" % code,
        "duration: %.2fs" % (time.time() - started),
        "--- stdout ---", clip(out, max_chars).rstrip("\n"),
        "--- stderr ---", clip(err, max_chars).rstrip("\n"),
    ]
    return code, "\n".join(lines) + "\n"


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--cwd")
    parser.add_argument("--timeout", type=float, default=120)
    parser.add_argument("--out")
    parser.add_argument("--max-chars", type=int, default=6000)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args(argv)
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command:
        parser.error("give the command after --")
    code, transcript = run(command, args.cwd, args.timeout, args.max_chars)
    sys.stdout.write(transcript)
    if args.out:
        with open(args.out, "w", encoding="utf-8") as handle:
            handle.write(transcript)
    return code


if __name__ == "__main__":
    sys.exit(main())
