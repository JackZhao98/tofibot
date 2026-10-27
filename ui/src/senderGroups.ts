/**
 * Sender groups in a group chat: consecutive Bot units (messages, Activity rows, live status) from the
 * same Bot within a short window read as one block, with the avatar and name shown once on its first
 * unit. Units carry `data-sender` (Bot id) and `data-at` (ISO time); everything that is not a Bot unit
 * (a person's message, a notice, a question or draft card, a date or unread divider) ends the block.
 * `data-continues` marks later units; `data-last` marks the unit that closes a group. The name sits above the group's first unit,
 * the avatar beside its last unit (chat-app convention). `data-speaker-change` marks any message whose speaker differs from the
 * one before it (a person or a Bot), which gets more room above it.
 */
export const SENDER_GROUP_WINDOW_MS = 5 * 60_000;

const BREAKERS = ".date-divider, .unread-divider, article.message:not(.message-bot), .message-ui-card, [data-question-id], [data-draft-id], .binary-question-message, .question-message, .mail-draft-message";

export function markSenderGroups(root: ParentNode | null): void {
  if (!root) return;
  const units: HTMLElement[] = [];
  let sender = "";
  let at = NaN;
  for (const element of root.querySelectorAll<HTMLElement>(`[data-sender], ${BREAKERS}`)) {
    if (!element.hasAttribute("data-sender")) {
      // A breaker nested inside a unit (a question record in a task block) does not end the unit's own block.
      if (!element.parentElement?.closest("[data-sender]")) sender = "";
      continue;
    }
    if (element.parentElement?.closest("[data-sender]")) continue;
    const id = element.dataset.sender ?? "";
    const time = Date.parse(element.dataset.at ?? "");
    const continues = Boolean(id) && id === sender && Number.isFinite(time) && Number.isFinite(at) && Math.abs(time - at) < SENDER_GROUP_WINDOW_MS;
    if (continues) { if (element.dataset.continues !== "true") element.dataset.continues = "true"; }
    else if (element.hasAttribute("data-continues")) element.removeAttribute("data-continues");
    sender = id;
    at = Number.isFinite(time) ? time : at;
    units.push(element);
  }
  markSpeakerChanges(root);
  markBubbleRuns(root);
  // The avatar sits beside the group's last unit: the one the next unit does not continue.
  units.forEach((element, index) => {
    const last = !units[index + 1] || units[index + 1].dataset.continues !== "true";
    if (last) { if (element.dataset.last !== "true") element.dataset.last = "true"; }
    else if (element.hasAttribute("data-last")) element.removeAttribute("data-last");
  });
}

const SPEAKER_UNITS = "article.message, [data-sender]";

/** Marks messages whose speaker differs from the previous message's, so the gap between speakers can be wider than within one. */
function markSpeakerChanges(root: ParentNode): void {
  let previous: string | undefined;
  for (const element of root.querySelectorAll<HTMLElement>(`${SPEAKER_UNITS}, .date-divider`)) {
    if (element.matches(".date-divider")) { previous = undefined; continue; }
    if (element.parentElement?.closest(SPEAKER_UNITS)) continue;
    const speaker = element.dataset.sender || (element.matches(".message-user") ? "user" : element.matches(".message-bot") ? "bot" : "other");
    const change = previous !== undefined && speaker !== previous;
    if (change) { if (element.dataset.speakerChange !== "true") element.dataset.speakerChange = "true"; }
    else if (element.hasAttribute("data-speaker-change")) element.removeAttribute("data-speaker-change");
    previous = speaker;
  }
}

/**
 * Bubble runs: consecutive plain bubbles from one speaker (`data-from`: "user", a Bot id, or "bot") within the group window,
 * with nothing rendered between them, sit closer and merge their facing corners (chat-app convention). A block joins the one
 * before it only when both blocks hold nothing but the bubble host and the actions row (a divider, tool row, task card, owner node,
 * reaction chips or an error note in either block breaks the run) and the blocks are adjacent siblings. Sets `data-joins-prev` /
 * `data-joins-next` on the `.message-block`; attribute writes only, when changed, no layout reads.
 */
const NOT_BUBBLE = ".message-ui-card, .message-notice, .message-assignment";

function runUnit(block: Element): { from: string; at: number; open: boolean } | undefined {
  // `open`: nothing follows the bubble in its block (no task block, owner node, tool row, reaction chips), so a next bubble may join it.
  // Anything before the bubble host (a date or unread divider) keeps the block from joining the one above.
  let open = true;
  for (const child of block.children) {
    if (child.classList.contains("message-hover-host") || child.classList.contains("message-actions-row")) continue;
    if (!block.firstElementChild?.classList.contains("message-hover-host")) return undefined;
    open = false;
  }
  if (!block.firstElementChild?.classList.contains("message-hover-host")) return undefined;
  const article = block.querySelector<HTMLElement>(":scope > .message-hover-host > article.message[data-from]");
  if (!article || article.matches(NOT_BUBBLE) || article.querySelector(".message-tools")) return undefined;
  return { from: article.dataset.from ?? "", at: Date.parse(article.dataset.at ?? ""), open };
}

function setFlag(element: HTMLElement, name: "joinsPrev" | "joinsNext", on: boolean): void {
  if (on) { if (element.dataset[name] !== "true") element.dataset[name] = "true"; }
  else if (element.dataset[name] !== undefined) delete element.dataset[name];
}

function markBubbleRuns(root: ParentNode): void {
  let previous: { block: HTMLElement; from: string; at: number; open: boolean } | undefined;
  for (const block of root.querySelectorAll<HTMLElement>(".message-block")) {
    const unit = runUnit(block);
    const joins = Boolean(unit && previous?.open && previous.block === block.previousElementSibling && unit.from && unit.from === previous.from
      && Number.isFinite(unit.at) && Number.isFinite(previous.at) && Math.abs(unit.at - previous.at) < SENDER_GROUP_WINDOW_MS);
    setFlag(block, "joinsPrev", joins);
    if (previous) setFlag(previous.block, "joinsNext", joins);
    previous = unit ? { block, ...unit } : undefined;
    if (!unit) setFlag(block, "joinsNext", false);
  }
  // Nothing follows the list's last block, so it never joins forward.
  if (previous) setFlag(previous.block, "joinsNext", false);
}
