// V3 kit, files and rich objects: FileTile, FileRow, ArtifactCard, AttachmentGrid, Letter, InboxDigest, DraftCard,
// ScheduleCard, CodeBlock (V3 PLAN 0.8). The pictures are specimens.
import { useState } from "react";
import { ArtifactCard } from "../../ds/ArtifactCard/ArtifactCard";
import { AttachmentGrid } from "../../ds/AttachmentGrid/AttachmentGrid";
import { BotAvatar } from "../../ds/BotAvatar/BotAvatar";
import { Button } from "../../ds/Button/Button";
import { CodeBlock } from "../../ds/CodeBlock/CodeBlock";
import { DraftCard } from "../../ds/DraftCard/DraftCard";
import { FileRow } from "../../ds/FileRow/FileRow";
import { FileTile } from "../../ds/FileTile/FileTile";
import type { FileKind } from "../../ds/FileTile/parts";
import { InboxDigest } from "../../ds/InboxDigest/InboxDigest";
import { Letter } from "../../ds/Letter/Letter";
import { ScheduleCard } from "../../ds/ScheduleCard/ScheduleCard";
import type { CatalogState } from "../types";
import { Dots, Row, Specimen } from "./v3-parts";

// Specimen pictures: SVG drawings in public/catalog/.
const PIC = Object.fromEntries(["sunset", "valley", "field", "night", "desert", "lake"].map(name => [name, `/catalog/${name}.svg`])) as Record<"sunset" | "valley" | "field" | "night" | "desert" | "lake", string>;
const PDF = "/catalog/report-page.svg";
const WEB = "/catalog/web-page.svg";
const noop = () => {};

const KINDS: [FileKind, string, string][] = [
  ["pdf", "pdf", "Weekly report.pdf"], ["doc", "docx", "Contract.docx"], ["sheet", "csv", "Prices.csv"], ["slides", "pptx", "Launch.pptx"],
  ["image", "png", "Cover.png"], ["code", "py", "sync.py"], ["md", "md", "Notes.md"], ["archive", "zip", "Assets.zip"],
  ["video", "mov", "Recording.mov"], ["audio", "mp3", "Voice note.mp3"], ["page", "html", "Compare.html"], ["text", "txt", "Readme.txt"],
];

const PY = ["import json", "from pathlib import Path", "", "# Read every report and keep the open ones.", "def load(folder):", "    rows = []", "    for path in Path(folder).glob(\"*.json\"):", "        data = json.loads(path.read_text())", "        if data[\"status\"] != \"closed\":", "            rows.append(data)", "    return rows", "", "def total(rows):", "    return sum(r[\"amount\"] for r in rows) * 1.08", "", "def main():", "    rows = load(\"reports\")", "    print(f\"{len(rows)} open, total {total(rows):.2f}\")", "    return 0"];

function Fold() {
  const [folded, setFolded] = useState(true);
  return <Letter label="Mail from Lin" from="Lin Wei" to="you@tofi.example" subject="Q3 price table, one change" postmark={["Received", "09:12", "11.17"]}
    note={{ avatar: <BotAvatar name="Mochi" coat="teal" size={28} src="/brand/tofi-logo-calico.svg" />, title: "Mochi's read", text: "Lin moves the Europe prices to Monday. Needs your yes before noon." }}
    folded={folded} more={folded ? "▾ Show full mail · 1 attachment" : "▴ Fold"} onMore={() => setFolded(!folded)}
    actions={<Button variant="secondary" size="sm">Reply</Button>} hint="Mochi drafted a reply" primary={<Button variant="primary" size="sm">Open draft</Button>}>
    <p style={{ margin: 0 }}>Hi,</p>
    <p style={{ margin: 0 }}>The Europe column changes on Monday, not Friday. Berlin and Paris move to the new list; London stays.</p>
    <p style={{ margin: 0 }}>Can you confirm before noon so I can send the table to the shops?</p>
    <p style={{ margin: 0 }}>Thanks, Lin</p>
  </Letter>;
}

