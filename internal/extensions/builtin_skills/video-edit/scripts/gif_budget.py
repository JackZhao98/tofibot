#!/usr/bin/env python3
"""Make a GIF from a video clip that fits a byte budget, shrinking frame rate, width and colors until it does.

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


def main():
    parser = argparse.ArgumentParser(description="Video clip to a GIF under a size budget (shrink loop over fps, width, colors).")
    parser.add_argument("input", help="video file (left unchanged)")
    parser.add_argument("output", help="GIF to write; must differ from the input")
    parser.add_argument("--start", type=float, default=0, help="clip start in seconds (default 0)")
    parser.add_argument("--duration", type=float, default=0, help="clip length in seconds (default: to the end)")
    parser.add_argument("--max-bytes", type=int, default=1000000, help="size budget in bytes (default 1000000)")
    parser.add_argument("--width", type=int, default=480, help="starting width in pixels (default 480; never enlarged beyond the source)")
    parser.add_argument("--min-width", type=int, default=160, help="smallest width tried (default 160)")
    args = parser.parse_args()
    if os.path.abspath(args.input) == os.path.abspath(args.output):
        sys.exit("output must be a different file from the input")
    width = min(args.width, source_width(args.input))
    # Order of shrinking: frame rate first, then colors at the lowest rate, then width at each step down.
    plan = [(fps, width, 256) for fps in FPS_STEPS]
    w = width
    while w > args.min_width:
        w = max(args.min_width, int(w * 0.8) // 2 * 2)
        for colors in COLOR_STEPS:
            plan.append((FPS_STEPS[-2], w, colors))
    best = None
    with tempfile.TemporaryDirectory() as tmp:
        palette = os.path.join(tmp, "palette.png")
        candidate = os.path.join(tmp, "try.gif")
        for fps, w, colors in plan:
            size = build(args.input, candidate, args.start, args.duration, fps, w, colors, palette)
            print("try fps=%d width=%d colors=%d -> %d bytes" % (fps, w, colors, size))
            if best is None or size < best[0]:
                best = (size, candidate + ".best", fps, w, colors)
                with open(candidate, "rb") as src, open(best[1], "wb") as dst:
                    dst.write(src.read())
            if size <= args.max_bytes:
                break
        if best[0] > args.max_bytes:
            print("smallest GIF is %d bytes, over the budget of %d; shorten the clip or accept a smaller picture" % (best[0], args.max_bytes), file=sys.stderr)
            sys.exit(2)
        with open(best[1], "rb") as src, open(args.output, "wb") as dst:
            dst.write(src.read())
    print("wrote %s: %d bytes, fps=%d width=%d colors=%d (budget %d)" % (args.output, best[0], best[2], best[3], best[4], args.max_bytes))


if __name__ == "__main__":
    main()
