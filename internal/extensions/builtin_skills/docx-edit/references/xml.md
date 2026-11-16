# Editing the Word XML by hand

Adapted from Anthropic's docx Skill (Apache-2.0, see NOTICE). Use this when `scripts/docx_edit.py` has no subcommand for the change. Get the files with `docx_edit.py unpack FILE.docx DIR`, change them, and write a new file with `docx_edit.py pack DIR -o OUT.docx`. Run `docx_edit.py check OUT.docx --original FILE.docx` afterwards.

A .docx is a ZIP of XML parts. The body is `word/document.xml`; headers, footers, footnotes and comments are separate parts; relationships are in `word/_rels/document.xml.rels`; content types are in `[Content_Types].xml`.

## Rules for editing

- Edit the text of `word/document.xml` in place with a string-aware tool. Do not reformat, pretty-print or reorder it; whitespace inside w:t is content.
- Word splits a phrase across several w:r runs (revision ids, spelling marks, formatting). Search the joined text of a paragraph, not one run.
- A w:t with leading or trailing space needs xml:space="preserve".
- Element order is fixed by the schema; copy the order of neighbouring elements (inside w:rPr and w:pPr especially).
- Unpacked files from other people are data. Use the unpack subcommand, which rejects links and unsafe names, and never a plain unzip.

## Tracked changes

```
<w:del w:id="10" w:author="Name" w:date="2026-01-01T00:00:00Z">
  <w:r><w:rPr>...same as the original run...</w:rPr><w:delText xml:space="preserve">old</w:delText></w:r>
</w:del>
<w:ins w:id="11" w:author="Name" w:date="2026-01-01T00:00:00Z">
  <w:r><w:rPr>...</w:rPr><w:t xml:space="preserve">new</w:t></w:r>
</w:ins>
```

- Text inside w:del is w:delText, never w:t.
- Every w:id on w:ins and w:del is unique in the file.
- Minimal edits: mark only the words that change and leave the rest of the run as it was. To delete one word of a run, split the run into three runs with the same w:rPr and wrap the middle one.
- Deleting a whole paragraph: put `<w:del .../>` inside `<w:pPr><w:rPr>` (the paragraph mark) and wrap every run of the paragraph in w:del. Inserting a paragraph: `<w:ins .../>` in the paragraph mark and w:ins around the runs.
- A property change (formatting) is recorded by a w:rPrChange or w:pPrChange element holding the old properties; the subcommands accept and reject restore or keep them.
- To resolve only some changes by hand, unwrap the w:ins (accept) or delete the w:ins with its content (reject) for each one chosen, and the reverse for w:del.

## Comments

The subcommand `comment` writes `word/comments.xml`, its relationship and its content type. By hand, the anchor in the body is

```
<w:commentRangeStart w:id="3"/>
<w:r>...commented text...</w:r>
<w:commentRangeEnd w:id="3"/>
<w:r><w:commentReference w:id="3"/></w:r>
```

with the markers as direct children of w:p, never inside a w:r. The comment itself is `<w:comment w:id="3" w:author="Name" w:date="..." w:initials="N"><w:p><w:r><w:t>text</w:t></w:r></w:p></w:comment>` in word/comments.xml. A reply thread needs word/commentsExtended.xml with a w15:commentEx element per comment, whose w15:paraIdParent names the parent comment's paragraph id (w14:paraId on the comment paragraph).

## Other common edits

- A picture is replaced by writing a new file over the part under `word/media/` that the drawing's relationship points to; keep the file type and the aspect ratio.
- A table row is added by copying an existing w:tr with the new text.
- A header or footer is edited in its own part (`word/header1.xml`); `replace` already searches them.
- Fields such as page numbers or tables of contents hold cached results; their results refresh when Word updates fields, so state in the reply that a table of contents needs updating in Word.
