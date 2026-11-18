// V3 kit, group 4: Avatar, BotAvatar, BrandMark, MemberChip, Brand.
import { Avatar, type AvatarTone } from "../../ds/Avatar/Avatar";
import { BotAvatar, type BotAvatarState, type BotCoat } from "../../ds/BotAvatar/BotAvatar";
import { Brand } from "../../ds/Brand/Brand";
import { BrandMark } from "../../ds/BrandMark/BrandMark";
import { MemberChip } from "../../ds/MemberChip/MemberChip";
import { saveBotGround } from "../../avatarStore";
import type { CatalogState } from "../types";
import { Row, Specimen } from "./v3-parts";

const TONES: AvatarTone[] = ["mustard", "teal", "grape", "cobalt", "lime", "tomato"];
const COATS: BotCoat[] = ["tomato", "mustard", "teal", "grape", "lime", "paper"];
const STATES: BotAvatarState[] = ["thinking", "working", "waiting", "error", "asleep"];
const BOT = "bot_mochi";

export const identity: CatalogState[] = [
  { id: "avatar", kind: "inline", label: "Avatar · every tone, initials from one or two words",
    render: () => <Row>{TONES.map(tone => <Specimen key={tone} label={tone}><Avatar name="Mia Chen" tone={tone} /></Specimen>)}
      <Specimen label="one word"><Avatar name="Ana" /></Specimen></Row> },
  { id: "avatar-sizes", kind: "inline", label: "Avatar · sizes, presence, photo",
    render: () => <Row>
      {[24, 34, 48].map(size => <Specimen key={size} label={`${size}`}><Avatar name="Mia Chen" size={size} tone="cobalt" /></Specimen>)}
      {(["online", "away", "offline"] as const).map(status => <Specimen key={status} label={status}><Avatar name="Mia Chen" tone="teal" status={status} /></Specimen>)}
      <Specimen label="photo"><Avatar name="Mia Chen" src="/brand/tofi-logo-calico.svg" /></Specimen>
    </Row> },
  { id: "botavatar-coats", kind: "inline", label: "BotAvatar · every coat (the cat is the Bot's own, drawn once)",
    render: () => <Row>{COATS.map(coat => <Specimen key={coat} label={coat}><BotAvatar id={BOT} name="Mochi" coat={coat} /></Specimen>)}</Row> },
  { id: "botavatar-states", kind: "inline", label: "BotAvatar · every state and size",
    render: () => <Row>
      <Specimen label="idle"><BotAvatar id={BOT} name="Mochi" coat="teal" /></Specimen>
      {STATES.map(state => <Specimen key={state} label={state}><BotAvatar id={BOT} name="Mochi" coat="mustard" state={state} /></Specimen>)}
      {[28, 40, 64].map(size => <Specimen key={size} label={`${size}`}><BotAvatar id={BOT} name="Mochi" coat="grape" size={size} /></Specimen>)}
    </Row> },
  { id: "botavatar-ground", kind: "inline", label: "BotAvatar · ground read from the avatar store (by id), the bot glyph, and an image",
    render: () => {
      saveBotGround("bot_store_lime", "lime");
      saveBotGround("bot_store_tomato", "tomato");
      return <Row>
        <Specimen label="stored lime"><BotAvatar id="bot_store_lime" name="Lime Bot" /></Specimen>
        <Specimen label="stored tomato"><BotAvatar id="bot_store_tomato" name="Tomato Bot" /></Specimen>
        <Specimen label="default for id"><BotAvatar id="bot_without_ground" name="Default Bot" /></Specimen>
        <Specimen label="no id: glyph"><BotAvatar name="Glyph Bot" coat="teal" /></Specimen>
        <Specimen label="src image"><BotAvatar name="Image Bot" coat="paper" src="/brand/tofi-logo-calico.svg" /></Specimen>
      </Row>;
    } },
  { id: "brandmark", kind: "inline", label: "BrandMark · official marks on the light tile; dark tile; sizes",
    render: () => <Row>
      <Specimen label="GitHub"><BrandMark src="/brand/services/github-monochrome.svg" name="GitHub" /></Specimen>
      <Specimen label="Notion"><BrandMark src="/brand/services/notion.svg" name="Notion" /></Specimen>
      <Specimen label="Linear"><BrandMark src="/brand/services/linear.svg" name="Linear" /></Specimen>
      <Specimen label="Gmail"><BrandMark src="/brand/services/google-gmail.png" name="Gmail" /></Specimen>
      <Specimen label="dark tile"><BrandMark src="/brand/services/github-inverse.svg" name="GitHub" tile="dark" /></Specimen>
      <Specimen label="24"><BrandMark src="/brand/services/notion.svg" size={24} /></Specimen>
      <Specimen label="48"><BrandMark src="/brand/services/notion.svg" size={48} /></Specimen>
    </Row> },
  { id: "memberchip", kind: "inline", label: "MemberChip · avatar, role, remove",
    render: () => <div className="tb-members">
      <MemberChip name="Mia Chen" avatar={<Avatar name="Mia Chen" size={28} tone="cobalt" />} role="Owner" />
      <MemberChip name="Mochi" avatar={<BotAvatar id={BOT} name="Mochi" coat="teal" size={28} />} onRemove={() => {}} />
      <MemberChip name="Ana Ruiz" avatar={<Avatar name="Ana Ruiz" size={28} tone="lime" />} role="Admin" onRemove={() => {}} removeLabel="Remove Ana from the group" />
      <MemberChip name="No avatar" />
    </div> },
  { id: "brand", kind: "inline", label: "Brand · awake and asleep, wordmark or mark only, sizes",
    render: () => <Row>
      <Specimen label="awake 30"><Brand /></Specimen>
      <Specimen label="asleep 30"><Brand mood="asleep" /></Specimen>
      <Specimen label="mark only"><Brand wordmark={false} /></Specimen>
      <Specimen label="44"><Brand size={44} /></Specimen>
      <Specimen label="22"><Brand size={22} /></Specimen>
    </Row> },
];
