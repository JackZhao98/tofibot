// V3 kit, group 5 (PR 0.9): SettingsSection, SettingsRow, KeyRow, DataTable, Stat.
import { Avatar } from "../../ds/Avatar/Avatar";
import { Badge } from "../../ds/Badge/Badge";
import { Button } from "../../ds/Button/Button";
import { DataTable, type DataColumn } from "../../ds/DataTable/DataTable";
import { IconButton } from "../../ds/IconButton/IconButton";
import { KeyRow } from "../../ds/KeyRow/KeyRow";
import { SettingsRow } from "../../ds/SettingsRow/SettingsRow";
import { SettingsSection } from "../../ds/SettingsSection/SettingsSection";
import { Stat } from "../../ds/Stat/Stat";
import { Tag } from "../../ds/Tag/Tag";
import { TextInput } from "../../ds/TextInput/TextInput";
import type { CatalogState } from "../types";
import { Dots } from "./v3-parts";

const WIDE = { maxWidth: 760, display: "grid", gap: 28 } as const;
const more = <IconButton icon="more" label="Key actions" size="sm" />;

const MEMBERS = [
  { id: 1, name: "Lin Park", tone: "mustard", role: "Admin", group: "Core", spend: "$42.18", state: "Active" },
  { id: 2, name: "Mia Chen", tone: "teal", role: "Member", group: "Research", spend: "$8.02", state: "Active" },
  { id: 3, name: "Lee Park", tone: "grape", role: "Member", group: "Self-serve", spend: "$0.00", state: "Paused" },
];
const MEMBER_COLUMNS: DataColumn[] = [
  { key: "name", label: "Member", render: (v, r) => <span style={{ display: "inline-flex", gap: 10, alignItems: "center" }}><Avatar name={String(v)} size={26} tone={r.tone as "mustard"} />{String(v)}</span> },
  { key: "role", label: "Role" },
  { key: "group", label: "Group" },
  { key: "spend", label: "Spend", align: "right", mono: true },
  { key: "state", label: "State", render: v => v === "Active" ? null : <Badge>{String(v)}</Badge> },
];
const VENDORS = [
  { id: "n", vendor: "Notion", logo: "/brand/services/notion.svg", calls: "1,284", spend: "$12.40" },
  { id: "l", vendor: "Linear", logo: "/brand/services/linear.svg", calls: "611", spend: "$4.75" },
  { id: "g", vendor: "GitHub", logo: "/brand/services/github-monochrome.svg", calls: "97", spend: "$0.90" },
];
const VENDOR_COLUMNS: DataColumn[] = [
  { key: "vendor", label: "Service", logoKey: "logo" },
  { key: "calls", label: "Calls", align: "right", mono: true },
  { key: "spend", label: "Spend", align: "right", mono: true },
];
const WIDE_COLUMNS: DataColumn[] = [
  { key: "when", label: "When", mono: true }, { key: "who", label: "Member" }, { key: "action", label: "Action" },
  { key: "target", label: "Target" }, { key: "from", label: "From" }, { key: "result", label: "Result" },
];
const WIDE_ROWS = [
  { id: 1, when: "Nov 17 09:41", who: "Lin Park", action: "Created key", target: "production-bot", from: "10.0.10.4", result: "Done" },
  { id: 2, when: "Nov 17 09:44", who: "Mia Chen", action: "Revoked key", target: "old-ci", from: "10.0.10.9", result: "Done" },
];

