import { TofiIcon } from "./icons";
import { LiveMark } from "./LiveMark";

/**
 * A step's state as a small icon (done, running, problem, other) with its word as the accessible name. The word is
 * the existing `step.state.*` string, so nothing new to translate. Used by both Activity lists.
 */
const PROBLEM = new Set(["failed", "not_executed", "interrupted"]);
export function ToolStatusIcon({ state, label }: { state: string; label: string }) {
  if (state === "running" || state === "queued") return <span className="tool-status is-live" role="img" aria-label={label}><LiveMark /></span>;
  const name = state === "completed" || state === "approved" || state === "reviewed" ? "check" : PROBLEM.has(state) ? "alert" : "clock";
  return <span className={`tool-status is-${PROBLEM.has(state) ? "problem" : state === "completed" || state === "approved" ? "done" : "other"}`} role="img" aria-label={label}><TofiIcon name={name} size={14} aria-hidden="true" /></span>;
}
