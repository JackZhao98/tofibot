// Static-kit parity with the frozen V3.0 spec (V3 PLAN 0.6, 7.7). LOCAL: it needs the spec folder, which is not in the repo.
//   V3_SPEC=/path/to/docs/design/ds-v3-frozen-2026-11-17 PLAYWRIGHT_MODULE=/abs/playwright/index.mjs npm run test:ds-parity
// Each case is rendered twice to static HTML: by the spec's bundle (project/components/bundle.js, React 19 standing in for its
// React 18 global) and by the kit (src/ds, through Vite SSR). Both go in a Chromium page with their own CSS (the spec's
// tokens.css + bundle.css; the V3 skin + the kit's sheets), in light and dark. The two trees are walked in parallel; the tag
// names and the computed layout and paint properties of every element must be equal, except the departures listed below.
import { readFileSync, readdirSync, statSync, existsSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRequire } from "node:module";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const spec = process.env.V3_SPEC;
// The spec folder is untracked, so CI and a fresh clone cannot read it: skip with a clear line instead of failing ci-local.
if (!spec || !existsSync(join(spec, "project", "components", "bundle.js"))) { console.log("ds-parity: SKIPPED, V3_SPEC is not set to the frozen spec folder (docs/design/ds-v3-frozen-2026-11-17); this check runs only where that folder exists"); process.exit(0); }
if (!process.env.PLAYWRIGHT_MODULE) { console.log("ds-parity: PLAYWRIGHT_MODULE is not set"); process.exit(2); }
const require = createRequire(join(ui, "package.json"));
const React = require("react");
const { renderToStaticMarkup } = require("react-dom/server");
globalThis.window = globalThis;
globalThis.React = React;
(0, eval)(readFileSync(join(spec, "project", "components", "bundle.js"), "utf8"));
const T = globalThis.TofiV3;