export const files: CatalogState[] = [
  { id: "file-tile-compact", kind: "inline", label: "FileTile · compact row: download, uploading, expired", note: "Archives and unknown binaries have no preview.",
    render: () => <div className="tb-files">
      <FileTile kind="archive" ext="zip" name="firmware-2.4.zip" meta="7.9 MB" onDownload={noop} />
      <FileTile kind="video" ext="mov" name="Recording.mov" meta="Uploading 62%" />
      <FileTile kind="doc" ext="docx" name="Old contract.docx" meta="Expired" />
    </div> },
  { id: "file-tile-preview", kind: "inline", label: "FileTile · preview card: table, Markdown, code, audio", note: "The real first rows and lines, never a fake thumbnail.",
    render: () => <div className="tb-files">
      <FileTile kind="sheet" ext="csv" name="Prices-Q3.csv" meta="85 rows · 24 KB" onDownload={noop} onOpen={noop} preview={{ type: "table", rows: [["City", "Country", "Price"], ["Tokyo", "Japan", "¥4,800"], ["Shanghai", "China", "¥4,200"], ["Berlin", "Germany", "€39"]] }} />
      <FileTile kind="md" ext="md" name="Meeting notes.md" meta="2 KB" onDownload={noop} onOpen={noop} preview={{ type: "md", lines: ["Weekly sync", "Price table moves to Monday.", "Site redesign slips one week.", "Lin owns the Europe prices."] }} />
      <FileTile kind="code" ext="py" name="sync.py" meta="19 lines" onDownload={noop} onOpen={noop} preview={{ type: "code", lines: PY.slice(0, 7) }} />
      <FileTile kind="audio" ext="mp3" name="Voice note.mp3" meta="0:42 · 640 KB" onDownload={noop} onOpen={noop} preview={{ type: "audio", label: "0:42" }} />
    </div> },
  { id: "file-tile-media", kind: "inline", label: "FileTile · preview card: PDF, page, video, picture",
    render: () => <div className="tb-files">
      <FileTile kind="pdf" name="Weekly report.pdf" meta="3 pages · 214 KB" onDownload={noop} onOpen={noop} preview={{ type: "pdf", src: PDF, label: "3 pages" }} />
      <FileTile kind="page" ext="html" name="Compare.html" meta="Web page · 48 KB" onDownload={noop} onOpen={noop} preview={{ type: "page", src: WEB }} />
      <FileTile kind="video" ext="mov" name="Recording.mov" meta="0:42 · 12 MB" onDownload={noop} onOpen={noop} preview={{ type: "video", src: PIC.lake, label: "0:42" }} />
      <FileTile kind="image" ext="png" name="Cover.png" meta="1600 × 1000" onDownload={noop} onOpen={noop} preview={{ type: "image", src: PIC.sunset }} />
    </div> },
  { id: "file-glyphs", kind: "inline", label: "File glyph · one hue per kind",
    render: () => <div className="catalog-stack" style={{ maxWidth: 360 }}>{KINDS.map(([kind, ext, name]) => <FileRow key={kind} kind={kind} ext={ext} name={name} meta={kind} onOpen={noop} />)}</div> },
  { id: "file-row", kind: "inline", label: "FileRow · glyph, thumbnail, fresh", note: "For lists: the Artifacts widget, pickers. Not inside messages.",
    render: () => <Dots><div className="catalog-stack" style={{ maxWidth: 360 }}>
      <FileRow kind="pdf" name="Weekly report.pdf" meta="3 pages · just now" fresh freshLabel="New" onOpen={noop} />
      <FileRow thumb={PIC.sunset} name="Homepage.png" meta="1600 × 1000" onOpen={noop} />
      <FileRow kind="sheet" ext="csv" name="Prices-Q3.csv" meta="85 rows · yesterday" onOpen={noop} />
      <FileRow kind="doc" ext="docx" name="A very long file name that has to be cut at the edge of the row.docx" meta="Expired" onOpen={noop} />
    </div></Dots> },
  { id: "artifact", kind: "inline", label: "ArtifactCard · PDF, page with version, no preview", note: "224px in a message: the card alone.",
    render: () => <div className="tb-files">
      <ArtifactCard kind="pdf" name="Weekly report.pdf" meta="3 pages · 214 KB" preview={{ type: "pdf", src: PDF, label: "3 pages" }} onDownload={noop} onOpen={noop} />
      <ArtifactCard kind="page" ext="html" name="Compare.html" meta="Web page · 48 KB" version="v3" preview={{ type: "page", src: WEB }} onDownload={noop} onOpen={noop} />
      <ArtifactCard kind="archive" ext="zip" name="Site assets.zip" meta="48 MB" onDownload={noop} onOpen={noop} />
    </div> },
  { id: "artifact-list", kind: "inline", label: "ArtifactCard · on the Artifacts cover: who made it, go to message, wide",
    render: () => <div className="catalog-stack" style={{ maxWidth: 360 }}>
      <ArtifactCard wide kind="pdf" name="Weekly report.pdf" meta="3 pages · 214 KB" version="v2" preview={{ type: "pdf", src: PDF }} from="Probe" onGoto={noop} onDownload={noop} onOpen={noop} />
    </div> },
  { id: "attach-1", kind: "inline", label: "AttachmentGrid · one: landscape and portrait keep their shape",
    render: () => <Row>
      <Specimen label="1600 × 1000"><AttachmentGrid items={[{ src: PIC.sunset, width: 1600, height: 1000, alt: "Sunset over hills" }]} onOpen={noop} /></Specimen>
      <Specimen label="900 × 1200"><AttachmentGrid items={[{ src: PIC.valley, width: 900, height: 1200, alt: "Morning valley" }]} onOpen={noop} /></Specimen>
    </Row> },
  { id: "attach-2-4", kind: "inline", label: "AttachmentGrid · two, three with a clip, four",
    render: () => <Row>
      <Specimen label="2"><AttachmentGrid items={[PIC.field, PIC.night]} onOpen={noop} /></Specimen>
      <Specimen label="3 (clip)"><AttachmentGrid items={[PIC.night, { src: PIC.lake, kind: "video", duration: "0:42" }, PIC.field]} onOpen={noop} /></Specimen>
      <Specimen label="4"><AttachmentGrid items={[PIC.sunset, PIC.valley, PIC.field, PIC.night]} onOpen={noop} /></Specimen>
    </Row> },
  { id: "attach-5", kind: "inline", label: "AttachmentGrid · five or more: two over three, +N",
    render: () => <AttachmentGrid items={[PIC.lake, PIC.sunset, PIC.valley, PIC.field, PIC.night, PIC.desert, PIC.sunset, PIC.valley, PIC.field]} onOpen={noop} /> },
  { id: "letter-in", kind: "inline", label: "Letter · in, folded (open it with the button)", note: "The paper stays light in Night.",
    render: () => <Fold /> },
  { id: "letter-in-open", kind: "inline", label: "Letter · in, open, no note",
    render: () => <Letter from="Lin Wei" to="you@tofi.example" subject="Lunch on Friday?" postmark={["Received", "12:03", "11.16"]} actions={<Button variant="ghost" size="sm">Reply</Button>}>
      <p style={{ margin: 0 }}>Hi, are you free for lunch on Friday? The new place on Pine opens at noon.</p>
    </Letter> },
  { id: "letter-out", kind: "inline", label: "Letter · out: cat stamp, CC, sign, attachment, Send",
    render: () => <Letter direction="out" to="Lin Wei" cc="Mia Chen" subject="Re: Q3 price table" stamp={<img src="/brand/tofi-logo-calico.svg" alt="" width={34} height={34} />}
      note={{ avatar: <BotAvatar name="Mochi" coat="teal" size={28} src="/brand/tofi-logo-calico.svg" />, title: "Mochi's draft", text: "Confirms Monday and asks for the new list in CSV." }}
      sign="Mochi, for Mia" attachment="Prices-Q3.csv · 24 KB" actions={<Button variant="ghost" size="sm">Edit</Button>} hint="Nothing is sent before you press Send" primary={<Button variant="primary" size="sm" icon="send">Send</Button>}>
      <p style={{ margin: 0 }}>Hi Lin, Monday works. Please send the new Europe list as a CSV so I can check it against the old one.</p>
    </Letter> },
  { id: "inbox", kind: "inline", label: "InboxDigest · one lifted letter, a bill, a notice", note: "A digest has no actions; the lifted letter opens as a Letter.",
    render: () => <InboxDigest label="Sorted mail" items={[
      { from: "Lin Wei", subject: "Q3 price table, one change", tag: { label: "Needs reply", tone: "tomato" }, time: "09:12", lifted: true },
      { from: "Billing", subject: "Invoice 1042 is ready", tag: { label: "Bill" }, time: "08:40" },
      { from: "Calendar", subject: "Weekly sync moved to 15:00", tag: { label: "Notice", tone: "quiet" }, time: "08:02" },
      { from: "Newsletter", subject: "Ten tips for a tidy inbox", tag: { label: "Ignore", tone: "quiet" }, time: "Mon" },
    ]} /> },
  { id: "draft-message", kind: "inline", label: "DraftCard · pending message", note: "Behind a notebook margin rule; the destination is named before the press.",
    render: () => <DraftCard kind="Draft" title="Post to the team" dest="Posts to “Launch group”" actions={<Button variant="ghost" size="sm">Edit</Button>} hint="Nothing is posted before you press Send" primary={<Button variant="primary" size="sm">Send</Button>}>
      Price table moves to Monday. Berlin and Paris switch to the new list; London stays on the old one until the 30th.
    </DraftCard> },
  { id: "draft-form", kind: "inline", label: "DraftCard · pending form; a changed field carries the highlighter",
    render: () => <DraftCard kind="Needs you" title="Expense claim" dest="→ Company expenses · form no. 2291" fields={[["Amount", "¥4,200", true], ["Date", "11.16"], ["Category", "Travel · train", true], ["Note", "Shanghai trip"]]}
      actions={<Button variant="ghost" size="sm">Edit</Button>} primary={<Button variant="primary" size="sm">Submit</Button>} /> },
  { id: "draft-after", kind: "inline", label: "DraftCard · sent and canceled: two lines and a stamped receipt",
    render: () => <div className="catalog-stack">
      <DraftCard state="sent" receipt="Posted at 14:20" after={<Button variant="ghost" size="sm">View</Button>}>Price table moves to Monday. Berlin and Paris switch to the new list; London stays on the old one until the 30th.</DraftCard>
      <DraftCard state="canceled" receipt="Canceled at 14:21" after={<Button variant="ghost" size="sm">Restore</Button>}>Price table moves to Monday. Berlin and Paris switch to the new list; London stays on the old one until the 30th.</DraftCard>
    </div> },
  { id: "sched-states", kind: "inline", label: "ScheduleCard · done, running, paused, failed with Retry",
    render: () => <div className="catalog-stack">
      <ScheduleCard time="09:00" day="Daily" title="Morning summary" meta="Daily 09:00 · run 12" statusLabel="12:31" />
      <ScheduleCard state="run" time="14:00" day="Mon" title="Weekly price check" meta="Mondays 14:00 · run 3" statusLabel="Running · 14s" />
      <ScheduleCard state="paused" time="08:00" day="Daily" title="Inbox sweep" meta="Daily 08:00 · run 7" statusLabel="Paused" />
      <ScheduleCard state="error" time="07:30" day="Daily" title="Backup check" meta="Daily 07:30 · run 40" statusLabel="Failed" error="The server did not answer in time." onRetry={noop} />
    </div> },
  { id: "sched-open", kind: "inline", label: "ScheduleCard · task text open (click the row to fold it)",
    render: () => <ScheduleCard open time="09:00" day="Daily" title="Morning summary" meta="Daily 09:00 · run 12" statusLabel="12:31" bodyLabel="TASK">Read the new mail, list what needs a reply and post the short version to the team.</ScheduleCard> },
  { id: "code", kind: "inline", label: "CodeBlock · short, with file name and download (pinned)", note: "Five token kinds: keyword, string, number, function, comment.",
    render: () => <CodeBlock filename="sync.py" lang="python" lines={PY.slice(0, 11)} showActions onDownload={noop} /> },
  { id: "code-long", kind: "inline", label: "CodeBlock · long code folds after 16 lines; copied", note: "Press the fold button; copy turns into a tick for a moment.",
    render: () => <CodeBlock filename="sync.py" lang="python" lines={[...PY, "", "if __name__ == \"__main__\":", "    main()"]} onDownload={noop} /> },
  { id: "code-copied", kind: "inline", label: "CodeBlock · copied, and a long line that scrolls sideways",
    render: () => <CodeBlock lang="bash" copied lines={["$ curl -s https://api.example.com/v1/reports?status=open&since=2026-11-01&limit=500&fields=id,amount,status,owner | jq '.items | length'", "42"]} /> },
];
