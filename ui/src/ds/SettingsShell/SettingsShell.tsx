import { useEffect, useRef, type ReactNode, type Ref } from "react";
import { Icon, type IconName } from "../Icon/Icon";
import { cx } from "../cx";
import "../shared.css";
import "../SidebarRow/SidebarRow.css";
import "./SettingsShell.css";

export type SettingsShellItem = {
  value: string;
  label: ReactNode;
  icon?: IconName;
  /** A count of what waits for the person (a number, not a status). */
  badge?: ReactNode;
  /** The badge's accessible name. */
  badgeLabel?: string;
};
export type SettingsShellGroup = {
  id: string;
  /** One word. */
  label?: ReactNode;
  items: readonly SettingsShellItem[];
};
/** Extra class names on the parts, for the surfaces that keep older hooks (tests, sheets that still place something here). */
export type SettingsShellHooks = Partial<Record<"root" | "nav" | "group" | "groupLabel" | "link" | "linkLabel" | "badge" | "head" | "headText" | "title" | "actions" | "main" | "content", string>>;

export type SettingsShellProps = {
  groups: readonly SettingsShellGroup[];
  active: string;
  onNavigate: (value: string) => void;
  navLabel: string;
  /** The group's name above the title. */
  crumb?: ReactNode;
  title: ReactNode;
  /** A quiet word beside the title (beta, in development). */
  tag?: ReactNode;
  /** The page's state in words: Saved, Unsaved changes. Announced politely. */
  status?: ReactNode;
  /** Buttons for the head row. The shell also gives `actionsRef` for a page that places its own through a portal. */
  actions?: ReactNode;
  actionsRef?: Ref<HTMLDivElement>;
  hooks?: SettingsShellHooks;
  className?: string;
  children?: ReactNode;
};

/**
 * A full settings page: a rail of grouped links, a head row (group name, title, status, actions) and the content. At 700 px and
 * narrower the rail is one row of links above the content and the group names go. The links are buttons, because a page that holds
 * unsaved edits has to ask before it navigates; an item that is a real route can wrap this and render its own anchors later.
 */
export function SettingsShell({ groups, active, onNavigate, navLabel, crumb, title, tag, status, actions, actionsRef, hooks = {}, className, children }: SettingsShellProps) {
  const rail = useRef<HTMLElement>(null);
  // Narrow: the strip scrolls sideways, and the chosen link comes into view.
  useEffect(() => {
    const strip = rail.current;
    const on = strip?.querySelector<HTMLElement>('[aria-current="page"]');
    if (!strip || !on || strip.scrollWidth <= strip.clientWidth) return;
    strip.scrollLeft = Math.max(0, on.offsetLeft - (strip.clientWidth - on.offsetWidth) / 2);
  }, [active]);
  return <div className={cx("tb-settings", hooks.root, className)}>
    <nav ref={rail} className={cx("tb-settings-rail", hooks.nav)} aria-label={navLabel}>
      {groups.map(group => <div key={group.id} className={cx("tb-settings-group", hooks.group)} data-group={group.id}>
        {group.label ? <div className={cx("tb-settings-grouplabel", hooks.groupLabel)}>{group.label}</div> : null}
        {group.items.map(item => {
          const on = item.value === active;
          return <button key={item.value} type="button" data-tab={item.value} className={cx("tb-settings-link", on && "is-active", hooks.link)} aria-current={on ? "page" : undefined} onClick={() => onNavigate(item.value)}>
            {item.icon ? <Icon name={item.icon} size={18} variant={on ? "filled" : "outline"} /> : null}
            <span className={cx("tb-settings-linklabel", hooks.linkLabel)}>{item.label}</span>
            {item.badge ? <span className={cx("tb-count", hooks.badge)} aria-label={item.badgeLabel}>{item.badge}</span> : null}
          </button>;
        })}
      </div>)}
    </nav>
    <main className={cx("tb-settings-main", hooks.main)}>
      <header className={cx("tb-settings-head", hooks.head)}>
        <div className={cx("tb-settings-headtext", hooks.headText)}>
          {crumb ? <div className="tb-eyebrow">{crumb}</div> : null}
          <h2 className={cx("tb-settings-title", hooks.title)}>{title}{tag ? <span className="tb-settings-tag">{tag}</span> : null}</h2>
        </div>
        <div className="tb-settings-status" role="status">{status}</div>
        <div className={cx("tb-settings-actions", hooks.actions)} ref={actionsRef}>{actions}</div>
      </header>
      <div className={cx("tb-settings-content", hooks.content)}>{children}</div>
    </main>
  </div>;
}