const D = (c, p = {}, k) => ({ c, p, k });
const input = D("TextInput", { placeholder: "Name", "aria-label": "Name" });
// Cases that exist in both: [name, description]. Callbacks are omitted; buttons that show only with a handler get a no-op by name.
const noop = "fn";
const CASES = [
  ...["primary", "secondary", "ghost", "danger"].flatMap(v => ["md", "sm"].map(s => [`Button ${v} ${s}`, D("Button", { variant: v, size: s }, "Save and restart")])),
  ["Button icons", D("Button", { variant: "primary", icon: "plus", iconAfter: "arrow-right" }, "Create key")],
  ["Button busy", D("Button", { busy: true }, "Saving")],
  ["Button disabled", D("Button", { variant: "primary", disabled: true }, "Save")],
  ...["ghost", "solid"].flatMap(v => ["md", "sm"].map(s => [`IconButton ${v} ${s}`, D("IconButton", { icon: "copy", label: "Copy", variant: v, size: s })])),
  ["IconButton filled", D("IconButton", { icon: "star", label: "Pin", filled: true })],
  ["Icon", D("Icon", { name: "key", size: 20 })],
  ["Icon filled", D("Icon", { name: "key", size: 24, variant: "filled" })],
  ...["neutral", "working", "needs", "done", "error", "unread", "info", "paused"].map(t => [`Badge ${t}`, D("Badge", { tone: t }, "Label")]),
  ["Tag", D("Tag", {}, "read")], ["Tag icon removable", D("Tag", { icon: "file", onRemove: noop, removeLabel: "Remove" }, "PDF")],
  ["Kbd", D("Kbd", {}, "Esc")],
  ...["info", "warning", "error", "done"].map(t => [`Banner ${t}`, D("Banner", { tone: t, title: "Update ready", action: D("Button", { size: "sm" }, "Restart") }, "Restart to use it.")]),
  ["Notice", D("Notice", { icon: "info", action: D("Button", { variant: "ghost", size: "sm" }, "Review") }, "Spend is at 80%.")],
  ...["neutral", "done", "error"].map(t => [`Toast ${t}`, D("Toast", { tone: t, action: "Retry", onAction: noop, onClose: noop, closeLabel: "Dismiss" }, "Saved")]),
  ["Progress", D("Progress", { value: 42, label: "Upload", showValue: true })],
  ["Progress warning", D("Progress", { value: 82, label: "Storage", tone: "warning" })],
  ["Progress danger", D("Progress", { value: 96, tone: "danger" })],
  ["Skeleton", D("Skeleton", { lines: 4, label: "Loading" })],
  ["EmptyState", D("EmptyState", { title: "No keys", icon: "key", action: D("Button", { variant: "primary" }, "Create key") }, "Keys let a service call Tofi.")],
  ...["solid", "raised", "glass", "quiet", "block"].map(v => [`Card ${v}`, D("Card", { variant: v, eyebrow: "Eyebrow", title: "Weekly report", actions: D("Button", { size: "sm", variant: "ghost" }, "Edit"), footer: D("Button", { size: "sm" }, "Open") }, "Body text.")]),
  ["Card flush", D("Card", { pad: false, title: "Flush" }, "Body.")],
  ["TextInput", D("TextInput", { icon: "search", suffix: "MB", defaultValue: "512", "aria-label": "Limit" })],
  ["TextInput mono", D("TextInput", { mono: true, defaultValue: "sk-tofi-9Fq2", "aria-label": "Key" })],
  ["TextArea", D("TextArea", { defaultValue: "Notes", "aria-label": "Notes" })],
  ["Field", D("Field", { label: "Name", optional: "Optional", hint: "One line." }, input)],
  ["Field error", D("Field", { label: "Port", error: "Use 1 to 65535." }, input)],
  ["Checkbox", D("Checkbox", { defaultChecked: false }, "Read")],
  ["Checkbox checked", D("Checkbox", { defaultChecked: true }, "Write")],
  ["Checkbox disabled", D("Checkbox", { disabled: true }, "Admin")],
  ["Switch off", D("Switch", { label: "Sound" })],
  ["Switch on", D("Switch", { label: "Sound", defaultChecked: true })],
  ["Switch disabled", D("Switch", { label: "Sound", defaultChecked: true, disabled: true })],
  ["Segmented", D("Segmented", { label: "View", options: ["List", "Board", "Week"], defaultValue: "Board" })],
  ["Segmented icons", D("Segmented", { label: "Theme", options: [{ value: "light", label: "Light", icon: "sun" }, { value: "dark", label: "Dark", icon: "moon" }], defaultValue: "dark" })],
  ["Tabs", D("Tabs", { tabs: [{ value: "keys", label: "Keys", count: 4 }, { value: "usage", label: "Usage" }], defaultValue: "keys" }, "Keys view.")],
  ...["mustard", "teal", "grape", "cobalt", "lime", "tomato"].map(t => [`Avatar ${t}`, D("Avatar", { name: "Mia Chen", tone: t })]),
  ["Avatar presence", D("Avatar", { name: "Mia Chen", status: "online", size: 48 })],
  ...["tomato", "mustard", "teal", "grape", "lime", "paper"].map(c => [`BotAvatar ${c}`, D("BotAvatar", { name: "Mochi", coat: c, src: "/brand/tofi-logo-calico.svg" })]),
  ...["thinking", "working", "waiting", "error", "asleep"].map(s => [`BotAvatar ${s}`, D("BotAvatar", { name: "Mochi", coat: "teal", state: s, src: "/brand/tofi-logo-calico.svg" })]),
  ["BotAvatar glyph", D("BotAvatar", { name: "Mochi", coat: "grape" })],
  ["BrandMark", D("BrandMark", { src: "/brand/services/notion.svg", name: "Notion" })],
  ["BrandMark dark", D("BrandMark", { src: "/brand/services/github-inverse.svg", tile: "dark", size: 48 })],
  ["MemberChip", D("MemberChip", { name: "Mia Chen", role: "Owner", onRemove: noop, removeLabel: "Remove Mia", avatar: D("Avatar", { name: "Mia Chen", size: 28, tone: "cobalt" }) })],
  // Kit IV (PR 0.9): settings and data.
  ["SettingsSection", D("SettingsSection", { title: "Appearance", description: "How Tofi looks on this device.", actions: D("Button", { size: "sm" }, "Reset") }, [D("SettingsRow", { label: "Theme", hint: "Follows the system by default." }, D("Button", { variant: "secondary", size: "sm" }, "Change")), D("SettingsRow", { label: "Language" }, D("Tag", {}, "English"))])],
  ["SettingsSection danger", D("SettingsSection", { title: "Danger zone", danger: true }, D("SettingsRow", { label: "Start over", hint: "Deletes every Bot, conversation and file on this server." }, D("Button", { variant: "danger", size: "sm" }, "Start over")))],
  ["KeyRow icon", D("KeyRow", { name: "production-bot", scope: "read", masked: "sk-tofi-…9Fq2", meta: "Used 2 hours ago", actions: D("IconButton", { icon: "more", label: "Key actions", size: "sm" }) })],
  ["KeyRow logo status", D("KeyRow", { name: "OpenAI", logo: "/brand/services/notion.svg", masked: "sk-proj-…Tt0a", meta: "Added Oct 21", status: { tone: "error", label: "Refused" }, actions: D("IconButton", { icon: "more", label: "Key actions", size: "sm" }) })],
  ["KeyRow logo dark", D("KeyRow", { name: "GitHub", logo: "/brand/services/github-inverse.svg", logoTile: "dark", masked: "ghp_…4xYz", meta: "Added Nov 2" })],
  ["KeyRow revoked", D("KeyRow", { name: "old-ci", masked: "sk-tofi-…aa01", meta: "Revoked Oct 3", revoked: true })],
  ["KeyRow revoked logo", D("KeyRow", { name: "Notion", logo: "/brand/services/notion.svg", masked: "secret_…a1b2", meta: "Revoked Oct 3", revoked: true })],
  ["DataTable", D("DataTable", { columns: [{ key: "name", label: "Member" }, { key: "role", label: "Role" }, { key: "spend", label: "Spend", align: "right", mono: true }, { key: "state", label: "State", render: v => v === "Active" ? null : React.createElement("span", null, String(v)) }], rows: [{ id: 1, name: "Lin Park", role: "Admin", spend: "$42.18", state: "Active" }, { id: 2, name: "Lee Park", role: "Member", spend: "$0.00", state: "Paused" }] })],
  ["DataTable logoKey", D("DataTable", { columns: [{ key: "vendor", label: "Provider", logoKey: "logo" }, { key: "calls", label: "Calls", align: "right", mono: true, width: 90 }], rows: [{ id: "a", vendor: "Notion", logo: "/brand/services/notion.svg", calls: "1,284" }, { id: "b", vendor: "GitHub", logo: "/brand/services/github-monochrome.svg", calls: "97" }] })],
  ["DataTable empty", D("DataTable", { columns: [{ key: "name", label: "Member" }, { key: "role", label: "Role" }], rows: [], empty: "No members yet." })],
  ["Stat", D("Stat", { label: "Spend this month", value: "$42.18", delta: "21% of limit", spark: [20, 35, 30, 50, 45, 70, 60] })],
  ["Stat up", D("Stat", { label: "Runs", value: "1,284", delta: "+12% vs last month", deltaTone: "up", spark: [40, 42, 55, 48, 62, 58, 80] })],
  ["Stat down unit", D("Stat", { label: "Failed", value: "7", unit: "runs", delta: "+3", deltaTone: "down" })],
  // ---- Kit III: files and rich objects (V3 PLAN 0.8). Labels are passed where the spec's default is Chinese.
  ["FileTile compact", D("FileTile", { kind: "archive", ext: "zip", name: "firmware-2.4.zip", meta: "7.9 MB", onDownload: noop })],
  ["FileTile compact no action", D("FileTile", { kind: "doc", ext: "docx", name: "Old contract.docx", meta: "Expired" })],
  ["FileTile table", D("FileTile", { kind: "sheet", ext: "csv", name: "Prices.csv", meta: "85 rows", onOpen: noop, onDownload: noop, preview: { type: "table", rows: [["City", "Country", "Price"], ["Tokyo", "Japan", "4,800"], ["Berlin", "Germany", "39"]] } })],
  ["FileTile md", D("FileTile", { kind: "md", ext: "md", name: "Notes.md", meta: "2 KB", onOpen: noop, preview: { type: "md", lines: ["Weekly sync", "Price table moves.", "", "Lin owns Europe."] } })],
  ["FileTile code", D("FileTile", { kind: "code", ext: "py", name: "sync.py", onOpen: noop, onDownload: noop, preview: { type: "code", lines: ["import json", "def load(folder):", "    return []"] } })],
  ["FileTile audio", D("FileTile", { kind: "audio", ext: "mp3", name: "Voice note.mp3", meta: "0:42", onOpen: noop, preview: { type: "audio", label: "0:42" } })],
  ["FileTile pdf", D("FileTile", { kind: "pdf", name: "Report.pdf", meta: "3 pages", onOpen: noop, onDownload: noop, preview: { type: "pdf", src: "/catalog/report-page.svg", label: "3 pages" } })],
  ["FileTile video", D("FileTile", { kind: "video", ext: "mov", name: "Recording.mov", onOpen: noop, preview: { type: "video", src: "/catalog/lake.svg", label: "0:42" } })],
  ...["pdf", "doc", "sheet", "slides", "code", "md", "archive", "text"].map(kind => [`FileRow ${kind}`, D("FileRow", { kind, name: "Weekly report", meta: "3 pages", onOpen: noop })]),
  ["FileRow thumb", D("FileRow", { thumb: "/catalog/sunset.svg", name: "Homepage.png", meta: "1600 x 1000", onOpen: noop })],
  ["FileRow fresh", D("FileRow", { kind: "pdf", name: "Weekly report.pdf", meta: "just now", fresh: true, freshLabel: "New", onOpen: noop })],
  ["ArtifactCard preview", D("ArtifactCard", { kind: "pdf", name: "Report.pdf", meta: "3 pages", tag: "ARTIFACT", version: "v3", preview: { type: "pdf", src: "/catalog/report-page.svg" }, onOpen: noop, onDownload: noop })],
  ["ArtifactCard blank", D("ArtifactCard", { kind: "archive", ext: "zip", name: "Assets.zip", meta: "48 MB", tag: "ARTIFACT", onOpen: noop, onDownload: noop })],
  ["ArtifactCard wide from", D("ArtifactCard", { wide: true, kind: "page", ext: "html", name: "Compare.html", tag: "ARTIFACT", preview: { type: "page", src: "/catalog/web-page.svg" }, from: "Probe", onGoto: noop, gotoLabel: "Go to message", onOpen: noop })],
  ["AttachmentGrid 1 sized", D("AttachmentGrid", { items: [{ src: "/catalog/sunset.svg", width: 1600, height: 1000 }], onOpen: noop })],
  ["AttachmentGrid 1 portrait", D("AttachmentGrid", { items: [{ src: "/catalog/valley.svg", width: 900, height: 1200 }], onOpen: noop })],
  ["AttachmentGrid 2", D("AttachmentGrid", { items: ["/catalog/field.svg", "/catalog/night.svg"], onOpen: noop })],
  ["AttachmentGrid 3 video", D("AttachmentGrid", { items: ["/catalog/field.svg", { src: "/catalog/lake.svg", kind: "video", duration: "0:42" }, "/catalog/night.svg"], onOpen: noop })],
  ["AttachmentGrid 4", D("AttachmentGrid", { items: ["/catalog/field.svg", "/catalog/night.svg", "/catalog/lake.svg", "/catalog/desert.svg"], onOpen: noop })],
  ["AttachmentGrid 9", D("AttachmentGrid", { items: Array(9).fill("/catalog/field.svg"), onOpen: noop })],
  ["Letter in", D("Letter", { from: "Lin Wei", to: "you@tofi.example", subject: "Q3 prices", subjectLabel: "SUBJECT", postmark: ["Received", "09:12", "11.17"], note: { avatar: "M", title: "Mochi's read", text: "Needs your yes." }, folded: true, more: "Show full mail", onMore: noop, actions: D("Button", { size: "sm", variant: "secondary" }, "Reply"), hint: "Drafted", primary: D("Button", { size: "sm", variant: "primary" }, "Open") }, "Hi, the Europe column changes on Monday. Please confirm.")],
  ["Letter in plain", D("Letter", { from: "Lin Wei", to: "you@tofi.example", subject: "Lunch?", subjectLabel: "SUBJECT" }, "Are you free on Friday?")],
  ["Letter out", D("Letter", { direction: "out", to: "Lin Wei", cc: "Mia Chen", subject: "Re: Q3 prices", subjectLabel: "SUBJECT", stamp: "cat", sign: "Mochi, for Mia", attachment: "Prices.csv", actions: D("Button", { size: "sm", variant: "ghost" }, "Edit"), hint: "Not sent yet", primary: D("Button", { size: "sm", variant: "primary" }, "Send") }, "Monday works.")],
  ["InboxDigest", D("InboxDigest", { label: "Sorted mail", items: [{ from: "Lin Wei", subject: "Q3 price table", tag: { label: "Needs reply", tone: "tomato" }, time: "09:12", lifted: true }, { from: "Billing", subject: "Invoice 1042", tag: { label: "Bill" }, time: "08:40" }, { from: "Calendar", subject: "Sync moved", tag: { label: "Notice", tone: "quiet" }, time: "08:02" }] })],
  ["DraftCard message", D("DraftCard", { kind: "Draft", title: "Post to the team", dest: "Posts to Launch group", actions: D("Button", { size: "sm", variant: "ghost" }, "Edit"), hint: "Not posted yet", primary: D("Button", { size: "sm", variant: "primary" }, "Send") }, "Price table moves to Monday.")],
  ["DraftCard form", D("DraftCard", { kind: "Needs you", title: "Expense claim", dest: "Company expenses", changedLabel: "Edited", fields: [["Amount", "4,200", true], ["Date", "11.16"]], primary: D("Button", { size: "sm", variant: "primary" }, "Submit") })],
  ["DraftCard sent", D("DraftCard", { state: "sent", sentLabel: "Sent", receipt: "Posted at 14:20", after: D("Button", { size: "sm", variant: "ghost" }, "View") }, "Price table moves to Monday. Berlin and Paris switch to the new list; London stays on the old one until the thirtieth.")],
  ["DraftCard canceled", D("DraftCard", { state: "canceled", canceledLabel: "Canceled", receipt: "Canceled at 14:21", after: D("Button", { size: "sm", variant: "ghost" }, "Restore") }, "Price table moves to Monday. Berlin and Paris switch to the new list.")],
  ...["done", "run", "error"].map(state => [`ScheduleCard ${state}`, D("ScheduleCard", { state, time: "09:00", day: "Daily", title: "Morning summary", meta: "Daily 09:00 - run 12", statusLabel: state === "error" ? "Failed" : "12:31", bodyLabel: "TASK", ...(state === "error" ? { error: "No answer in time.", onRetry: noop, retryLabel: "Retry" } : {}) }, "Read the new mail and post a summary.")],),
  ["CodeBlock short", D("CodeBlock", { filename: "sync.py", lang: "python", lines: ["import json", "# note", "def load(path):", "    return json.loads(\"{}\") + 1.5", "", "if x and not y: print('a')"], copyLabel: "Copy", copiedLabel: "Copied", linesLabel: "{n} lines" })],
  ["CodeBlock folded", D("CodeBlock", { lines: ["const a = 1", "const b = 2", "const c = 3", "const d = 4", "const e = 5"], maxLines: 3, linesLabel: "{n} lines", moreLabel: "Show the other {n} lines", copyLabel: "Copy" })],
  ["CodeBlock actions", D("CodeBlock", { lines: ["echo hi"], showActions: true, onDownload: noop, downloadLabel: "Download", copyLabel: "Copy", linesLabel: "{n} lines" })],
  ["CodeBlock copied", D("CodeBlock", { lines: ["echo hi"], copied: true, copiedLabel: "Copied", linesLabel: "{n} lines" })],
];

