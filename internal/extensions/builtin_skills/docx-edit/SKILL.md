---
name: docx-edit
description: Edit an existing .docx with tracked changes, comments or a filled template; keeps formatting.
requires: [computer]
kind: deliverable
---

# Editing an existing Word file

This Skill starts from a Word file the person supplied and returns that file with changes. Writing a new document from nothing belongs to the document-report Skill. A request that names an existing file, a template, tracked changes or comments belongs here.

## What good looks like

- The returned file is the person's own file with the requested changes and nothing else moved: styles, numbering, tables, headers, footers, pictures and page setup are as they were.
- A change that was requested as tracked is a tracked change under the author name the person gave, and only the words that differ are marked. Text that was not part of the request is unchanged and untracked.
- Each requested change is present in the final file, once per place it was asked for, and the reply names any place where the text was not found.
- The pages look right when rendered: no shifted tables, lost pictures, broken numbering or new blank pages.
- The original file is never overwritten; the result is a new file.

## Reading first

- `python3 scripts/docx_edit.py read FILE.docx` prints the paragraphs with their styles, tables by row, headers and footers, comments with the text they are anchored to, and existing tracked changes marked {+added+} and [-deleted-]. The wording to search for is copied from this output.
- Legacy .doc files are converted first with `soffice --headless --convert-to docx --outdir DIR FILE.doc`, and the converted file is the one edited. Say so in the reply.
- A password-protected Word file cannot be edited; report that and ask for an unprotected copy.

## Changing text

- `docx_edit.py replace FILE.docx -o OUT.docx --find 'old text' --replace 'new text'` finds the text even when Word split it across runs, keeps the formatting of the words it replaces, and searches body, tables, headers, footers and notes. `--body-only` limits it to the body.
- A match must be unique. When the text occurs more than once the command lists every occurrence with its surroundings and exits; `--nth N` picks one and `--all` takes them all. Quoting more of the sentence usually makes it unique.
- `--tracked` records the change as a tracked deletion and insertion by `--author NAME`, and marks only the words that differ; `--whole` marks the entire found text. Without `--tracked` the text is replaced in place with no trace.
- Choose tracked when the person asked for tracked changes, review marks or a redline, or when the file already holds tracked changes. Choose untracked for template filling and for corrections the person asked to apply silently.
- Replacement text is one paragraph of plain text; tabs and line breaks are made by hand-editing the XML (references/xml.md).
- A match inside someone else's tracked insertion is refused. Accept or reject the existing changes first, or ask the person which they want.

## Filling a template

- Placeholders (double braces, brackets or blank underscores) are found with `read`. `docx_edit.py replace FILE.docx -o OUT.docx --map values.json` fills every placeholder from a JSON object of find and replace pairs, or a list of objects with find and replace keys, in one pass.
- A placeholder with no value from the person stays and is named in the reply. Values are never invented. After filling, `read` shows no placeholder that should have been filled.
- Lists, repeated table rows and pictures are edited with python-docx on a copy of the file, or by hand-editing the XML.

## Tracked changes and comments

- `docx_edit.py accept FILE.docx -o OUT.docx` accepts every tracked change and `reject` undoes every one, including joined or removed paragraphs, property changes and inserted or deleted table rows. Tracked changes of table cells are reported and left for Word.
- Accepting or rejecting some changes and keeping others is done on the XML (references/xml.md).
- `docx_edit.py comment FILE.docx -o OUT.docx --find 'text' --text 'comment' --author NAME` anchors a comment to the found text; several comments are added by repeating the command on its own output. Replies inside a comment thread are written by hand.
- Comments and tracked changes carry the author name the person gave, or a neutral assistant name when none was given.

## Checking the file

- `docx_edit.py check OUT.docx --original FILE.docx` tests the package: ZIP, well-formed XML, content types, relationships, unique tracked-change ids, comment anchors, and that python-docx opens it. With `--original` it also fails when any text differs from the original outside a tracked change, which finds an accidental untracked edit. It exits non-zero on any failure and the failure is fixed before delivery; it is never ignored.
- Render the result and look at the pages: `soffice --headless --convert-to pdf --outdir DIR OUT.docx` writes the PDF, and `pdftoppm -png -r 80 DIR/OUT.pdf DIR/page` writes one PNG per page. A rendered image reaches you only when you view it; the computer_files tool's files.view action returns a PNG or JPEG as an image, while a path or a listing shows nothing. Compare the pages that carry changes with the original rendered the same way.
- LibreOffice shows tracked changes as colored text; the PDF is a check for the layout and not a deliverable unless the person asked for a PDF.
- Hand edits of the XML: `docx_edit.py unpack FILE.docx DIR` extracts the package into a new folder and `pack DIR -o OUT.docx` zips it with the content types first and no folder entries. The XML is edited without reformatting it, and `check` runs on the packed file.

## Delivering

- Send the final .docx with send_message attachments, giving its path, under a name that tells it from the original.
- Every claim in the reply is true of the delivered file. The reply lists the changes made, says whether they are tracked, and names what was not found or not done and why.
- Publish only the final file; the unpacked folders, PDFs, page images and intermediate copies stay on the Bot computer.
