// The kit components test-ds-parity.mjs renders, by their V3 spec name. Loaded through Vite's SSR loader.
import { Avatar } from "../src/ds/Avatar/Avatar";
import { Badge } from "../src/ds/Badge/Badge";
import { Banner } from "../src/ds/Banner/Banner";
import { BotAvatar } from "../src/ds/BotAvatar/BotAvatar";
import { BrandMark } from "../src/ds/BrandMark/BrandMark";
import { Button } from "../src/ds/Button/Button";
import { Card } from "../src/ds/Card/Card";
import { Checkbox } from "../src/ds/Checkbox/Checkbox";
import { EmptyState } from "../src/ds/EmptyState/EmptyState";
import { Field } from "../src/ds/Field/Field";
import { Icon } from "../src/ds/Icon/Icon";
import { IconButton } from "../src/ds/IconButton/IconButton";
import { Kbd } from "../src/ds/Kbd/Kbd";
import { MemberChip } from "../src/ds/MemberChip/MemberChip";
import { Notice } from "../src/ds/Notice/Notice";
import { Progress } from "../src/ds/Progress/Progress";
import { Skeleton } from "../src/ds/Skeleton/Skeleton";
import { Tag } from "../src/ds/Tag/Tag";
import { TextArea } from "../src/ds/TextArea/TextArea";
import { TextInput } from "../src/ds/TextInput/TextInput";
import { Toast } from "../src/ds/Toast/Toast";
import { DataTable } from "../src/ds/DataTable/DataTable";
import { KeyRow } from "../src/ds/KeyRow/KeyRow";
import { SettingsRow } from "../src/ds/SettingsRow/SettingsRow";
import { SettingsSection } from "../src/ds/SettingsSection/SettingsSection";
import { Stat } from "../src/ds/Stat/Stat";

export const registry = { Avatar, Badge, Banner, BotAvatar, BrandMark, Button, Card, Checkbox, EmptyState, Field, Icon, IconButton, Kbd, MemberChip, Notice, Progress, Skeleton, Tag, TextArea, TextInput, Toast };

// Kit II: conversation and work (PR 0.7).
import { ApprovalCard } from "../src/ds/ApprovalCard/ApprovalCard";
import { Divider } from "../src/ds/Divider/Divider";
import { JumpButton } from "../src/ds/JumpButton/JumpButton";
import { LiveLine } from "../src/ds/LiveLine/LiveLine";
import { Mention } from "../src/ds/Mention/Mention";
import { ProcessLine } from "../src/ds/ProcessLine/ProcessLine";
import { ProgressiveBlur } from "../src/ds/ProgressiveBlur/ProgressiveBlur";
import { Reactions } from "../src/ds/Reactions/Reactions";
import { SidebarRow } from "../src/ds/SidebarRow/SidebarRow";
import { StepList } from "../src/ds/StepList/StepList";
import { TaskCard } from "../src/ds/TaskCard/TaskCard";
import { ToolSteps } from "../src/ds/ToolSteps/ToolSteps";
Object.assign(registry, { ApprovalCard, Divider, JumpButton, LiveLine, Mention, ProcessLine, ProgressiveBlur, Reactions, SidebarRow, StepList, TaskCard, ToolSteps });

// Forms (PR 2.2): Tabs is Radix with the spec's tags. Switch and Segmented are not here: Radix adds a hidden input beside the
// button in a server render, which the spec does not have. Select is not here: the spec's is a native <select>.
import { Tabs } from "../src/ds/Tabs/Tabs";
Object.assign(registry, { Tabs });

// Kit IV: settings and data (PR 0.9).
Object.assign(registry, { DataTable, KeyRow, SettingsRow, SettingsSection, Stat });

// ---- Kit III (V3 PLAN 0.8)
import { ArtifactCard } from "../src/ds/ArtifactCard/ArtifactCard";
import { AttachmentGrid } from "../src/ds/AttachmentGrid/AttachmentGrid";
import { CodeBlock } from "../src/ds/CodeBlock/CodeBlock";
import { DraftCard } from "../src/ds/DraftCard/DraftCard";
import { FileRow } from "../src/ds/FileRow/FileRow";
import { FileTile } from "../src/ds/FileTile/FileTile";
import { InboxDigest } from "../src/ds/InboxDigest/InboxDigest";
import { Letter } from "../src/ds/Letter/Letter";
import { ScheduleCard } from "../src/ds/ScheduleCard/ScheduleCard";

// The spec writes the line count as {n}; the kit's i18n writes {{count}}. The wrapper maps the spec's cases onto the kit.
const n = (text?: string) => text?.replace("{n}", "{{count}}");
const CodeBlockSpec = (props: Parameters<typeof CodeBlock>[0]) => <CodeBlock {...props} linesLabel={n(props.linesLabel)} moreLabel={n(props.moreLabel)} />;
Object.assign(registry, { ArtifactCard, AttachmentGrid, CodeBlock: CodeBlockSpec, DraftCard, FileRow, FileTile, InboxDigest, Letter, ScheduleCard });
