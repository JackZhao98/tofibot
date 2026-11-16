#!/usr/bin/env python3
"""Re-encode a video with H.264 and AAC so the file lands just under a byte target (two-pass bitrate from the duration).

Uses the system ffmpeg. The input video is never changed.
"""
from __future__ import annotations

import argparse
import os
import subprocess
import sys
import tempfile


def probe(path, entries, stream=None):
    command = ["ffprobe", "-v", "error"] + (["-select_streams", stream] if stream else []) + ["-show_entries", entries, "-of", "csv=p=0", path]
    out = subprocess.run(command, capture_output=True, text=True)
    if out.returncode != 0:
        sys.exit("ffprobe failed: %s" % out.stderr.strip())
    return out.stdout.strip()


def run(command):
    result = subprocess.run(command, capture_output=True, text=True)
    if result.returncode != 0:
        sys.exit("ffmpeg failed: %s\n%s" % (" ".join(command), result.stderr[-1500:]))


def main():
    parser = argparse.ArgumentParser(description="Compress a video to a size target with two-pass H.264.")
    parser.add_argument("input", help="video file (left unchanged)")
    parser.add_argument("output", help="mp4 to write; must differ from the input")
    parser.add_argument("--target-mb", type=float, required=True, help="size target in megabytes (1 MB = 1000000 bytes)")
    parser.add_argument("--max-height", type=int, default=0, help="scale down to at most this height (0 keeps the size)")
    parser.add_argument("--audio-kbps", type=int, default=96, help="audio bitrate (default 96); ignored when the input has no audio")
    args = parser.parse_args()
    if os.path.abspath(args.input) == os.path.abspath(args.output):
        sys.exit("output must be a different file from the input")
    duration = float(probe(args.input, "format=duration"))
    has_audio = bool(probe(args.input, "stream=codec_type", "a"))
    audio = args.audio_kbps if has_audio else 0
    target = args.target_mb * 1000000
    # Leave 5 percent for container overhead; a measured overshoot lowers the bitrate and encodes again, up to three times.
    video_kbps = int(target * 8 * 0.95 / duration / 1000) - audio
    scale = ["-vf", "scale=-2:'min(%d,ih)'" % args.max_height] if args.max_height else []
    for attempt in range(3):
        if video_kbps < 50:
            sys.exit("a %.2f MB target leaves under 50 kbit/s for the picture over %.0f s; raise the target, cut the clip or add --max-height" % (args.target_mb, duration))
        with tempfile.TemporaryDirectory() as tmp:
            log = os.path.join(tmp, "pass")
            base = ["ffmpeg", "-v", "error", "-y", "-i", args.input] + scale + ["-c:v", "libx264", "-b:v", "%dk" % video_kbps, "-pix_fmt", "yuv420p", "-passlogfile", log]
            run(base + ["-pass", "1", "-an", "-f", "null", os.devnull])
            tail = ["-c:a", "aac", "-b:a", "%dk" % audio] if has_audio else ["-an"]
            run(base + ["-pass", "2"] + tail + ["-movflags", "+faststart", args.output])
        size = os.path.getsize(args.output)
        print("attempt %d: %d bytes (target %d), video %d kbit/s, audio %d kbit/s" % (attempt + 1, size, int(target), video_kbps, audio))
        if size <= target:
            print("wrote %s: %d bytes" % (args.output, size))
            return
        video_kbps = int(video_kbps * target / size * 0.95)
    os.remove(args.output)
    sys.exit("could not reach the target in three attempts; raise the target, add --max-height 480 or cut the clip")


if __name__ == "__main__":
    main()