// Known departures, as [case name pattern, property]. Each is explained in the PR 0.6 report.
const ALLOWED = [
  [/^TextArea/, "gap"], [/^TextArea/, "align-items"],  // flex properties the spec's shared rule leaves on a block element: no effect
  // AttachmentGrid: the cell button inherits the text colour; the spec leaves the browser's default black (found by test:skin-sensitivity).
  ...["color", "border-top-color", "border-right-color", "border-bottom-color", "border-left-color", "outline-color", "caret-color", "column-rule-color", "text-decoration-color", "text-emphasis-color", "-webkit-text-fill-color", "-webkit-text-stroke-color"].map(property => [/^AttachmentGrid/, property]),
  // Card block: the kit reads the eyebrow, title, ghost action and footer rule in --on-block (pale on mustard in Night in the spec).
  ...["color", "border-top-color", "border-right-color", "border-bottom-color", "border-left-color"].map(property => [/^Card block/, property]),
  [/^Skeleton/, "opacity"],          // a running animation: sampled at different times
  [/^Checkbox/, "position"],           // the kit anchors the hidden input to its label
  [/^Checkbox/, "margin-top"], [/^Checkbox/, "margin-bottom"],  // the kit zeroes the hidden input's margin (V2 base gives inputs one)
  // Kit III: the Letter note title was a literal brown (#6d5a2a); the kit reads --mustard-text (no raw colours outside the skin).
  ...["color", "border-top-color", "border-right-color", "border-bottom-color", "border-left-color"].map(property => [/^Letter in$/, property]),
  [/^DraftCard form/, "width"],
  // DraftCard pending: the footer stops inside the bold edge (margin 2px, padding 10px), so it is 4px narrower.
  ...["width", "padding-left", "padding-right"].map(property => [/^DraftCard (message|form)$/, property]),  // the kit's label column is 96px (spec 76px): "Category" plus the "Edited" mark does not fit in 76px
  [/^ScheduleCard run/, "opacity"], [/^ScheduleCard run/, "transform"],  // the running dot breathes: sampled at different times
  // Letter in: the sender's round mark is centred on the name's line (vertical-align middle, 1px up); the spec drops it 5px below.
  [/^Letter in/, "position"], [/^Letter in/, "height"],  // the FROM row is 4px shorter (22px, not 26px)
  [/^CodeBlock copied/, "animation-name"],  // the kit's copy glyph spins into the tick (owner exception, PLAN 8.3); the spec swaps at once
];

