import { useState, type ReactNode } from "react";
import * as HoverCard from "@radix-ui/react-hover-card";
import { TofiIcon } from "../icons";
import { BotAvatar } from "../BotAvatar";
import type { Bot } from "../types";
import { useTranslation } from "../i18n";
import { layerRoot } from "../layers";
export function BotIdentityHoverCard({ bot, children, onOpen, onMention }: { bot: Bot; children: ReactNode; onOpen?: () => void; onMention?: () => void }) {
  const { t } = useTranslation("bots");
  const [open, setOpen] = useState(false);
  return <HoverCard.Root open={open} onOpenChange={setOpen} openDelay={400} closeDelay={180}>
    <HoverCard.Trigger asChild><span className="identity-card-anchor" tabIndex={0} aria-label={t("identity.profile_aria", { name: bot.name })}>{children}</span></HoverCard.Trigger>
    <HoverCard.Portal container={layerRoot()}><HoverCard.Content className="bot-identity-card tofi-pop" side="bottom" align="start" sideOffset={8} aria-label={t("identity.profile_aria", { name: bot.name })}><div className="identity-card-title"><BotAvatar id={bot.id} /><div><strong>{bot.name}</strong></div></div>{bot.instructions?.trim() && <p>{bot.instructions.trim().slice(0, 140)}{bot.instructions.trim().length > 140 ? "…" : ""}</p>}{onMention && <button onClick={() => { setOpen(false); onMention(); }}><TofiIcon name="mention" size={16} style={{ verticalAlign: "middle" }} /> {t("identity.mention", { name: bot.name })}</button>}{onOpen && <button onClick={() => { setOpen(false); onOpen(); }}>{t("identity.open_dm")} <TofiIcon name="chat" size={16} style={{ verticalAlign: "middle" }} /></button>}</HoverCard.Content></HoverCard.Portal>
  </HoverCard.Root>;
}
