// V3 kit, group 2: Badge, Tag, Kbd, Banner, Notice, Toast, Progress, Skeleton, EmptyState.
import { Badge, type BadgeTone } from "../../ds/Badge/Badge";
import { Banner, type BannerTone } from "../../ds/Banner/Banner";
import { BotAvatar } from "../../ds/BotAvatar/BotAvatar";
import { Button } from "../../ds/Button/Button";
import { DriverSwitch } from "../../ds/DriverSwitch/DriverSwitch";
import { ScreenPanel } from "../../ds/ScreenPanel/ScreenPanel";
import { EmptyState } from "../../ds/EmptyState/EmptyState";
import { Kbd } from "../../ds/Kbd/Kbd";
import { Notice } from "../../ds/Notice/Notice";
import { Progress } from "../../ds/Progress/Progress";
import { Skeleton } from "../../ds/Skeleton/Skeleton";
import { Tag } from "../../ds/Tag/Tag";
import { Toast } from "../../ds/Toast/Toast";
import { Widget } from "../../ds/Widget/Widget";
import { FileRow } from "../../ds/FileRow/FileRow";
import type { CatalogState } from "../types";
import { Dots, Row, Specimen } from "./v3-parts";

const TONES: [BadgeTone, string][] = [["neutral", "Label"], ["working", "Working"], ["needs", "Needs you"], ["done", "Done"], ["error", "Not finished"], ["unread", "3 new"], ["info", "Info"], ["paused", "Paused"]];
const BANNERS: [BannerTone, string, string][] = [
  ["info", "Update ready", "Restart to use the new version."],
  ["warning", "Storage almost full", "Delete files or raise the limit."],
  ["error", "No model", "Add a provider key to start a chat."],
  ["done", "Key created", "Copy it now; it is shown once."],
];

