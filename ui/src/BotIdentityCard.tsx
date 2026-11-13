import { lazy, Suspense, type ReactNode } from "react";
import type { Bot } from "./types";
const IdentityHoverCard = lazy(() => import("./overlays/BotIdentityHoverCard").then(module => ({ default: module.BotIdentityHoverCard })));
export function BotIdentityCard(props: { bot: Bot; children: ReactNode; onOpen?: () => void; onMention?: () => void }) {
  return <Suspense fallback={<span className="identity-card-anchor">{props.children}</span>}><IdentityHoverCard {...props} /></Suspense>;
}
