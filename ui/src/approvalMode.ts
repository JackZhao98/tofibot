/**
 * The one approval control on Settings > Permissions, mapped onto the server's AutoReview modes
 * (`/api/auto-review-settings`). "Always allow" is not a mode: it is a per-Bot, per-tool allowance
 * given on an approval card (Advanced > Tool rules), so it is deliberately not offered here.
 */
export type ReviewMode = "off" | "shadow" | "auto";
export type ApprovalChoice = "auto" | "ask";
export const approvalChoices: readonly ApprovalChoice[] = ["auto", "ask"];

/** Auto review decides low- and medium-risk calls; everything else (off, observe-only) asks a person first. */
export const approvalChoiceFor = (mode: ReviewMode): ApprovalChoice => (mode === "auto" ? "auto" : "ask");

/** The mode to store for a choice. "Ask" keeps an Observe setting made under Advanced; otherwise it is Off. */
export const reviewModeFor = (choice: ApprovalChoice, current: ReviewMode): ReviewMode => (choice === "auto" ? "auto" : current === "shadow" ? "shadow" : "off");
