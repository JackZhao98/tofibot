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
