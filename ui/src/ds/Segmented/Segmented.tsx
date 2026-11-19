import * as RadioGroup from "@radix-ui/react-radio-group";
import type { ReactNode } from "react";
import { Icon, type IconName } from "../Icon/Icon";
import { cx } from "../cx";
import "./Segmented.css";

export type SegmentedOption<T extends string> = T | { value: T; label: ReactNode; icon?: IconName };
export type SegmentedProps<T extends string> = {
  /** Two to four options; more than that is a Select. */
  options: readonly SegmentedOption<T>[];
  value?: T;
  defaultValue?: T;
  onChange?: (value: T) => void;
  /** The accessible name of the group. */
  label?: string;
  disabled?: boolean;
  className?: string;
};

/** Mutually exclusive options in one track; the chosen one rises with an edge (Radix RadioGroup: arrows move and choose). */
export function Segmented<T extends string>({ options, value, defaultValue, onChange, label, disabled, className }: SegmentedProps<T>) {
  return <RadioGroup.Root className={cx("tb-seg", className)} value={value} defaultValue={defaultValue}
    onValueChange={next => onChange?.(next as T)} disabled={disabled} aria-label={label} orientation="horizontal">
    {options.map(option => {
      const item: { value: T; label: ReactNode; icon?: IconName } = typeof option === "string" ? { value: option, label: option } : option;
      return <RadioGroup.Item key={item.value} value={item.value} className="tb-seg-opt">
        {item.icon ? <Icon name={item.icon} size={16} /> : null}<span>{item.label}</span>
      </RadioGroup.Item>;
    })}
  </RadioGroup.Root>;
}
