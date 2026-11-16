# Built-in Skill catalog

A built-in Skill is a how-to for one concrete job. Only its name and a
one-line description sit in the prompt; the body loads with read_skill when
the work matches (progressive disclosure). Each Skill is one folder under
`internal/extensions/builtin_skills/<name>/`: `SKILL.md` (frontmatter
`name`, `description` with trigger words first and at most 100 runes,
`kind`, and `requires: [computer]` when it needs the Bot computer), optional
`scripts/*.py` and `references/*.md`, a test in `tests/test_<name>.py`, and
an eval task in `eval/resourceful_tasks.json` scored before and after.

A Skill taken from an open-source source keeps its license file and
attribution, names the upstream version it was taken from, passes a
security read of its text and scripts, and is rewritten to this runtime
(tool names, the Linux Bot computer, send_message delivery, no example
sentences in model text).

## In the product

| Skill | Kind | Needs a computer | Job |
|---|---|---|---|
| web-research | procedure | no | look things up, news, compare sources, cite primary pages |
| email | procedure | no | find, read and send mail through connected tools; signed-in browser as fallback |
| web-tasks | procedure | yes | forms, orders, bookings and changes on websites |
| install-software | procedure | yes | install and remove software in user space |
| document-report | deliverable | yes | DOCX and PDF documents and reports |
| slides | deliverable | yes | PPTX decks |
| spreadsheet | deliverable | yes | XLSX workbooks with formulas |
| web-page | deliverable | yes | one self-contained HTML page |
| code-delivery | deliverable | yes | change code, run it, ship an archive |
| video | read | yes | read a video: frames, scenes, on-screen text, speech |

## Batch 1 (in progress; adapted from open-source Skills where a good one exists)

| Skill | Job |
|---|---|
| pdf | read, extract tables, split, merge, fill forms, OCR a scan |
| image-edit | crop, resize, annotate, combine, convert; look at the result |
| video-edit | cut, join, compress, GIF, burn subtitles, extract audio |
| file-convert | Word, PDF, Markdown, HTML, images and archives in both directions |
| data-chart | analyze a table with pandas, draw a chart, look at it |
| web-to-sheet | turn a page's table or list into a spreadsheet with the source link |

## Later batches (candidates)

| Skill | Job | Why it is a candidate |
|---|---|---|
| email-draft | write a reply or a new mail in the person's voice, ready for prepare_email | mail is a daily request; prepare_email exists |
| translate-document | translate a file and keep its layout (DOCX, PPTX, PDF, Markdown) | a common request that plain chat cannot keep formatted |
| page-monitor | watch a page on a schedule and report what changed | uses schedules and the page reader; a common ask |
| audio | text to speech, cut and join audio, transcribe with transcribe_audio | the image has ffmpeg; transcribe_audio exists |
| mac-folder-git | git work in a folder the person granted on their Mac | Mac folders shipped (#73) |
| meeting-notes | notes and action items from a recording or transcript | builds on transcribe_audio |
| receipts-to-sheet | read receipts or invoices (photo, PDF) into a spreadsheet | OCR and spreadsheet tools exist |
| qr-barcode | make and read QR codes and barcodes | a Bot drew a fake QR code once; real tools exist |

## Learn from (no license: study the idea, then write ours from scratch)

The skill folder's NOTICE then says "informed by <url>, no text or code
reused". Nothing below is copied; each line names the idea only. Survey of
2026-11-16 (ComposioHQ/awesome-claude-skills; vercel-labs/agent-skills had
nothing for these jobs).

| For our Skill | Learn from | Idea worth taking |
|---|---|---|
| receipts-to-sheet | https://github.com/ComposioHQ/awesome-claude-skills/tree/master/invoice-organizer | extract vendor, date, number, amount, method; detect duplicates by file hash; list files that need a human check; copy originals, never move. We add: check OCR amounts against the total, no unattended folder watcher |
| file-organize (new candidate) | https://github.com/ComposioHQ/awesome-claude-skills/tree/master/file-organizer | survey, then show the whole plan of moves and deletions, act only after a yes, log every step, prefer archive to delete, keep modification dates. We add: a dry run, an undo list, protected paths |
| citations (web-research) | https://github.com/ComposioHQ/awesome-claude-skills/tree/master/content-research-writer | mark claims that need a source while drafting, keep a running reference list, a pre-send check that every claim has one. We add: open each source and confirm the quote or number |
| email-draft | https://github.com/ComposioHQ/awesome-claude-skills/tree/master/internal-comms | classify the kind of message first, then apply the matching format and tone; ask when none fits. We add: the person's own sent mail as the voice reference |
| meeting-notes | https://github.com/ComposioHQ/awesome-claude-skills/tree/master/meeting-insights-analyzer | evidence with a timestamp for each point. Nothing else (it judges people without consent) |
| resume (later) | https://github.com/ComposioHQ/awesome-claude-skills/tree/master/tailored-resume-generator | rank requirements as must, strong, bonus; map each to real experience; name the gaps; never invent experience |

Apache-2.0 (adapt with attribution, NOTICE and a pinned commit):
anthropics/skills at e4bb5d960d87 for pdf, docx, xlsx, pptx and
slack-gif-creator (its output size budget and shrink loop, for video-edit
GIFs; the system ffmpeg only, never the bundled GPL binary).

Not used: Composio per-app skills (they route mail and accounts through a
third-party hosted service), agent-browser (duplicates our browser tools,
has a credential vault, tells the agent to prefer it), anything AGPL.

## Next

1. Batch 1, after the open-source survey (licenses, red flags, which to adapt).
2. email-draft and translate-document: frequent requests, low risk, no new tool.
3. audio and meeting-notes: build on transcribe_audio.
4. page-monitor: needs a schedule design review (how often, what counts as a change).
5. receipts-to-sheet, qr-barcode, mac-folder-git: when a request or an eval shows the need.

## Rules for every new Skill

- Evidence: an eval task scored before and after; the Skill stays when it
  makes the Bot know it can do the job, even where a model can sometimes do
  it without the Skill.
- Trigger overlap: an eval task also scores which Skill was read
  (video/video-edit, document-report/pdf, spreadsheet/data-chart/web-to-sheet,
  file-convert against all of them).
- Prompt budget: each index line counts on every prompt; the prompt caps
  move only by the measured delta.
- At 30 or more Skills, group the index by kind.
