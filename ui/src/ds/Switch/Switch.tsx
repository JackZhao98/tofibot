import * as RadixSwitch from "@radix-ui/react-switch";
import { cx } from "../cx";
import "./Switch.css";

export type SwitchProps = {
  checked?: boolean;
  defaultChecked?: boolean;
  /** Called with the new value. */
  onChange?: (on: boolean) => void;
  /** The accessible name when no visible label points at the switch. */
  label?: string;
  /** The id of the element that names the switch. */
  labelledBy?: string;
  disabled?: boolean;
  /** A change is being saved: the pointer shows progress. */
  busy?: boolean;
  id?: string;
  className?: string;
};

/** On or off, and it takes effect at once (Radix Switch). Teal when on, with a spring thumb. A choice that needs Save is a Checkbox. */
export function Switch({ checked, defaultChecked, onChange, label, labelledBy, disabled, busy, id, className }: SwitchProps) {
  return <RadixSwitch.Root id={id} className={cx("tb-switch", className)} checked={checked} defaultChecked={defaultChecked} disabled={disabled}
    aria-label={label} aria-labelledby={labelledBy} aria-busy={busy ? true : undefined} onCheckedChange={onChange}>
    <RadixSwitch.Thumb className="tb-switch-thumb" />
  </RadixSwitch.Root>;
}
