import * as RadioGroup from "@radix-ui/react-radio-group";
import { cx } from "../cx";
import "./SwatchPicker.css";

/** The hues a Bot may take. Blue is the person's and is never offered. */
export const BOT_HUES = ["tomato", "mustard", "teal", "grape", "lime"] as const;
export type BotHue = (typeof BOT_HUES)[number];
export type SwatchOption<T extends string> = T | { value: T; label: string };

export type SwatchPickerProps<T extends string = BotHue> = {
  /** The accessible name of the group. */
  label: string;
  value?: T;
  defaultValue?: T;
  onChange?: (value: T) => void;
  /** Hue names, or hues with the name to announce. */
  options?: readonly SwatchOption<T>[];
  disabled?: boolean;
  className?: string;
};

/** A Bot's colour: round swatches from the palette, one ringed (Radix RadioGroup: arrows move and choose). The colour is the Bot's
 *  avatar ground and follows it everywhere. No free colour input: the hues keep contrast in both themes. */
export function SwatchPicker<T extends string = BotHue>({ label, value, defaultValue, onChange, options, disabled, className }: SwatchPickerProps<T>) {
  const list = (options ?? BOT_HUES) as readonly SwatchOption<T>[];
  return <RadioGroup.Root className={cx("tb-swatches", className)} value={value} defaultValue={defaultValue} onValueChange={next => onChange?.(next as T)} disabled={disabled} aria-label={label} orientation="horizontal">
    {list.map(option => {
      const item = typeof option === "string" ? { value: option, label: option } : option;
      return <RadioGroup.Item key={item.value} value={item.value} aria-label={item.label} title={item.label} data-hue={item.value} className={cx("tb-swatch", `tb-swatch--${item.value}`)} />;
    })}
  </RadioGroup.Root>;
}
