// Mail in the timeline: the mail a Bot read (kit Letter, in), the text card, and the draft that waits for a press (Letter, out) with
// its stamped receipt. Synthetic mail only.
import type { ComponentProps } from "react";
import { DisplayCard } from "../../DisplayCard";
import { MailDraftCard, type MailDraft } from "../../MailDraftCard";
import { at, bot, isolated } from "../world";
import type { CatalogSection, CatalogState } from "../types";

const BODY = "Hi,\n\nThe Europe column changes on Monday, not Friday. Berlin and Paris move to the new list; London stays.\n\nCan you confirm before noon so I can send the table to the shops?\n\nThanks, Lin";
const LONG = `${BODY}\n\n${"Please also check the new list for the Rome shop, which still shows the old Friday date. ".repeat(6)}`;
const mail = { type: "mail" as const, from: "Lin Wei", to: "you@tofi.example", subject: "Q3 price table, one change", received_at: "2026-11-16 09:12", summary: "Lin moves the Europe prices to Monday. Needs your yes before noon.", source: "Gmail", body: BODY };
const draft = (extra: Partial<MailDraft> = {}): MailDraft => ({ draft_id: "d1", conversation_id: "dm_mochi", bot_id: "bot_mochi", run_id: "run_m", to: "lin@example.test", cc: "mia@example.test", subject: "Re: Q3 price table", body: "Hi Lin, Monday works. Please send the new Europe list as a CSV so I can check it against the old one.", demo: false, status: "pending", revision: 1, created_at: at(30), updated_at: at(20), ...extra });
const display = (id: string, label: string, card: ComponentProps<typeof DisplayCard>["card"], extra: Partial<CatalogState> = {}): CatalogState => ({ id, label, kind: "inline", render: () => <DisplayCard card={card} bot={{ id: "bot_mochi", name: "Mochi" }} onDraftReply={async () => true} />, ...extra });
const mailDraft = (id: string, label: string, item: MailDraft, extra: Partial<CatalogState> = {}): CatalogState => ({ id, label, kind: "inline", render: () => <MailDraftCard draft={item} bot={bot("bot_mochi")} group={false} onChanged={async () => {}} />, ...extra });

export const mailCards: CatalogSection = {
  id: "mail", number: "14b", title: "Mail cards (Letter, DraftCard)", context: ["catalog-column"],
  server: isolated({}),
  states: [
    display("mail-in", "Mail read, with the Bot's note", mail),
    display("mail-in-long", "Mail read, folded", { ...mail, body: LONG }, { note: "the fold button opens the rest" }),
    display("mail-text", "Text card", { type: "text", title: "Meeting notes", body: BODY, source: "Notes" }),
    mailDraft("draft-pending", "Draft waiting for Send", draft()),
    mailDraft("draft-demo", "Draft, demo", draft({ demo: true })),
    mailDraft("draft-sent", "Draft, sent", draft({ status: "sent" })),
    mailDraft("draft-declined", "Draft, held back", draft({ status: "declined" })),
  ],
};
