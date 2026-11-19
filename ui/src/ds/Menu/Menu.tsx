import type { ReactNode } from "react";
import { Icon, type IconName } from "../Icon/Icon";
import { cx } from "../cx";
import "../shared.css";
import "../pop.css";
import "./Menu.css";

export type MenuItem =
  | "-"
  | { heading: string }
  | { label: string; icon?: IconName; hint?: string; checked?: boolean; danger?: boolean; disabled?: boolean; active?: boolean; onSelect?: () => void };

export type MenuProps = {
  items: MenuItem[];
  /** The accessible name of the menu. */
  label: string;
  className?: string;
};

/** The classes the product's Radix menus put on their surface (src/overlays/RadixOverlays.tsx): one look for the static kit
 *  Menu and for every DropdownMenu, so a menu is drawn in one place. `tofi-pop` is added beside it by the adapters: the hook the tests read. */
export const MENU_SURFACE = "tb-pop tb-glass tb-menu";
export const MENU_ITEM = "tb-menu-item";

/** A glass menu: icon, label, a key hint or a tick. The row under the pointer turns solid ink. Put destructive items last,
 *  after a separator, and keep a menu to about eight items. The product's menus are Radix DropdownMenus wearing the same
 *  classes (OverlayMenu); this component draws the same thing for the catalog and for static use. */
export function Menu({ items, label, className }: MenuProps): ReactNode {
  return <div className={cx("tb-pop", "tb-glass", "tb-menu", "tb-menu--static", className)} role="menu" aria-label={label}>
    {items.map((item, index) => {
      if (item === "-") return <div key={index} className="tb-menu-sep" role="separator" />;
      if ("heading" in item) return <div key={index} className="tb-menu-heading">{item.heading}</div>;
      return <button key={index} type="button" role="menuitem" className={cx(MENU_ITEM, item.danger && "is-danger", item.active && "is-active")} disabled={item.disabled} onClick={item.onSelect}>
        {item.icon ? <Icon name={item.icon} size={18} /> : <span className="tb-menu-gap" />}
        <span className="tb-menu-label">{item.label}</span>
        {item.hint ? <span className="tb-menu-hint">{item.hint}</span> : item.checked ? <Icon name="check" size={16} /> : null}
      </button>;
    })}
  </div>;
}
