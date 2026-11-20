import * as Collapsible from "@radix-ui/react-collapsible";
import * as m from "motion/react-m";
import { useEffect, useId, useRef, useState } from "react";
import { BotAvatar } from "./ds/BotAvatar/BotAvatar";
import { CollapsibleContent } from "./ds/Collapsible/Collapsible";
import { IconButton } from "./ds/IconButton/IconButton";
import { Icon } from "./ds/Icon/Icon";
import { useTranslation } from "./i18n";
import { botCatState } from "./chat/botCatState";
import { CARD_IN, CARD_OUT, CAT_TRAVEL, CatMotion, CatTravel, takeFlight } from "./catMotion";
import { roleOf, withRole } from "./botRole";
import { setPendingSection } from "./settings/pendingSection";
import { closeWidget, openCover, type Surfaces } from "./surfaces";
import type { Bot, Run, ToolActivity } from "./types";
import "./bot-card.css";

/** What the workspace hands the Bot card besides the Bot itself: live state for the status line and the three rows behind the menu. */
export type BotCardCtx = {
  runs: Run[]; activities: ToolActivity[]; asked: ReadonlySet<string>; asleep: boolean;
  computerState: string; memoryCount: number; taskCount: number;
  /** Absent when the Bot has no computer. */
  openComputer?: () => void; openMemory: () => void; openTasks: () => void;
};

type Surf = (update: (current: Surfaces) => Surfaces) => void;

function EditableLine({ value, label, placeholder, required, maxLength, onSave, className }: { value: string; label: string; placeholder?: string; required?: boolean; maxLength: number; onSave: (next: string) => Promise<void>; className: string }) {
  const [draft, setDraft] = useState(value);
  const [failed, setFailed] = useState(false);
  // Escape sets this before it blurs the field, so the blur that follows throws the edit away instead of saving it.
  const cancelled = useRef(false);
  useEffect(() => setDraft(value), [value]);
  const commit = (typed: string) => {
    if (cancelled.current) { cancelled.current = false; setDraft(value); return; }
    const next = typed.trim();
    if (next === value.trim() || (required && !next)) { setDraft(value); return; }
    setFailed(false);
    onSave(next).catch(() => { setFailed(true); setDraft(value); });
  };
  return <input className={className} value={draft} aria-label={label} placeholder={placeholder} maxLength={maxLength} aria-invalid={failed || undefined}
    onChange={event => setDraft(event.target.value)} onBlur={event => commit(event.currentTarget.value)}
    // Enter that confirms an IME candidate is composition, not a request to save.
    onKeyDown={event => { if (event.key === "Enter" && !event.nativeEvent.isComposing) event.currentTarget.blur(); else if (event.key === "Escape") { cancelled.current = true; event.stopPropagation(); event.currentTarget.blur(); } }} />;
}

/**
 * The Bot card: the top widget of the column. The cat (the header's own cat, travelling by its shared layout id) sits on the card's top edge,
 * half over it; the card unfolds downward from the cat's middle and pushes the other widgets down. Lazy chunk `surfaceViews`.
 * `leaving` is the exit: the cat is handed back to the header at once (it flies home while the card folds, 180 ms).
 */
