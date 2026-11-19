// V3 kit, conversation and work (PR 0.7): SidebarRow, Divider, ProgressiveBlur, JumpButton, LiveLine, Mention, Reactions, ProcessLine,
// ToolSteps, StepList, TaskCard, ApprovalCard. The work log, the plan and the checklist marks follow S18 to S20, not the frozen spec.
import { ApprovalCard } from "../../ds/ApprovalCard/ApprovalCard";
import { Avatar } from "../../ds/Avatar/Avatar";
import { BotAvatar, type BotAvatarState } from "../../ds/BotAvatar/BotAvatar";
import { Button } from "../../ds/Button/Button";
import { ChatHeader, type ChatHeaderPosition } from "../../ds/ChatHeader/ChatHeader";
import { IconButton } from "../../ds/IconButton/IconButton";
import { Divider } from "../../ds/Divider/Divider";
import { JumpButton } from "../../ds/JumpButton/JumpButton";
import { Notice } from "../../ds/Notice/Notice";
import { LiveLine } from "../../ds/LiveLine/LiveLine";
import { Mention, type MentionTone } from "../../ds/Mention/Mention";
import { ProcessLine, ProcessPad } from "../../ds/ProcessLine/ProcessLine";
import { ProgressiveBlur } from "../../ds/ProgressiveBlur/ProgressiveBlur";
import { Reactions } from "../../ds/Reactions/Reactions";
import { SidebarRow } from "../../ds/SidebarRow/SidebarRow";
import { StepList } from "../../ds/StepList/StepList";
import { TaskCard } from "../../ds/TaskCard/TaskCard";
import { ToolSteps, type ToolStepData } from "../../ds/ToolSteps/ToolSteps";
import type { CatalogState } from "../types";
import { Dots, Row, Specimen } from "./v3-parts";

const MENTIONS: MentionTone[] = ["mustard", "tomato", "teal", "grape", "lime", "you"];
const noop = () => {};

const ROUND: ToolStepData[] = [
  { tool: "read_file", arg: "notes/week-46.md", dur: "0.4s" },
  { tool: "browser", arg: "Screenshot the home page", dur: "3.2s", steps: [
    { tool: "navigate", arg: "tofi.example", dur: "2.3s" },
    { tool: "screenshot", arg: "Full page · 1600 wide", dur: "0.9s", state: "bad", note: "302 to /login, no page came back" },
  ] },
  { tool: "pandoc", arg: "week-46.md → PDF", dur: "4.2s", steps: [{ tool: "count_pages", arg: "3 pages", dur: "0.1s" }] },
  { tool: "mail.send", arg: "To Lin · Report-week-46.pdf", dur: "0:12", state: "run" },
];
const CONTEXT: ToolStepData[] = [
  { tool: "file", arg: "notes/week-46.md" },
  { tool: "memory", arg: "Write the weekly report in three parts: this week, problems, next week." },
  { tool: "memory", arg: "Screenshots default to 1600 wide." },
];
const SIDEBAR: React.CSSProperties = { width: 300, padding: 8, background: "var(--c-sidebar-bg)", borderRadius: "var(--radius-md)", display: "grid", gap: 2 };

