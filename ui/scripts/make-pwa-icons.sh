#!/bin/sh
# Regenerates the install icons in public/pwa from public/brand/tofi-app-icon.svg (needs rsvg-convert and ImageMagick).
# "any" icons keep the rounded tile; the maskable icon is a full-bleed square with the art inside the 80% safe zone.
set -e
cd "$(dirname "$0")/.."
src=public/brand/tofi-app-icon.svg
out=public/pwa
tmp=$(mktemp -d)
mkdir -p "$out"
rsvg-convert -w 192 -h 192 "$src" -o "$out/icon-192.png"
rsvg-convert -w 512 -h 512 "$src" -o "$out/icon-512.png"
rsvg-convert -w 368 -h 368 "$src" -o "$tmp/maskable.png"
magick -size 512x512 xc:'#4FB3A0' "$tmp/maskable.png" -gravity center -composite -alpha off "$out/icon-maskable-512.png"
# iOS fills transparency with black, so the touch icon is an opaque full-bleed square.
rsvg-convert -w 148 -h 148 "$src" -o "$tmp/touch.png"
magick -size 180x180 xc:'#4FB3A0' "$tmp/touch.png" -gravity center -composite -alpha off "$out/apple-touch-icon-180.png"
rm -rf "$tmp"