export function BotCard({ bot, card, surf, save, leaving, focusN, conversationId }: { bot: Bot; card: BotCardCtx; surf: Surf; /** The Bot settings save (Workspace `updateBot`). */ save: (id: string, input: Partial<Pick<Bot, "name" | "instructions">>) => Promise<void>; leaving: boolean; focusN: number; conversationId: string }) {
  const { t } = useTranslation("chat");
  const [open, setOpen] = useState(false);
  // Only a tap on the header cat flies and unfolds; a card that is simply there (a conversation switch brings an open one back) shows at once.
  // The flight state lives on the DOM, not in React state: a render in the middle of the motion would be a hitch in it.
  const [fresh] = useState(takeFlight);
  const wrap = useRef<HTMLDivElement>(null);
  const fold = useRef<HTMLDivElement>(null);
  const menuId = useId();
  const cat = useRef<HTMLButtonElement>(null);
  useEffect(() => { if (focusN > 0 && !leaving) cat.current?.focus({ preventScroll: true }); }, [focusN, leaving]);
  useEffect(() => { if (!fresh) return; const timer = window.setTimeout(() => wrap.current?.removeAttribute("data-flying"), CAT_TRAVEL.duration * 1000 + 60); return () => window.clearTimeout(timer); }, [fresh]);
  const state = botCatState(bot.id, conversationId, card.runs, card.activities, card.asleep, card.asked);
  const working = card.activities.filter(activity => activity.bot_id === bot.id && activity.conversation_id === conversationId && (activity.status === "running" || activity.status === "queued")).at(-1);
  const status = state === "waiting" ? t("botCard.status_waiting") : state === "working" || state === "thinking" ? (working ? t("botCard.status_working_on", { what: working.name.replace(/[_.-]+/g, " ") }) : t("botCard.status_working")) : state === "error" ? t("botCard.status_failed") : t("botCard.status_idle");
  const settings = (section?: string) => { setPendingSection(section ? { scope: "bot-settings", page: "", section } : null); surf(current => openCover(current, "bot-settings")); };
  const rows: { key: string; label: string; value: string; onOpen?: () => void }[] = [
    { key: "computer", label: t("contextCard.title.desktop"), value: card.computerState, onOpen: card.openComputer },
    { key: "memory", label: t("contextCard.title.memory"), value: String(card.memoryCount), onOpen: card.openMemory },
    { key: "tasks", label: t("botCard.scheduled_tasks"), value: String(card.taskCount), onOpen: card.openTasks },
  ];
  return <CatMotion><div ref={wrap} className="bot-card-wrap" data-flying={fresh || undefined}>
    <m.div ref={fold} className={`bot-fold${fresh ? " is-moving" : ""}`} initial={fresh ? { height: 0 } : false} animate={{ height: leaving ? 0 : "auto" }} transition={leaving ? CARD_OUT : CARD_IN}
      onAnimationStart={() => fold.current?.classList.add("is-moving")} onAnimationComplete={() => fold.current?.classList.remove("is-moving")}>
      <div className="bot-fold-in"><Collapsible.Root open={open} onOpenChange={setOpen} asChild>
        <section className="bot-card" data-card="bot" aria-label={t("botCard.label", { name: bot.name })}>
          <div className="bot-card-bar">
            <Collapsible.Trigger asChild><IconButton icon="menu" size="sm" label={t(open ? "botCard.menu_close" : "botCard.menu_open")} aria-controls={menuId} /></Collapsible.Trigger>
            <IconButton icon="close" size="sm" label={t("contextCard.close", { title: t("botCard.title") })} onClick={() => surf(current => closeWidget(current, "bot"))} />
          </div>
          <EditableLine className="bot-card-name" value={bot.name} label={t("botCard.name")} required maxLength={200} onSave={name => save(bot.id, { name })} />
          <EditableLine className="bot-card-role" value={roleOf(bot.instructions)} label={t("botCard.role")} placeholder={t("botCard.role_placeholder")} maxLength={300} onSave={line => save(bot.id, { instructions: withRole(bot.instructions, line) })} />
          <p className="bot-card-status" role="status" data-state={state ?? "idle"}><span className="bot-card-dot" aria-hidden="true" />{status}</p>
          <CollapsibleContent id={menuId} className="bot-card-menu">
            <ul>{rows.map(row => <li key={row.key}><button type="button" className="bot-card-row" disabled={!row.onOpen} onClick={row.onOpen}><span>{row.label}</span><span className="bot-card-value">{row.value}<Icon name="chevron-right" size={14} /></span></button></li>)}</ul>
          </CollapsibleContent>
          <button type="button" className="bot-card-advanced" onClick={() => settings()}>{t("botCard.advanced")}<Icon name="chevron-right" size={12} /></button>
        </section>
      </Collapsible.Root></div>
    </m.div>
    {!leaving && <button ref={cat} type="button" className="bot-card-cat" aria-label={t("botCard.avatar", { name: bot.name })} onClick={() => settings("identity")}>
      <CatTravel id={conversationId} className="tb-chathead-face tb-chathead-travel"><BotAvatar id={bot.id} name={bot.name} coat="paper" size={96} state={state} /></CatTravel>
    </button>}
  </div></CatMotion>;
}