export const conversation: CatalogState[] = [
  { id: "sidebarrow", kind: "inline", label: "SidebarRow · icon, avatar and meta, active, unread, count, status",
    note: "The selected row keeps its ink edge. Under the Mac material (html[data-desktop=darwin]) the sidebar ground turns translucent; the web shows it plain.",
    render: () => <div style={SIDEBAR}>
      <SidebarRow icon="chat" label="All conversations" />
      <SidebarRow icon="chat" label="Tasks" active />
      <SidebarRow avatar={<Avatar name="Mochi" tone="grape" size={34} />} label="Mochi" meta="Sent the weekly report to Lin" />
      <SidebarRow avatar={<Avatar name="Pip" tone="teal" size={34} />} label="Pip" meta="Needs a decision" unread count={3} />
      <SidebarRow avatar={<Avatar name="Rye" tone="mustard" size={34} />} label="Rye" meta="Working on the import" status="working" />
      <SidebarRow avatar={<Avatar name="Moss" tone="lime" size={34} />} label="Moss, with a very long conversation title that has to be cut" meta="A long last message that also has to be cut at the edge of the row" active />
    </div> },
  { id: "divider", kind: "inline", label: "Divider · date, unread line",
    render: () => <div className="catalog-stack"><Divider>Today · 14:20</Divider><Divider tone="unread">3 new</Divider></div> },
  { id: "notice-lines", kind: "inline", label: "Notice · text only, one action, the sentence as the action, a failed read",
    note: "At most one action. Spend reminders, handovers, a command that ended, a read that failed. Never a run's error.",
    render: () => <div className="catalog-stack">
      <Notice icon="retry">Resuming from the last checkpoint.</Notice>
      <Notice icon="alert" action={<Button variant="ghost" size="sm" onClick={noop}>Open usage</Button>}>You have used $52.40 of your $60 monthly limit.</Notice>
      <Notice icon="bot-handoff"><button type="button" className="tb-notice-link" onClick={noop}>Mochi handed this over to Pip</button></Notice>
      <Notice icon="history" action={<Button variant="ghost" size="sm" onClick={noop}>View earlier messages</Button>} />
      <Notice tone="danger" icon="alert" action={<Button variant="ghost" size="sm" onClick={noop}>Retry</Button>}>Couldn't load earlier messages: the server is not answering.</Notice>
    </div> },
  { id: "progressive-blur", kind: "inline", label: "ProgressiveBlur · bottom and top bands over scrolling text",
    note: "Two layers fade out from the edge; the text stays visible, blurred, as it passes under.",
    render: () => <Dots><div className="catalog-stack" style={{ position: "relative", height: 190, overflow: "hidden" }}>
      {Array.from({ length: 12 }, (_, i) => <p key={i} style={{ margin: "0 0 6px" }}>Line {i + 1}: messages pass under the band and stay readable.</p>)}
      <ProgressiveBlur edge="top" height={48} />
      <ProgressiveBlur />
    </div></Dots> },
  { id: "jump", kind: "inline", label: "JumpButton · on the notebook ground",
    render: () => <Dots><Row><Specimen label="default"><JumpButton label="Back to latest" onClick={noop} /></Specimen></Row></Dots> },
  { id: "liveline", kind: "inline", label: "LiveLine · state, elapsed, with an avatar",
    render: () => <div className="catalog-stack">
      <LiveLine>Thinking</LiveLine>
      <LiveLine elapsed="0:14">Waiting to start</LiveLine>
      <LiveLine elapsed="1:02" avatar={<Avatar name="Mochi" tone="grape" size={20} />}>Finishing</LiveLine>
    </div> },
  { id: "mention", kind: "inline", label: "Mention · every colour, and one that just arrived",
    note: "Blue (you) is the person's. Bots take the five hues.",
    render: () => <div className="catalog-stack">
      <p style={{ margin: 0 }}>{MENTIONS.map(tone => <span key={tone}><Mention name={tone === "you" ? "Mia" : "Mochi"} tone={tone} /> </span>)}</p>
      <p style={{ margin: 0 }}>Can <Mention name="Mochi" tone="grape" fresh /> look at this before Friday?</p>
    </div> },
  { id: "reactions", kind: "inline", label: "Reactions · yours tinted, with who in the tooltip",
    render: () => <Row>
      <Specimen label="three chips"><Reactions items={[
        { emoji: "👍", count: 3, who: "Mia, Lin and 1 more", onToggle: noop },
        { emoji: "🎉", count: 1, mine: true, who: "You", onToggle: noop },
        { emoji: "👀", count: 12, onToggle: noop },
      ]} /></Specimen>
    </Row> },
  { id: "worklog", kind: "inline", label: "ProcessLine + ToolSteps · the work log (S18)",
    note: "A ruled scratch pad: indent carries the structure, only the title is bold, the only mark is the cross on a failed call. Fold the first row to see the nested calls fold.",
    render: () => <div className="catalog-stack"><ProcessPad>
      <ProcessLine label="This round · 14:20" meta="1:12" defaultOpen live><ToolSteps steps={ROUND} /></ProcessLine>
      <ProcessLine label="Context" meta="3 files · 2 memories" defaultOpen><ToolSteps steps={CONTEXT} /></ProcessLine>
      <ProcessLine label="Retries" meta="Screenshot · 1 time"><ToolSteps steps={[{ tool: "screenshot", arg: "Full page", dur: "0.9s" }]} /></ProcessLine>
      <ProcessLine label="Yesterday 18:02 · Meeting notes" meta="2:40" />
    </ProcessPad></div> },
  { id: "toolsteps-results", kind: "inline", label: "ToolSteps · output lines, three levels, a folded call",
    render: () => <div className="catalog-stack"><ProcessPad><ToolSteps steps={[
      { tool: "shell", arg: "ls notes", dur: "0.1s", result: "week-45.md\nweek-46.md" },
      { tool: "agent", arg: "Research the competitors", dur: "9.8s", open: false, steps: [{ tool: "search", arg: "calm task apps" }] },
      { tool: "agent", arg: "Draft the report", dur: "6.1s", steps: [{ tool: "write", arg: "report.md", steps: [{ tool: "format", arg: "headings", dur: "0.2s" }] }] },
      { tool: "wait", arg: "For Lin to answer", state: "wait" },
    ]} /></ProcessPad></div> },
  { id: "steplist", kind: "inline", label: "StepList · the bullet-journal plan (S20), in two Bot colours",
    note: "Washi tape and the date stamp take the Bot's colour; the current step sits under a highlighter band. Blue is never a Bot's.",
    render: () => <div className="catalog-stack" style={{ gap: 28, paddingTop: 14 }}>
      <StepList botColor="grape" stamp="11.17 Mon" title="Weekly report, week 46" steps={[
        { text: "Sum up the 3 meeting notes", state: "done" },
        { text: "Open the home page and take a screenshot", state: "failed", error: { reason: "The site wants a login; no page came back.", onRetry: noop, details: "HTTP 302 -> /login" } },
        { text: "Lay it out as a PDF", state: "done" },
        { text: "Send it to Lin", state: "active" },
        { text: "Wait for your answer", state: "pending" },
      ]} />
      <StepList botColor="teal" stamp="11.17 Mon" title="Tidy the downloads folder" steps={[
        { text: "List what is there", state: "done", fresh: true },
        { text: "Move the PDFs", state: "active" },
        { text: "Delete the duplicates", state: "waiting" },
      ]} />
    </div> },
  { id: "steplist-more", kind: "inline", label: "StepList · folded, one-step failure without a plan",
    render: () => <div className="catalog-stack" style={{ gap: 28, paddingTop: 14 }}>
      <StepList botColor="mustard" stamp="11.17 Mon" title="Weekly report, week 46" folded steps={[{ text: "a", state: "done" }, { text: "b" }]} />
      <StepList botColor="tomato" title="Open the page" steps={[{ text: "Open the home page", state: "failed", error: { reason: "The site is down.", onRetry: noop } }]} />
    </div> },
  { id: "taskcard", kind: "inline", label: "TaskCard · waiting for you, every item state",
    note: "The note waits for you: ink edge and hard shadow. The checklist uses the A marker set (S19).",
    render: () => <div className="catalog-stack">
      <TaskCard eyebrow="Needs you" title="Send the weekly report" status={{ tone: "needs", label: "Waiting" }}
        items={[{ text: "Sum up the notes", state: "done" }, { text: "Take the screenshot", state: "failed" }, { text: "Lay it out", state: "done", fresh: true }, { text: "Confirm the recipient", state: "waiting" }, { text: "Send", state: "open" }]}
        meta="Started 14:02 · Mochi" actions={<><Button variant="primary" size="sm">Confirm</Button><Button size="sm" variant="ghost">Not now</Button></>} />
    </div> },
  { id: "taskcard-paused", kind: "inline", label: "TaskCard · paused (a plain teal note, no edge)",
    render: () => <div className="catalog-stack">
      <TaskCard paused title="Back up the photos" status={{ tone: "paused", label: "Paused" }} items={[{ text: "Copy 2019", state: "done" }, { text: "Copy 2020" }]} meta="Paused at 14:20" actions={<Button size="sm">Resume</Button>} />
      <TaskCard title="No checklist" />
    </div> },
  { id: "approval", kind: "inline", label: "ApprovalCard · open (with and without Remember), approved, declined",
    note: "Facts come from the server, above the Bot's words. Open it carries the ink edge and hard shadow; resolved it settles.",
    render: () => <div className="catalog-stack" style={{ gap: 20 }}>
      <ApprovalCard title="Delete 14 files from Downloads?" facts={[["Folder", "/Users/mia/Downloads", true], ["Files", "14 · 212 MB"], ["Reversible", "Moved to Trash"]]} rememberLabel="Always for this folder" onApprove={noop} onRemember={noop} onDecline={noop}>
        These are the PDFs I already filed.
      </ApprovalCard>
      <ApprovalCard title="Send mail to Lin?" facts={[["To", "lin@example.com", true]]} onApprove={noop} onDecline={noop} />
      <ApprovalCard title="Delete 14 files from Downloads?" facts={[["Folder", "/Users/mia/Downloads", true]]} resolved={{ label: "APPROVED", reason: "Moved to Trash", fresh: true }}>These are the PDFs I already filed.</ApprovalCard>
      <ApprovalCard title="Send mail to Lin?" resolved={{ label: "DECLINED", reason: "You said no", declined: true }} />
    </div> },
];

