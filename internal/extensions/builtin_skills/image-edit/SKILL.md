---
name: image-edit
description: Edit pictures and screenshots; crop, resize, annotate, blur, combine, convert, view the result.
requires: [computer]
kind: deliverable
---

# Editing pictures and screenshots

## What good looks like

- The edit the request names is done to the right part of the picture, and nothing else about the picture changed.
- The original file is kept. Every edit writes a new file with a new name.
- Annotations sit on the thing they point at. Boxes enclose the named area, labels are readable against the background, and no label covers what it labels.
- A blurred region hides what it should hide. Text under a blur is unreadable in the result.
- Size and format fit the use: a picture sent in chat stays under a few megabytes and about 2000 pixels on the long side unless the request asks for more.

## Getting the input file

- A file the person sent is on the Bot computer at the path listed with it.
- A picture of the screen is saved with the save_to parameter of desktop.capture on computer_desktop; a picture of a page is saved with save_to on browser.snapshot of computer_browser, and full_page together with save_to saves the whole page. save_to is an absolute path under /workspace that ends in .png; the result gives saved_to, saved_width and saved_height, the size of the file's own coordinates.
- A picture or video on the web is downloaded with curl into the Bot's own directory.
- A generated image is at the computer_path its result gives.

## Looking

- A picture reaches you only when you view it; the computer_files tool's files.view action returns a PNG or JPEG as an image, while a path or a listing shows nothing. View the input before choosing coordinates, and view the output before sending it.
- Read the size from the file: `identify -format '%w x %h %m\n' in.png` prints width, height and format. Coordinates for every tool below are pixels of that size, origin at the top left.
- To place a box on a named area, view the picture, estimate the area in pixels, draw, and view the result. Adjust and draw again until the box fits. `tesseract in.png - tsv` lists each recognized word with its pixel box when the area is a piece of text.
- Small details are checked on a crop of the result at full size, because a scaled view hides misalignment.

## Geometry and format

- Crop: `convert in.png -crop 600x400+120+80 +repage out.png` takes width x height at an offset from the top left.
- Resize: `convert in.png -resize 1200x out.png` fits the width and keeps the ratio; `-resize 1200x800\>` only shrinks.
- Rotate: `convert in.jpg -auto-orient -rotate 90 out.jpg`; `-auto-orient` applies the camera's rotation flag, which phone photos carry.
- Convert: `convert in.png out.jpg` picks the format from the extension. A picture with transparency going to JPEG needs `-background white -alpha remove -alpha off`. WebP and PNG keep transparency.
- Reduce size: `convert in.jpg -strip -resize 2000x2000\> -quality 82 out.jpg`. Screenshots with flat color compress better as PNG with `-colors 128` or as WebP.
- Combine side by side: `convert a.png b.png -background white -gravity center +append out.png`; stacked: `-append`. A grid: `montage a.png b.png c.png d.png -tile 2x2 -geometry +8+8 -background white out.png`. Pictures of different sizes are brought to a common height or width with `-resize` before joining.
- ImageMagick 7 offers `magick` in place of `convert`; both take the same arguments.

## Annotating

- `scripts/annotate.py` draws boxes with a label, arrows, numbered badges, text strips, blur and pixelation from a small JSON spec and writes a new picture; `--help` prints the spec format. It finds a font with Chinese, Japanese and Korean glyphs on the computer and exits with a message when none covers the label text.
- Write the spec to a file in the working folder, with positions in pixels of the input. Arrows point from the label toward the target. Numbered badges are placed beside the area and the key to the numbers goes in the reply or in a text strip.
- Choose a color that contrasts with the area. Use two colors when two groups of marks are told apart by color.
- Blur or pixelate sensitive areas (names, numbers, faces) when the request asks to hide them. Check the result at full size, because a light blur on large text stays readable; raise the radius until it is not.
- Several rounds of editing are done from the original each time, so marks do not stack on marks.

## Delivery

- Name the output after the original with a suffix that tells the edit apart; the original stays where it is.
- Send the result with send_message attachments (path). Send several pictures only when the request asks for several.
- Every claim in the reply is true of the delivered file. State the final size and format when they changed, and name the marks as they appear in the picture.
- Publish only the final picture. Drafts and spec files stay in the working folder.

## When something cannot be done

- A picture that does not open, a format the computer cannot write, or a label font that is missing is reported with the file name and the message the tool gave.
- Text inside a picture cannot be edited as text; changing it means painting over the area and drawing new text, and the reply says so.
- A request to remove a person or object cleanly is outside what these tools do. Say so, and offer a blur or a crop.
