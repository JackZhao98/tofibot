#!/usr/bin/env python3
"""Make a GIF from a video clip that uses a byte budget well: the largest width and frame rate that fit, a small margin under the limit.

Uses the system ffmpeg (palettegen and paletteuse). The input video is never changed.
Idea of a size budget with a shrink loop informed by Anthropic's slack-gif-creator (Apache-2.0); no code reused.
"""
from __future__ import annotations

import argparse
import os
import subprocess
import sys
import tempfile

FPS_STEPS = [15, 12, 10, 8, 6]
COLOR_STEPS = [256, 128, 64, 32]
# The GIF aims near the budget: the largest picture and smoothest motion that
# fit, a small margin under the limit, so a budget is used and not wasted.
MARGIN = 0.95
WIDTH_STEP = 16


def run(command):
    result = subprocess.run(command, capture_output=True, text=True)
    if result.returncode != 0:
        sys.exit("ffmpeg failed: %s\n%s" % (" ".join(command), result.stderr[-1500:]))


def build(source, output, start, duration, fps, width, colors, palette):
    window = []
    if start:
        window += ["-ss", str(start)]
    if duration:
        window += ["-t", str(duration)]
    scale = "fps=%d,scale=%d:-2:flags=lanczos" % (fps, width)
    run(["ffmpeg", "-v", "error", "-y"] + window + ["-i", source, "-vf", "%s,palettegen=max_colors=%d:stats_mode=diff" % (scale, colors), palette])
    run(["ffmpeg", "-v", "error", "-y"] + window + ["-i", source, "-i", palette, "-lavfi",
                                                    "%s[x];[x][1:v]paletteuse=dither=bayer:bayer_scale=4:diff_mode=rectangle" % scale,
                                                    "-loop", "0", output])
    return os.path.getsize(output)


def source_width(source):
    out = subprocess.run(["ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width", "-of", "csv=p=0", source],
                         capture_output=True, text=True)
    if out.returncode != 0 or not out.stdout.strip():
        sys.exit("ffprobe could not read a video stream from %s" % source)
    return int(out.stdout.strip().split(",")[0])


def search_width(fits, low, high):
    """Largest width in [low, high] (multiples of WIDTH_STEP, low always tried) that fits, or None."""
    if fits(low) is None:
        return None
    best = low
    lo, hi = low // WIDTH_STEP + 1, high // WIDTH_STEP
    while lo <= hi:
        mid = (lo + hi) // 2
        if fits(mid * WIDTH_STEP) is not None:
            best, lo = mid * WIDTH_STEP, mid + 1
        else:
            hi = mid - 1
    if best < high and fits(high) is not None:
        best = high
    return best


def main():
    parser = argparse.ArgumentParser(description="Video clip to a GIF that uses a size budget well: the largest width and frame rate that fit, with a small margin.")
    parser.add_argument("input", help="video file (left unchanged)")
    parser.add_argument("output", help="GIF to write; must differ from the input")
    parser.add_argument("--start", type=float, default=0, help="clip start in seconds (default 0)")
    parser.add_argument("--duration", type=float, default=0, help="clip length in seconds (default: to the end)")
    parser.add_argument("--max-bytes", type=int, default=1000000, help="size budget in bytes (default 1000000)")
    parser.add_argument("--width", "--max-width", dest="width", type=int, default=960, help="largest width tried (default 960; never beyond the source)")
    parser.add_argument("--min-width", type=int, default=160, help="smallest width tried (default 160)")
    parser.add_argument("--good-width", type=int, default=480, help="a width this large ends the search at the current frame rate (default 480)")
    args = parser.parse_args()
    if os.path.abspath(args.input) == os.path.abspath(args.output):
        sys.exit("output must be a different file from the input")
    high = max(args.min_width, min(args.width, source_width(args.input)) // 2 * 2)
    low = min(args.min_width, high)
    good = min(args.good_width, high)
    limit = int(args.max_bytes * MARGIN)
    with tempfile.TemporaryDirectory() as tmp:
        palette = os.path.join(tmp, "palette.png")
        results = {}

        def attempt(fps, width, colors):
            key = (fps, width, colors)
            if key not in results:
                path = os.path.join(tmp, "f%d-w%d-c%d.gif" % key)
                size = build(args.input, path, args.start, args.duration, fps, width, colors, palette)
                print("try fps=%d width=%d colors=%d -> %d bytes" % (fps, width, colors, size))
                results[key] = (size, path)
            size, path = results[key]
            return path if size <= limit else None

        chosen = None
        for colors in COLOR_STEPS:
            # Highest frame rate first; a lower rate buys a wider picture.
            for fps in FPS_STEPS:
                width = search_width(lambda w: attempt(fps, w, colors), low, high)
                if width is None:
                    continue
                if chosen is None or width > chosen[1]:
                    chosen = (fps, width, colors)
                if width >= good:
                    break
            if chosen is not None:
                break
        if chosen is None:
            smallest = min(size for size, _ in results.values())
            print("smallest GIF is %d bytes, over the budget of %d; shorten the clip or accept a smaller picture" % (smallest, args.max_bytes), file=sys.stderr)
            sys.exit(2)
        size, path = results[chosen]
        with open(path, "rb") as src, open(args.output, "wb") as dst:
            dst.write(src.read())
    print("wrote %s: %d bytes (%d%% of the budget %d), fps=%d width=%d colors=%d" % (args.output, size, round(100 * size / args.max_bytes), args.max_bytes, chosen[0], chosen[1], chosen[2]))


if __name__ == "__main__":
    main()
