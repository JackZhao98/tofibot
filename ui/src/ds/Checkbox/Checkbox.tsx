import { useEffect, useRef, type InputHTMLAttributes, type ReactNode, type Ref } from "react";
import { Icon } from "../Icon/Icon";
import { cx } from "../cx";
import "./Checkbox.css";

export type CheckboxProps = Omit<InputHTMLAttributes<HTMLInputElement>, "type" | "onChange" | "children" | "className"> & {
  /** Called with the new value. */
  onChange?: (checked: boolean) => void;
  /** Goes on the label; every other attribute goes on the input. */
  className?: string;
  ref?: Ref<HTMLInputElement>;
  /** Some of a group are chosen (a group box over its items): drawn as a dash until it is checked. */
  mixed?: boolean;
  /** The label. */
  children?: ReactNode;
};

/** A square checkbox that fills tomato when checked. It is the native input (keyboard, forms and screen readers come with
 *  it); there is no Radix Checkbox package in the repo. For a setting that applies at once use Switch. */
export function Checkbox({ onChange, className, children, disabled, mixed, ref, ...rest }: CheckboxProps) {
  const own = useRef<HTMLInputElement | null>(null);
  useEffect(() => { if (own.current) own.current.indeterminate = Boolean(mixed) && !rest.checked; }, [mixed, rest.checked]);
  const setRef = (element: HTMLInputElement | null) => { own.current = element; if (typeof ref === "function") ref(element); else if (ref) ref.current = element; };
  return <label className={cx("tb-check", disabled && "is-disabled", className)}>
    <input {...rest} ref={setRef} type="checkbox" disabled={disabled} onChange={event => onChange?.(event.target.checked)} />
    <span className="tb-check-box" aria-hidden="true"><Icon name={mixed && !rest.checked ? "minus" : "check"} size={14} /></span>
    {children ? <span className="tb-check-label">{children}</span> : null}
  </label>;
}
