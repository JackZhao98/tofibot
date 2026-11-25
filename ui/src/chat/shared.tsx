import { readReplyMarker } from "../replyMarker";
import { dateTimeFormat } from "../timezone";
import { LazyGroupAvatar as GroupAvatar } from "../LazyGroupAvatar";
import { intlLocale } from "../i18n/format";
import { BotAvatar, type AvatarMotion } from "../BotAvatar";
import type { Message } from "../types";
import { lazy } from "react";

export function Avatar({ id, group = false, mini = false, motion, members }: { label: string; id: string; group?: boolean; mini?: boolean; motion?: AvatarMotion; members?: string[] }) {
  if (group) return <span className={`avatar group-avatar ${mini ? "mini" : ""}`} aria-hidden="true"><GroupAvatar ids={members ?? []} iconSize={mini ? 14 : 19} /></span>;
  return <BotAvatar id={id} mini={mini} motion={motion} />;
}
/**
 * One stable identity for a person's message from the moment it is typed until it is history. The optimistic copy and
 * the server's copy carry the same client_message_id, so React keeps one DOM node across the swap and nothing replays.
 */
export function blockKey(message: Message) {
  return message.role === "user" && message.client_message_id ? `cm:${message.client_message_id}` : message.id;
}
export function messageId() {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  // LAN HTTP does not expose randomUUID, but still supports getRandomValues.
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
/** Scrolls to a message and flashes it once without leaving a persistent frame. */
export function jumpToMessage(target: HTMLElement | null | undefined) {
  if (!target) return;
  target.scrollIntoView({ behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "instant" : "smooth", block: "center" });
  target.focus({ preventScroll: true });
  const block = target.closest<HTMLElement>(".message-block") ?? target;
  // A data attribute, not a class: the block's className is rewritten by React whenever its arrival flags change, which wiped the flash.
  block.removeAttribute("data-jump-target");
  void block.offsetWidth;
  block.setAttribute("data-jump-target", "");
  window.setTimeout(() => block.removeAttribute("data-jump-target"), 1700);
}
/** Shown in the conversation while a run has neither tool steps nor reply text yet. */
/** Avatar + name that opens a sender group in a group chat (hidden by CSS when the unit continues the previous one). */
export function SenderHead({ id, name }: { id: string; name: string }) {
  return <div className="sender-head"><div className="sender-avatar"><Avatar label={name} id={id} mini /></div><span className="sender-name">{name}</span></div>;
}
export function formatTime(value: string, timeZone: string) {
  return dateTimeFormat(intlLocale(), { hour: "2-digit", minute: "2-digit", timeZone }).format(new Date(value));
}
export function previewText(value: string, max = 160) {
  const normalized = readReplyMarker(value).body.replace(/\[(.*?)]\([^)]*\)?|[*_`#>]/g, "$1").replace(/\s+/g, " ").trim();
  return normalized.length > max ? `${normalized.slice(0, max - 1)}…` : normalized;
}

export const FirstRunHero = lazy(() => import("../FirstRunHero").then(module => ({ default: module.FirstRunHero })));

export const MailDraftCard = lazy(() => import("../MailDraftCard").then(module => ({ default: module.MailDraftCard })));
