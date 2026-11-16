---
name: video
description: Read what a video or animation shows and says, with frame times, scenes, on-screen text and speech.
---

# Reading a video

## What good looks like

- The description comes from frames you looked at and speech that was transcribed. Each statement about the video carries the time it applies to.
- Scenes are found by change in the picture as well as by an even spread of samples, so a short scene between two samples is not missed.
- Text on screen is read from a frame at full size. Speech is quoted from a transcript; the wording of narration is never guessed from the picture.
- The reply says what was seen, what was sampled, and what was not checked.

## Probing

- Read the file's facts before decoding any frame: `ffprobe -v error -show_entries format=duration,size:stream=index,codec_type,codec_name,width,height,r_frame_rate -of default=nw=1 in.mp4` gives duration, size, streams, resolution and frame rate. A file with no audio stream has no speech to transcribe.

## Frames and scenes

- Keep the time of every frame you extract. Frames saved without their times cannot be placed in the story.
- An even spread across the duration: `ffmpeg -i in.mp4 -vf 'fps=1/2,showinfo' -fps_mode passthrough f_%03d.png 2>&1 | grep -o 'pts_time:[0-9.]*'` writes one frame every two seconds and prints each frame's time. Choose the rate so a short video gives at least eight frames and a long one stays under about thirty.
- Scene changes: `ffmpeg -i in.mp4 -vf 'select=gt(scene\,0.3),showinfo' -fps_mode vfr s_%03d.png 2>&1 | grep -o 'pts_time:[0-9.]*'` writes a frame at each cut and prints its time. Lower the threshold toward 0.1 for soft transitions and raise it toward 0.5 when too many frames come out. The first frame of the video is not selected by this filter; extract it too.
- One frame at a chosen time: `ffmpeg -ss 00:00:07.5 -i in.mp4 -frames:v 1 at_7.5.png`.

## Looking

- Build a contact sheet and view it before describing the video: `ffmpeg -i in.mp4 -vf 'fps=1/2,scale=320:-1,drawtext=text=%{pts\:hms}:x=4:y=4:fontsize=18:fontcolor=white:box=1:boxcolor=black@0.6,tile=4x3' -frames:v 1 sheet.png` draws each frame's time on it. Cut a long video into several sheets of twelve frames.
- A picture reaches you only when you view it; the computer_files tool's files.view action returns a PNG or JPEG as an image, while a path or a listing shows nothing.
- Small text is unreadable on a sheet. View the single frame at full size, or read it with `tesseract frame.png -`, and compare the output with what you see.
- Compare neighbouring frames to describe what moves, appears or changes between them.

## Speech

- When the run has the transcribe_audio tool, give it the file; a video in mp4 or webm form can be passed whole. A file over the 25 MiB limit or in another format is converted first: `ffmpeg -i in.mp4 -vn -ac 1 -ar 16000 -b:a 48k speech.mp3`, and a long recording is cut into parts with `-ss START -t LENGTH`. Quote the transcript with times when the times matter; the service returns text without times, so cut the audio at known times to place the words.
- Without transcribe_audio, say that the speech was not transcribed and that the transcription service is set up in Settings under dictation. Describe the video from its pictures and text alone, and keep any statement about the narration out of the reply.
- Speech that is missing from the transcript or unclear in it is reported as unclear.

## Reporting

- List what was seen with times: scene, on-screen text, on-screen objects, speech.
- State how the video was sampled: the frame rate used, the scene threshold, and that anything between two samples was not seen.
- Name what was not checked: audio that was not transcribed, text too small to read, parts of the video that were skipped.

## Scope

Making or editing a video is outside this Skill. Cutting and re-encoding are used only to read or transcribe the file.
