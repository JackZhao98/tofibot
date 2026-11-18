import type { TextareaHTMLAttributes } from "react";
import { cx } from "../cx";
import "../control.css";
import "./TextArea.css";

export type TextAreaProps = TextareaHTMLAttributes<HTMLTextAreaElement>;

/** A multi-line field with the same pressed-in look. One-line values go in TextInput. */
export function TextArea({ className, rows = 3, ...rest }: TextAreaProps) {
  return <textarea {...rest} rows={rows} className={cx("tb-textarea", className)} />;
}
