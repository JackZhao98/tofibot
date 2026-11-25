import * as RadixSwitch from "@radix-ui/react-switch";
import type { ReactNode } from "react";
import { cx } from "../cx";
import "./DriverSwitch.css";

export type DriverSwitchProps = {
  /** Who holds the mouse: the Bot (its cat) or you. */
  driver: "bot" | "you";
  /** The Bot's cat, small. */
  bot: ReactNode;
  /** The word for your side. */
  youLabel: string;
  /** The accessible name and the hover hint: the action the flip performs. */
  label: string;
  /** The computer is not up yet: the thumb goes grey. */
  idle?: boolean;
  disabled?: boolean;
  /** Flipping to You takes control; flipping back hands it back. */
  onToggle?: () => void;
  className?: string;
};

/** Who holds the mouse on the Bot computer: a two-sided switch whose thumb springs to the Bot's cat or to you (Radix Switch).
 *  It lives in the chin of a ScreenPanel and is the device's status: no Take control button beside it. Not for settings (use Switch). */
export function DriverSwitch({ driver, bot, youLabel, label, idle, disabled, onToggle, className }: DriverSwitchProps) {
  const you = driver === "you";
  return <RadixSwitch.Root checked={you} onCheckedChange={() => onToggle?.()} disabled={disabled} aria-label={label} title={label} data-hint={label}
    className={cx("tb-driver", you ? "is-you" : "is-bot", idle && "is-idle", className)}>
    <span className="tb-driver-thumb" aria-hidden="true" />
    <span className="tb-driver-side is-bot" aria-hidden="true">{bot}</span>
    <span className="tb-driver-side is-you" aria-hidden="true">{youLabel}</span>
  </RadixSwitch.Root>;
}