// ---- Kit II: conversation and work (PR 0.7)
const kitII = (name, c, p, k) => [name, D(c, p, k)];
CASES.push(
  kitII("SidebarRow", "SidebarRow", { icon: "chat", label: "All conversations" }),
  kitII("SidebarRow active", "SidebarRow", { icon: "chat", label: "Tasks", active: true }),
  kitII("SidebarRow meta count", "SidebarRow", { avatar: D("Avatar", { name: "Mia Chen", size: 34, tone: "teal" }), label: "Mia", meta: "Sent the report", unread: true, count: 3 }),
  kitII("SidebarRow status", "SidebarRow", { avatar: D("Avatar", { name: "Mia Chen", size: 34, tone: "teal" }), label: "Mia", meta: "Working", status: "needs" }),
  kitII("Divider date", "Divider", {}, "Today · 14:20"),
  kitII("Divider unread", "Divider", { tone: "unread" }, "3 new"),
  kitII("ProgressiveBlur bottom", "ProgressiveBlur", {}),
  kitII("ProgressiveBlur top", "ProgressiveBlur", { edge: "top", height: 48 }),
  kitII("JumpButton", "JumpButton", { label: "Back to latest", onClick: noop }),
  kitII("LiveLine", "LiveLine", { elapsed: "0:14" }, "Thinking"),
  kitII("LiveLine avatar", "LiveLine", { avatar: D("Avatar", { name: "Mia Chen", size: 20 }) }, "Finishing"),
  ...["mustard", "tomato", "teal", "grape", "lime"].map(tone => kitII(`Mention ${tone}`, "Mention", { name: "Mochi", tone })),
  kitII("Mention fresh", "Mention", { name: "Mochi", fresh: true }),
  kitII("MessageBubble bot", "MessageBubble", { role: "bot", author: "Mochi", botColor: "grape", avatar: D("Avatar", { name: "Mochi", size: 34, tone: "grape" }) }, "Two things need a decision."),
  kitII("MessageBubble user", "MessageBubble", { role: "user" }, "Which two?"),
  kitII("MessageBubble run", "MessageBubble", { role: "bot", author: "Mochi", joined: true, continued: true }, "In the middle of a run."),
  kitII("MessageBubble quote", "MessageBubble", { role: "user", quote: { author: "Mochi", text: "Two things need a decision.", tone: "tomato" } }, "Which two?"),
  kitII("MessageBubble sending", "MessageBubble", { role: "user", sending: true }, "Going out."),
  kitII("Reactions", "Reactions", { items: [{ emoji: "👍", count: 3, who: "Mia and 2 more", onToggle: noop }, { emoji: "🎉", count: 1, mine: true, onToggle: noop }] }),
  kitII("TaskCard", "TaskCard", { eyebrow: "Needs you", title: "Send the report", status: { tone: "needs", label: "Waiting" }, items: [{ text: "Notes", state: "done" }, { text: "Screenshot", state: "failed" }, { text: "Layout", state: "done" }, { text: "Recipient", state: "waiting" }, { text: "Send", state: "open" }], meta: "Started 14:02", actions: D("Button", { size: "sm" }, "Confirm") }),
  kitII("TaskCard paused", "TaskCard", { paused: true, title: "Back up the photos", status: { tone: "paused", label: "Paused" }, items: [{ text: "Copy 2019", state: "done" }] }),
  kitII("ApprovalCard", "ApprovalCard", { title: "Delete 14 files?", facts: [["Folder", "/Users/mia/Downloads", true], ["Files", "14"]], rememberLabel: "Always", onApprove: noop, onDecline: noop }, "I already filed these."),
  kitII("ApprovalCard resolved", "ApprovalCard", { title: "Delete 14 files?", resolved: { label: "APPROVED", reason: "Moved to Trash" } }, "I already filed these."),
  kitII("ApprovalCard declined", "ApprovalCard", { title: "Delete 14 files?", resolved: { label: "DECLINED", reason: "You said no", declined: true, fresh: true } }),
  // S18 to S20 drawings replace these three: only checked that the kit renders them (see DEPARTURES).
  kitII("ProcessLine", "ProcessLine", { label: "This round", meta: "1:12", defaultOpen: true }, D("ToolSteps", { steps: [{ tool: "read_file", arg: "notes.md", dur: "0.4s" }, { tool: "shell", arg: "ls", state: "bad", note: "failed" }] })),
  kitII("ToolSteps", "ToolSteps", { steps: [{ tool: "browser", arg: "page", steps: [{ tool: "navigate", arg: "x" }] }] }),
  kitII("StepList", "StepList", { botColor: "grape", title: "Weekly report", stamp: "11.17", steps: [{ text: "a", state: "done" }, { text: "b", state: "failed", error: { reason: "no", onRetry: noop } }, { text: "c", state: "active" }, { text: "d" }] }),
);
// The live mark spins: its rotated box is sampled at different moments on the two sides.
for (const property of ["width", "height", "transform"]) ALLOWED.push([/^LiveLine/, property]);
// Components whose design was changed after the freeze: the DOM and the look differ on purpose, so the case is rendered by the kit
// (it must not throw) and listed, not compared. Each names its decision in docs/design/v3-refactor/PLAN.md section 2.
const DEPARTURES = [
  [/^ProcessLine$|^ToolSteps$/, "S18", "the work log is an indented ruled scratch pad: no pills, no status marks, only a failed row gets a cross"],
  [/^StepList$/, "S20", "the plan is a bullet-journal page: washi tape, date stamp, signifier column, no boxes and no strike-through"],
];
const departure = name => DEPARTURES.find(([pattern]) => pattern.test(name));
// TaskCard checklist marks are the A set (S19): the artwork is a mask image, which this test does not compare; the rest must match.
console.log("departure S19: TaskCard: the checklist tick and cross are the A marker set (mask artwork, not compared); every other property is compared");

