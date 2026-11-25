import { Children, cloneElement, useId, type ReactElement, type ReactNode } from "react";
import { Icon } from "../Icon/Icon";
import { cx } from "../cx";
import "./Field.css";

type ControlProps = { id?: string; "aria-invalid"?: boolean; "aria-describedby"?: string };

export type FieldProps = {
  label: ReactNode;
  /** Only when the field is unfamiliar or risky; one line. */
  hint?: ReactNode;
  error?: ReactNode;
  /** The word beside the label for an optional field. */
  optional?: string;
  id?: string;
  className?: string;
  /** Exactly one control (V3 PLAN S17): TextInput, TextArea or Select. */
  children: ReactElement<ControlProps>;
};

/** A label, one control, and a hint or an error under it, wired for screen readers. */
export function Field({ label, hint, error, optional, id, className, children }: FieldProps) {
  const generated = useId();
  const controlId = id ?? `tb-f${generated}`;
  const message = error ? "error" : hint ? "hint" : null;
  const own = Children.only(children).props["aria-describedby"];
  const describedBy = message ? `${controlId}-d` : undefined;
  const merged = [own, describedBy].filter(Boolean).join(" ") || undefined;
  return <div className={cx("tb-field", error ? "is-invalid" : false, className)}>
    <label className="tb-field-label" htmlFor={controlId}>{label}{optional ? <span className="tb-field-opt">{optional}</span> : null}</label>
    {cloneElement(Children.only(children), { id: controlId, "aria-invalid": error ? true : undefined, "aria-describedby": merged })}
    {error ? <div className="tb-field-msg tb-field-msg--error" id={describedBy}><Icon name="error" size={14} />{error}</div>
      : hint ? <div className="tb-field-msg" id={describedBy}>{hint}</div> : null}
  </div>;
}
