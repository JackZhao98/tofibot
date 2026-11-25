import * as RadixTabs from "@radix-ui/react-tabs";
import { useState, type ReactNode } from "react";
import { cx } from "../cx";
import "./Tabs.css";

export type TabItem<T extends string> = { value: T; label: ReactNode; count?: number };
export type TabsProps<T extends string> = {
  tabs: readonly TabItem<T>[];
  value?: T;
  defaultValue?: T;
  onChange?: (value: T) => void;
  /** The panel of the chosen tab, or a function of its value. Leave out for a bare strip. */
  children?: ReactNode | ((value: T) => ReactNode);
  /** The accessible name of the tab strip. */
  label?: string;
  /** A 36px strip of 12px tabs that shorten with an ellipsis: browser tabs, a toolbar row. */
  compact?: boolean;
  className?: string;
};

/** Text tabs on a hairline rule; the chosen tab gets a tomato bar (Radix Tabs). Views of one object, never navigation between pages. */
export function Tabs<T extends string>({ tabs, value, defaultValue, onChange, children, label, compact, className }: TabsProps<T>) {
  const [own, setOwn] = useState<T | undefined>(defaultValue ?? tabs[0]?.value);
  const current = value ?? own;
  const change = (next: string) => { setOwn(next as T); onChange?.(next as T); };
  return <RadixTabs.Root className={cx("tb-tabs", compact && "tb-tabs--compact", className)} value={current} onValueChange={change}>
    <RadixTabs.List className="tb-tablist" aria-label={label}>
      {tabs.map(tab => <RadixTabs.Trigger key={tab.value} value={tab.value} className="tb-tab">
        {tab.label}{tab.count != null ? <span className="tb-tab-count">{tab.count}</span> : null}
      </RadixTabs.Trigger>)}
    </RadixTabs.List>
    {children && current !== undefined ? <RadixTabs.Content value={current} className="tb-tabpanel">{typeof children === "function" ? children(current) : children}</RadixTabs.Content> : null}
  </RadixTabs.Root>;
}