// ---- render both sides
const { createServer } = await import("vite");
const { default: react } = await import("@vitejs/plugin-react");
const server = await createServer({ configFile: false, mode: "development", root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0 }, appType: "custom", logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const { registry } = await server.ssrLoadModule("/scripts/ds-parity-registry.tsx");
const fn = () => {};
const make = (components, desc) => {
  if (typeof desc === "string" || desc == null) return desc;
  const props = Object.fromEntries(Object.entries(desc.p).map(([key, value]) => [key, value === noop ? fn : value && typeof value === "object" && value.c ? make(components, value) : value]));
  const kids = Array.isArray(desc.k) ? desc.k.map(k => make(components, k)) : [make(components, desc.k)];
  return React.createElement(components[desc.c], props, ...kids.filter(k => k != null));
};
const specHtml = [], kitHtml = [];
for (const [name, desc] of CASES) {
  specHtml.push(renderToStaticMarkup(make(T, desc)));
  kitHtml.push(renderToStaticMarkup(make(registry, desc)));
}

// ---- the kit's sheets: every ds/*.css, in a stable order
const walk = dir => readdirSync(dir).flatMap(name => { const path = join(dir, name); return statSync(path).isDirectory() ? walk(path) : [path]; });
const kitSheets = walk(join(ui, "src", "ds")).filter(file => file.endsWith(".css")).sort().map(file => "/" + relative(ui, file));
const specCss = ["tokens.css", "components/bundle.css"].map(file => readFileSync(join(spec, "project", file), "utf8").replace(/@import url\([^)]*\);?/g, "")).join("\n");