const MOCHI = "bot_mochi", MISO = "bot_miso", PIP = "bot_pip";
const cat = (id: string, name: string, state?: BotAvatarState) => <BotAvatar id={id} name={name} coat="paper" size="var(--cat)" state={state} />;
const ACTIONS = <><IconButton icon="monitor" label="Bot computer" /><IconButton icon="folder" label="Artifacts" /><IconButton icon="more" label="More actions" /></>;
const BACK = <IconButton icon="arrow-left" label="Back to the list" />;
const HEADS: { position: ChatHeaderPosition; title: string; sub?: string; ids: [string, string][] }[] = [
  { position: "top", title: "Mochi", ids: [[MOCHI, "Mochi"]] },
  { position: "scrolled", title: "Mochi", ids: [[MOCHI, "Mochi"]] },
  { position: "aside", title: "Mochi", ids: [[MOCHI, "Mochi"]] },
];
const head = (props: Partial<React.ComponentProps<typeof ChatHeader>> & { title: string }) => <div className="catalog-dots" style={{ padding: 0, overflow: "hidden", minHeight: props.position === "top" ? 150 : 76 }}>
  <ChatHeader blur avatars={[cat(MOCHI, "Mochi")]} actions={ACTIONS} menu={BACK} menuMode="narrow" {...props} />
