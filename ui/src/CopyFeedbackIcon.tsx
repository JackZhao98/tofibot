import { TofiIcon } from "./icons";
import "./copy-feedback.css";

/** Decorative Lab copy-to-check glyph; clipboard behavior stays with the caller. */
export function CopyFeedbackIcon({ copied, size = 16 }: { copied: boolean; size?: number }) {
  return <span className={`copy-feedback${copied ? " is-copied" : ""}`} style={{ width:size, height:size }} aria-hidden="true">
    <TofiIcon name="copy" size={size} />
    <TofiIcon name="check" size={size} variant="filled" />
  </span>;
}
