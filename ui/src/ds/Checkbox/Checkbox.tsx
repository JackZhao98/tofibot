import type { InputHTMLAttributes, ReactNode, Ref } from "react";
import { Icon } from "../Icon/Icon";
import { cx } from "../cx";
import "./Checkbox.css";

export type CheckboxProps = Omit<InputHTMLAttributes<HTMLInputElement>, "type" | "onChange" | "children" | "className"> & {
  /** Called with the new value. */
  onChange?: (checked: boolean) => void;
  /** Goes on the label; every other attribute goes on the input. */
  className?: string;
  ref?: Ref<HTMLInputElement>;
  /** The label. */
  children?: ReactNode;
};

/** A square checkbox that fills tomato when checked. It is the native input (keyboard, forms and screen readers come with
 *  it); there is no Radix Checkbox package in the repo. For a setting that applies at once use Switch. */
export function Checkbox({ onChange, className, children, disabled, ...rest }: CheckboxProps) {
  return <label className={cx("tb-check", disabled && "is-disabled", className)}>
    <input {...rest} type="checkbox" disabled={disabled} onChange={event => onChange?.(event.target.checked)} />
    <span className="tb-check-box" aria-hidden="true"><Icon name="check" size={14} /></span>
    {children ? <span className="tb-check-label">{children}</span> : null}
  </label>;
}