export const settingsKit: CatalogState[] = [
  { id: "settings-section", kind: "inline", label: "SettingsSection · title, description, actions, rows in a card",
    render: () => <Dots><div style={WIDE}>
      <SettingsSection title="Appearance" description="How Tofi looks on this device." actions={<Button size="sm" variant="ghost">Reset</Button>}>
        <SettingsRow label="Theme" hint="Follows the system by default."><Button variant="secondary" size="sm">Change</Button></SettingsRow>
        <SettingsRow label="Language"><Tag>English</Tag></SettingsRow>
        <SettingsRow label="Server name" htmlFor="catalog-server-name"><span style={{ width: 200 }}><TextInput id="catalog-server-name" defaultValue="Home" /></span></SettingsRow>
      </SettingsSection>
    </div></Dots> },
  { id: "settings-section-danger", kind: "inline", label: "SettingsSection · danger zone",
    render: () => <Dots><div style={WIDE}>
      <SettingsSection title="Danger zone" description="These cannot be undone." danger>
        <SettingsRow label="Start over" hint="Deletes every Bot, conversation and file on this server."><Button variant="danger" size="sm">Start over</Button></SettingsRow>
        <SettingsRow label="Remove this member"><Button variant="danger" size="sm">Remove</Button></SettingsRow>
      </SettingsSection>
    </div></Dots> },
  { id: "settings-section-parts", kind: "inline", label: "SettingsSection · title only, long description, two sections stacked",
    render: () => <Dots><div style={WIDE}>
      <SettingsSection title="Title only"><SettingsRow label="One row"><Tag>on</Tag></SettingsRow></SettingsSection>
      <SettingsSection title="A long description holds one line, then wraps at sixty characters"
        description="Descriptions stop at sixty characters so a line never runs the width of the page, even when the page is wide.">
        <SettingsRow label="Row"><Tag>on</Tag></SettingsRow>
      </SettingsSection>
    </div></Dots> },
  { id: "settings-row", kind: "inline", label: "SettingsRow · label only, with hint, several controls, long hint (wraps under the label on a phone)",
    render: () => <Dots><div style={WIDE}><SettingsSection title="Rows">
      <SettingsRow label="Label only"><Tag>on</Tag></SettingsRow>
      <SettingsRow label="With a hint" hint="Bots that use this stop at their next call."><Button size="sm" variant="secondary">Edit</Button></SettingsRow>
      <SettingsRow label="Two controls"><Button size="sm" variant="ghost">Cancel</Button><Button size="sm">Save</Button></SettingsRow>
      <SettingsRow label="A long label that names a setting with some care" hint="A long hint that explains what changes, what stays, and when the change takes effect, in a sentence or two.">
        <span style={{ width: 200 }}><TextInput defaultValue="Home" aria-label="Name" /></span>
      </SettingsRow>
    </SettingsSection></div></Dots> },
  { id: "keyrow", kind: "inline", label: "KeyRow · icon, scope, outside service mark, stamp, revoked (masked values only)",
    render: () => <Dots><div style={WIDE}>
      <SettingsSection title="Tofi API keys" description="Each key shows its last four characters, nothing more.">
        <KeyRow name="production-bot" scope="read" masked="sk-tofi-…9Fq2" meta="Used 2 hours ago" actions={more} />
        <KeyRow name="ci-runner" scope="read, write" masked="sk-tofi-…b7c0" meta="Never used" actions={more} />
        <KeyRow name="old-ci" masked="sk-tofi-…aa01" meta="Revoked Oct 3" revoked />
      </SettingsSection>
      <SettingsSection title="Service keys">
        <KeyRow name="Notion" logo="/brand/services/notion.svg" masked="secret_…a1b2" meta="Added Nov 2" actions={more} />
        <KeyRow name="Linear" logo="/brand/services/linear.svg" masked="lin_api_…Tt0a" meta="Added Oct 21" status={{ tone: "error", label: "Refused" }} actions={more} />
        <KeyRow name="GitHub" logo="/brand/services/github-inverse.svg" logoTile="dark" masked="ghp_…4xYz" meta="Added Oct 9" status={{ tone: "needs", label: "Expired" }} actions={more} />
        <KeyRow name="Slack" logo="/brand/services/slack.png" masked="xoxb-…77aa" meta="Revoked Sep 30" revoked />
      </SettingsSection>
    </div></Dots> },
  { id: "keyrow-parts", kind: "inline", label: "KeyRow · no meta, no actions, two buttons, a long name and value",
    render: () => <Dots><div style={WIDE}><SettingsSection title="Keys">
      <KeyRow name="No meta, no actions" masked="sk-tofi-…0001" />
      <KeyRow name="Two buttons" masked="sk-tofi-…0002" meta="Used today" actions={<><Button size="sm" variant="ghost">Rotate</Button><Button size="sm" variant="ghost">Revoke</Button></>} />
      <KeyRow name="A key with a very long name that should wrap rather than push the actions off the card" scope="read" masked="sk-tofi-…0003" meta="Used 4 minutes ago from the office network" status={{ tone: "error", label: "Refused" }} actions={more} />
      <KeyRow name="Custom icon" icon="lock" masked="ssh-ed25519 …Hq9" meta="Added Aug 12" actions={more} />
    </SettingsSection></div></Dots> },
  { id: "keyrow-focus", kind: "inline", label: "KeyRow · keyboard focus on the row's action",
    render: () => <Dots><div style={WIDE}><SettingsSection title="Keys"><KeyRow name="production-bot" masked="sk-tofi-…9Fq2" meta="Used 2 hours ago" actions={<IconButton icon="more" label="Key actions" size="sm" data-specimen="focus" />} /></SettingsSection></div></Dots>,
    force: [{ selector: '[data-specimen="focus"]', state: "focus" }] },
  { id: "datatable", kind: "inline", label: "DataTable · cells with avatars, mono right-aligned figures, stamps, service marks",
    render: () => <Dots><div style={WIDE}>
      <DataTable columns={MEMBER_COLUMNS} rows={MEMBERS} />
      <DataTable columns={VENDOR_COLUMNS} rows={VENDORS} />
    </div></Dots> },
  { id: "datatable-section", kind: "inline", label: "DataTable · inside a SettingsSection (no second frame)",
    render: () => <Dots><div style={WIDE}><SettingsSection title="Spend by service" description="This month, all Bots.">
      <DataTable columns={VENDOR_COLUMNS} rows={VENDORS} />
    </SettingsSection></div></Dots> },
  { id: "datatable-empty", kind: "inline", label: "DataTable · empty",
    render: () => <Dots><div style={WIDE}><DataTable columns={MEMBER_COLUMNS} rows={[]} empty="No members yet. Invite someone to start." /></div></Dots> },
  { id: "datatable-wide", kind: "inline", label: "DataTable · six columns; on a phone the frame scrolls sideways and takes keyboard focus",
    render: () => <Dots><div style={WIDE}><DataTable label="Audit log" columns={WIDE_COLUMNS} rows={WIDE_ROWS} /></div></Dots> },
  { id: "datatable-hover", kind: "inline", label: "DataTable · row hover, scroll region focus",
    render: () => <Dots><div style={WIDE}><DataTable label="Audit log" columns={WIDE_COLUMNS} rows={WIDE_ROWS} /></div></Dots>,
    force: [{ selector: ".tb-table tbody tr:nth-child(2)", state: "hover" }, { selector: ".tb-table-wrap", state: "focus" }] },
  { id: "stat", kind: "inline", label: "Stat · spark, delta tones, unit, no delta, four in a row",
    render: () => <Dots><div style={{ ...WIDE, gridTemplateColumns: "repeat(auto-fit, minmax(160px, 1fr))", gap: 16 }}>
      <Stat label="Spend this month" value="$42.18" delta="21% of limit" spark={[20, 35, 30, 50, 45, 70, 60]} />
      <Stat label="Runs" value="1,284" delta="+12% vs last month" deltaTone="up" spark={[40, 42, 55, 48, 62, 58, 80]} />
      <Stat label="Failed" value="7" unit="runs" delta="+3" deltaTone="down" />
      <Stat label="Storage" value="3.2" unit="GB" />
    </div></Dots> },
  { id: "stat-reel", kind: "inline", label: "Stat · reel, digits rolled to their value",
    render: () => <Dots><div style={{ ...WIDE, gridTemplateColumns: "repeat(auto-fit, minmax(160px, 1fr))", gap: 16 }}>
      <Stat variant="reel" label="Input tokens" value="1,284,090" />
      <Stat variant="reel" label="Requests" value="3,402" />
    </div></Dots> },
];