export const status: CatalogState[] = [
  { id: "badge", kind: "inline", label: "Badge · every tone",
    render: () => <Row>{TONES.map(([tone, text]) => <Specimen key={tone} label={tone}><Badge tone={tone}>{text}</Badge></Specimen>)}</Row> },
  { id: "badge-icons", kind: "inline", label: "Badge · icon override, no icon, empty",
    render: () => <Row>
      <Specimen label="icon=key"><Badge tone="info" icon="key">Key</Badge></Specimen>
      <Specimen label="icon=null"><Badge tone="done" icon={null}>Done</Badge></Specimen>
      <Specimen label="empty (hidden)"><Badge tone="done" /></Specimen>
    </Row> },
  { id: "tag", kind: "inline", label: "Tag · plain, with icon, removable",
    render: () => <Row>
      <Specimen label="plain"><Tag>read</Tag></Specimen>
      <Specimen label="icon"><Tag icon="file">PDF</Tag></Specimen>
      <Specimen label="removable"><Tag onRemove={() => {}}>write</Tag></Specimen>
      <Specimen label="custom remove label"><Tag onRemove={() => {}} removeLabel="Drop scope">admin</Tag></Specimen>
    </Row> },
  { id: "kbd", kind: "inline", label: "Kbd · one key per cap",
    render: () => <Row><Kbd>⌘</Kbd><Kbd>K</Kbd><Kbd>Esc</Kbd><Kbd>Shift</Kbd></Row> },
  { id: "banner", kind: "inline", label: "Banner · every tone",
    render: () => <div className="catalog-stack">{BANNERS.map(([tone, title, text]) => <Banner key={tone} tone={tone} title={title}>{text}</Banner>)}</div> },
  { id: "banner-parts", kind: "inline", label: "Banner · with an action, title only, text only",
    render: () => <div className="catalog-stack">
      <Banner tone="info" title="Update ready" action={<Button size="sm">Restart</Button>}>Restart to use the new version.</Banner>
      <Banner tone="warning" title="Storage almost full" />
      <Banner tone="error">Add a provider key to start a chat.</Banner>
    </div> },
  { id: "notice", kind: "inline", label: "Notice · plain, with icon, with one action",
    render: () => <div className="catalog-stack">
      <Notice>The conversation moved to Mochi.</Notice>
      <Notice icon="info">Spend is at 80% of the limit.</Notice>
      <Notice icon="alert" action={<Button variant="ghost" size="sm">Review</Button>}>A command ended with an error.</Notice>
    </div> },
  { id: "toast", kind: "inline", label: "Toast · neutral, done, error; with action and close",
    render: () => <Dots><div className="catalog-stack">
      <Toast>Key copied</Toast>
      <Toast tone="done" onClose={() => {}}>Saved</Toast>
      <Toast tone="error" action="Retry" onAction={() => {}} onClose={() => {}}>Not sent: the conversation is archived</Toast>
      <Toast icon="bell" tone="neutral" action="Open">Mochi finished a task</Toast>
    </div></Dots> },
  { id: "progress", kind: "inline", label: "Progress · tones, values, label and percentage",
    render: () => <div className="catalog-stack">
      <Progress value={0} label="Upload" showValue />
      <Progress value={42} label="Upload" showValue />
      <Progress value={100} label="Upload" showValue />
      <Progress value={82} label="Storage" showValue tone="warning" />
      <Progress value={96} label="Spend" showValue tone="danger" />
      <Progress value={60} />
    </div> },
  { id: "skeleton", kind: "inline", label: "Skeleton · 1, 3 and 5 lines",
    render: () => <div className="catalog-stack"><Skeleton lines={1} /><Skeleton /><Skeleton lines={5} /></div> },
  { id: "empty", kind: "inline", label: "EmptyState · icon, art, with and without action",
    render: () => <Dots><div className="catalog-stack">
      <EmptyState title="No keys yet" icon="key" action={<Button variant="primary" icon="plus">Create key</Button>}>Keys let a service call Tofi as you.</EmptyState>
      <EmptyState title="No files" art={<img src="/brand/tofi-logo-calico.svg" alt="" />}>Files a Bot sends appear here.</EmptyState>
      <EmptyState title="Nothing here" />
    </div></Dots> },
  { id: "screen-panel", kind: "inline", label: "ScreenPanel · small and large, at rest, Bot driving, you driving",
    render: () => <Dots><div className="catalog-stack" style={{ maxWidth: 420 }}>
      <ScreenPanel size="small" title="Bot computer" placeholder="The computer is off" expandLabel="Enlarge" closeLabel="Close" chin={<Button variant="secondary" size="sm">Turn on</Button>} />
      <ScreenPanel size="small" title="Bot computer" driver="bot" placeholder="Mochi is working" expandLabel="Enlarge" closeLabel="Close" chin={<DriverSwitch driver="bot" bot={<BotAvatar name="Mochi" coat="paper" size={22} />} youLabel="You" label="Take control" />} />
      <ScreenPanel size="large" title="Bot computer" driver="you" source="Your Mac" placeholder="You are driving" expandLabel="Shrink" expanded closeLabel="Close" chin={<DriverSwitch driver="you" bot={<BotAvatar name="Mochi" coat="paper" size={22} />} youLabel="You" label="Hand back" />} />
    </div></Dots> },
  { id: "widget", kind: "inline", label: "Widget · column open, folded, sidebar",
    render: () => <Dots><div className="catalog-stack" style={{ maxWidth: 340 }}>
      <Widget title="Artifacts" foldLabel="Fold" closeLabel="Close" onFold={() => {}} onClose={() => {}}><FileRow kind="pdf" name="weekly-report.pdf" meta="3 pages" /></Widget>
      <Widget title="Terminal" folded foldLabel="Unfold" closeLabel="Close" onFold={() => {}} onClose={() => {}}><span /></Widget>
      <div style={{ height: 160 }}><Widget form="sidebar" title="Memory" closeLabel="Close" onClose={() => {}}><span /></Widget></div>
    </div></Dots> },
  { id: "driver-switch", kind: "inline", label: "DriverSwitch · Bot, you, idle, disabled",
    render: () => <Dots><Row>
      <Specimen label="bot"><DriverSwitch driver="bot" bot={<BotAvatar name="Mochi" coat="paper" size={22} />} youLabel="You" label="Take control" /></Specimen>
      <Specimen label="you"><DriverSwitch driver="you" bot={<BotAvatar name="Mochi" coat="paper" size={22} />} youLabel="You" label="Hand back" /></Specimen>
      <Specimen label="idle"><DriverSwitch driver="bot" idle bot={<BotAvatar name="Mochi" coat="paper" size={22} />} youLabel="You" label="Take control" /></Specimen>
      <Specimen label="disabled"><DriverSwitch driver="bot" disabled bot={<BotAvatar name="Mochi" coat="paper" size={22} />} youLabel="You" label="Take control" /></Specimen>
    </Row></Dots> },
];