</div>;

export const chatHeader: CatalogState[] = [
  { id: "chathead-positions", kind: "inline", label: "ChatHeader · top, scrolled, aside",
    note: "The same cat travels between positions; the left pill shows where the list is hidden (390 px). Hover the name for the underline.",
    render: () => <div className="catalog-stack" style={{ maxWidth: "none", gap: 12 }}>{HEADS.map(h => <div key={h.position}>{head({ title: h.title, position: h.position })}</div>)}</div> },
  { id: "chathead-group", kind: "inline", label: "ChatHeader · a group of three cats, member count at the top, a long name that ellipsizes",
    render: () => <div className="catalog-stack" style={{ maxWidth: "none", gap: 12 }}>
      {head({ title: "Weekly report group", subtitle: "3 members", position: "top", avatars: [cat(MOCHI, "Mochi"), cat(MISO, "Miso"), cat(PIP, "Pip")] })}
      {head({ title: "Weekly report and launch planning with the whole design and finance crew", subtitle: "3 members", position: "top", avatars: [cat(MOCHI, "Mochi"), cat(MISO, "Miso"), cat(PIP, "Pip")] })}
      {head({ title: "Weekly report group", position: "scrolled", avatars: [cat(MOCHI, "Mochi"), cat(MISO, "Miso"), cat(PIP, "Pip")] })}
      {head({ title: "Weekly report and launch planning with the whole design and finance crew", position: "scrolled", avatars: [cat(MOCHI, "Mochi"), cat(MISO, "Miso"), cat(PIP, "Pip")] })}
    </div> },
  { id: "chathead-states", kind: "inline", label: "ChatHeader · cat states: thinking, working, waiting, error, asleep",
    note: "Thinking sways, working bobs, waiting for you leans in, an error stops still, asleep dims. The dot names the state. Reduced motion holds the pose.",
    render: () => <div className="catalog-stack" style={{ maxWidth: "none", gap: 12 }}>{(["thinking", "working", "waiting", "error", "asleep"] as const).map(state => <div key={state}>{head({ title: `Mochi · ${state}`, position: "scrolled", avatars: [cat(MOCHI, "Mochi", state)] })}</div>)}</div> },
  { id: "chathead-phone", kind: "inline", label: "ChatHeader · phone: back pill, three actions, a real name", widths: ["phone"],
    render: () => <div className="catalog-stack" style={{ maxWidth: "none", gap: 12 }}>
      {head({ title: "Archivist", position: "scrolled", avatars: [cat(MOCHI, "Archivist", "working")], menuMode: "always" })}
      {head({ title: "Probe and Scout in the weekly report group", position: "scrolled", avatars: [cat(MOCHI, "Probe"), cat(MISO, "Scout")], menuMode: "always" })}
      {head({ title: "Mochi", position: "top", menuMode: "always" })}
    </div> },
];
