---
name: video-edit
description: Make a new video or GIF from a clip; cut, join, compress, GIF, subtitles, audio, speed.
requires: [computer]
kind: deliverable
---

# Editing a video

Reading what a video shows or says belongs to the video Skill. This Skill produces a new file from a clip.

## What good looks like

- The new file holds the part of the clip the request names, plays in a common player, and meets any size or length limit in the request.
- The original file is kept. Every edit writes a new file.
- Picture and sound stay in sync after cutting, joining and speed changes. Joined clips share one size and frame rate.
- Burned subtitles are readable, inside the frame and in the right language and encoding.
- A GIF is only as large as the budget demands: the shrink order keeps the frame rate smooth and the picture legible as long as the size allows.

## Getting the input file

- A file the person sent is on the Bot computer at the path listed with it.
- A frame of the screen or of a page is saved with the save_to parameter of desktop.capture on computer_desktop or of browser.snapshot on computer_browser (an absolute .png path under /workspace).
- A video on the web is downloaded with curl into the Bot's own directory.

## Probing

- `ffprobe -v error -show_entries format=duration,size,bit_rate:stream=index,codec_type,codec_name,width,height,r_frame_rate -of default=nw=1 in.mp4` gives duration, size, streams, resolution and frame rate. Read it before editing and again on the output.
- The system ffmpeg is used. No binary is downloaded or bundled.

## Cutting, joining, speed, audio

- Cut by time with re-encoding, which is frame accurate: `ffmpeg -ss 00:00:03 -to 00:00:10 -i in.mp4 -c:v libx264 -c:a aac -movflags +faststart out.mp4`. With `-ss` before `-i` the times count from the start of the input.
- Cut without re-encoding is fast and keeps quality, and it can start up to a few seconds early because it snaps to a keyframe: `ffmpeg -ss 3 -to 10 -i in.mp4 -c copy out.mp4`. Use re-encoding when the cut must be exact.
- Join clips of the same codec, size and frame rate: write a file list with lines of the form `file 'a.mp4'` and use `ffmpeg -f concat -safe 0 -i list.txt -c copy out.mp4`. Clips that differ are brought to one size first: `ffmpeg -i a.mp4 -i b.mp4 -filter_complex '[0:v]scale=1280:720,setsar=1,fps=30[v0];[1:v]scale=1280:720,setsar=1,fps=30[v1];[0:a][1:a]concat=n=2:v=1:a=1[a];[v0][v1]concat=n=2:v=1:a=0[v]' -map '[v]' -map '[a]' out.mp4` joins two clips that both have audio; clips with no audio drop the audio inputs from the filter.
- Speed: `ffmpeg -i in.mp4 -filter_complex '[0:v]setpts=PTS/2[v];[0:a]atempo=2[a]' -map '[v]' -map '[a]' out.mp4` plays at double speed; use `setpts=PTS*2` and `atempo=0.5` for half speed. One `atempo` takes factors from 0.5 to 2; chain two for more.
- Extract audio: `ffmpeg -i in.mp4 -vn -c:a libmp3lame -q:a 4 out.mp3`, or `-c:a copy out.m4a` when the stream is AAC. A video with no audio stream has nothing to extract; say so.
- Remove audio: `-an`. Replace the sound track: `ffmpeg -i video.mp4 -i sound.mp3 -map 0:v -map 1:a -c:v copy -shortest out.mp4`.

## Compressing to a size target

- `scripts/compress_to_size.py` re-encodes with two-pass H.264 so the file lands under a megabyte target; `--help` lists the options. It exits with a message when the target leaves too little bitrate for the length; shorten the clip, lower the resolution with `--max-height`, or raise the target.
- For a quality-based result, `ffmpeg -i in.mp4 -c:v libx264 -crf 28 -preset medium -vf scale=-2:720 -c:a aac -b:a 96k out.mp4`; a higher crf gives a smaller file.
- Check the size of the output against the limit with `ls -l`; a messaging or mail limit is met by the output, not by the setting.

## GIF within a size budget

- `scripts/gif_budget.py` cuts a window of the clip and builds a GIF with a generated palette, then shrinks frame rate, colors and width in that order until the file fits the byte budget; `--help` lists the options. It prints each attempt and exits with code 2 when even the smallest setting is over budget. Pass `--start` and `--duration` for the part to keep.
- A GIF has no sound. Long clips grow fast: a few seconds at 480 pixels wide is the usual range under one megabyte. When a budget cannot be met, cut the clip shorter or offer a short MP4 as well.
- Palette method by hand: `ffmpeg -t 4 -i in.mp4 -vf 'fps=12,scale=480:-2:flags=lanczos,split[a][b];[a]palettegen=max_colors=128[p];[b][p]paletteuse=dither=bayer' out.gif`.

## Burning subtitles

- `ffmpeg -i in.mp4 -vf 'subtitles=subs.srt:force_style=FontName=Noto Sans CJK SC\,Fontsize=22\,Outline=2' -c:a copy out.mp4` draws the subtitles into the picture. The filter needs the .srt in the working folder with a plain path; copy it there when the name has spaces or punctuation.
- A .srt saved in a legacy encoding shows broken characters; convert it with `iconv -f GB18030 -t UTF-8 in.srt > subs.srt` after checking the encoding with `file -i`.
- For Chinese, Japanese or Korean text name an installed CJK font in `force_style` (commas inside it are escaped with a backslash); `fc-list :lang=zh` lists the candidates. A missing font draws empty boxes, which the contact sheet shows.
- Soft subtitles that a player can switch on and off: `ffmpeg -i in.mp4 -i subs.srt -c copy -c:s mov_text out.mp4`.

## Checking the result

- Probe the output and compare duration, size, resolution and audio presence with what the request asked for. A cut of 10 seconds has a duration near 10.
- Build a contact sheet of the output and view it: `ffmpeg -i out.mp4 -vf 'fps=1,scale=320:-1,drawtext=text=%{pts\:hms}:x=4:y=4:fontsize=18:fontcolor=white:box=1:boxcolor=black@0.6,tile=4x3' -frames:v 1 sheet.png`. A picture reaches you only when you view it; the computer_files tool's files.view action returns a PNG or JPEG as an image. For a GIF the same command works on the GIF file.
- Look at subtitle frames at full size, and check the first and last frames of a cut or a join for black frames or a frozen picture.

## Delivery

- Name the output after the original with a suffix that tells the edit apart.
- Send the result with send_message attachments (path). A file over the chat limit is reduced with the size tools above before it is sent.
- Every claim in the reply is true of the delivered file. State the duration, size, resolution and what was dropped, audio included.
- Publish only the final file. Test cuts, palettes and contact sheets stay in the working folder.

## When something cannot be done

- A codec ffmpeg cannot read or write, a missing subtitle font, or a size target the clip cannot meet is reported with the number that shows it and the nearest option that works.
