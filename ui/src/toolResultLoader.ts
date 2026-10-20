import type { ToolActivity } from "./types";

// Conversation lists carry only a preview of long tool results. Expanding a
// step asks the app to fetch the full result; the app registers the loader.
let loader: ((activity: ToolActivity) => void) | undefined;

export function setToolResultLoader(next: ((activity: ToolActivity) => void) | undefined) { loader = next; }

/** Call when a step's details are opened. A no-op for results that are already complete. */
export function requestFullToolResult(activity: ToolActivity) {
  if (activity.result_preview) loader?.(activity);
}