const PROPS = ["display", "position", "color", "background-color", "background-image", "border-top-color", "border-right-color", "border-bottom-color", "border-left-color",
  "border-top-width", "border-right-width", "border-bottom-width", "border-left-width", "border-top-style", "border-top-left-radius", "border-bottom-right-radius", "box-shadow", "opacity",
  "font-family", "font-size", "font-weight", "line-height", "letter-spacing", "text-transform", "text-align", "padding-top", "padding-right", "padding-bottom", "padding-left",
  "margin-top", "margin-bottom", "gap", "width", "height", "flex-grow", "flex-shrink", "align-items", "justify-content", "transform", "filter", "animation-name", "outline-style"];

const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE).href);
const browser = await chromium.launch({ headless: true });
const failures = [];
try {
  for (const theme of ["light", "dark"]) {
    const page = await browser.newPage({ viewport: { width: 900, height: 900 } });
    await page.route("**/*", route => route.request().url().startsWith(origin) ? route.continue() : route.abort());
    const html = side => `<!doctype html><html data-theme="${theme}" ${side === "kit" ? 'data-skin="v3"' : ""} style="color-scheme:${theme}"><head><meta charset="utf-8">${side === "spec" ? `<style>${specCss}</style>` : ["/src/skin/structure.css", "/src/skin/v3.css", "/src/skin/roles.css", ...kitSheets].map(href => `<link rel="stylesheet" href="${href}">`).join("")}</head>
      ${side === "kit" ? "<style>*,*::before,*::after{box-sizing:border-box}body{font:400 15px/1.6 var(--font-body);color:var(--ink)}</style>" : ""}
      <body style="margin:0;background:var(--bg)">${(side === "spec" ? specHtml : kitHtml).map((markup, i) => `<div class="case" data-i="${i}" style="padding:10px;width:520px">${markup}</div>`).join("")}</body></html>`;
    const measure = async side => {
      await page.route(`${origin}/__${side}`, route => route.fulfill({ contentType: "text/html", body: html(side) }));
      await page.goto(`${origin}/__${side}`);
      await page.waitForLoadState("networkidle");
      // The product's TofiIcon wraps the drawing in <g> groups (motion hooks); they draw nothing, so they are skipped. Radix adds a
      // hidden form input beside a Switch or radio in a server render (aria-hidden, not in the browser tree outside a form): skipped too.
      return page.evaluate(props => [...document.querySelectorAll(".case")].map(box => [...box.querySelectorAll('*:not(g, input[aria-hidden="true"])')].map(el => {
        const style = getComputedStyle(el);
        const rect = el.getBoundingClientRect();
        return { tag: el.tagName.toLowerCase(), values: { ...Object.fromEntries(props.map(p => [p, style.getPropertyValue(p)])), width: `${Math.round(rect.width)}`, height: `${Math.round(rect.height)}` } };
      })), PROPS);
    };
    const [a, b] = [await measure("spec"), await measure("kit")];
    CASES.forEach(([name], i) => {
      if (departure(name)) return;
      const left = a[i], right = b[i];
      if (left.length !== right.length || left.some((el, j) => el.tag !== right[j].tag)) { failures.push(`${theme} ${name}: element trees differ (${left.map(e => e.tag).join(" ")} vs ${right.map(e => e.tag).join(" ")})`); return; }
      left.forEach((el, j) => {
        for (const prop of PROPS) {
          if (el.values[prop] === right[j].values[prop]) continue;
          if (ALLOWED.some(([pattern, property]) => pattern.test(name) && property === prop)) continue;
          failures.push(`${theme} ${name}: <${el.tag}> #${j} ${prop}: spec ${el.values[prop]} vs kit ${right[j].values[prop]}`);
        }
      });
    });
    await page.close();
  }
} finally { await browser.close(); await server.close(); }
for (const failure of failures.slice(0, 80)) console.log(`FAIL ${failure}`);
if (failures.length) { console.log(`ds-parity: ${failures.length} difference(s) over ${CASES.length} cases`); process.exit(1); }
for (const [pattern, id, why] of DEPARTURES) console.log(`departure ${id}: ${CASES.filter(([name]) => pattern.test(name)).map(([name]) => name).join(", ")}: ${why}`);
console.log(`ds-parity: PASS (${CASES.length} cases, light and dark; ${CASES.filter(([name]) => departure(name)).length} listed departures, not compared)`);
