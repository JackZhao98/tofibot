import type { InputHTMLAttributes, ReactNode, Ref } from "react";
import { Icon, type IconName } from "../Icon/Icon";
import { cx } from "../cx";
import "../control.css";
import "./TextInput.css";

export type TextInputProps = Omit<InputHTMLAttributes<HTMLInputElement>, "prefix"> & {
  /** A leading icon. */
  icon?: IconName;
  /** Trailing text or a small node (a unit, a hint). */
  suffix?: ReactNode;
  /** Ids, URLs and keys. */
  mono?: boolean;
  /** `className` goes on the frame; every other attribute goes on the input. */
  ref?: Ref<HTMLInputElement>;
};

/** A pressed-in single-line field. Long text goes in TextArea. Wrap it in Field for a label. */
export function TextInput({ icon, suffix, mono, className, type = "text", ...rest }: TextInputProps) {
  return <div className={cx("tb-input", mono && "tb-input--mono", className)}>
    {icon && <Icon name={icon} size={16} />}
    <input {...rest} type={type} />
    {suffix ? <span className="tb-input-suffix">{suffix}</span> : null}
  </div>;
}
