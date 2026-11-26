import { useRef, useState, type ReactNode } from "react";
import { arrowOpens, OverlayMenu, pressToggle } from "../overlays/OverlayHost";
import { TofiIcon } from "../icons";
import { Button } from "../ds/Button/Button";
import { IconButton } from "../ds/IconButton/IconButton";
import { ServiceMark } from "../ServiceMark";
import { useTranslation } from "../i18n";
import type { ConnectionKind } from "./status";

export type RowMenuItem = { key: string; label: string; danger?: boolean; onSelect: () => void };
/** `pending`: the connection is waiting on the person; the button breathes (a ring, opacity and scale only; static under reduced motion). */
export type RowPrimary = { label: string; onClick?: () => void; href?: string; disabled?: boolean; pending?: boolean };

/** The primary button of a row or a detail page. */
export function PrimaryButton({ primary }: { primary: RowPrimary }) {
  // The continue-sign-in step is a link that opens the provider's page; it wears the kit Button's classes (the kit Button renders a <button> only).
  return primary.href
    ? <a className="tb-btn tb-btn--secondary tb-btn--sm" data-pending={primary.pending || undefined} href={primary.href} target="_blank" rel="noopener noreferrer"><span className="tb-btn-label">{primary.label}</span></a>
    : <Button size="sm" data-primary pending={primary.pending} disabled={primary.disabled} onClick={primary.onClick}>{primary.label}</Button>;
}

/** The "more actions" menu (Radix through the overlay host). */
export function MoreMenu({ name, menu, busy = false }: { name: string; menu: RowMenuItem[]; busy?: boolean }) {
  const { t } = useTranslation("extensions");
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const anchor = useRef<HTMLDivElement>(null);
  const label = t("mcp.more_actions", { name });
  return <div className="mcp-menu-anchor" ref={anchor}>
    <IconButton ref={trigger} className="mcp-menu-trigger" icon="more" label={label} filled={open} aria-haspopup="menu" aria-expanded={open} disabled={busy} {...pressToggle(() => setOpen(value => !value))} onKeyDown={arrowOpens(setOpen)} />
    {open && <OverlayMenu open onOpenChange={setOpen} className="mcp-menu" label={label} trigger={trigger} anchor={anchor} align="end" sideOffset={7}>
      {menu.map(item => <button key={item.key} className={item.danger ? "mcp-menu-danger is-danger" : undefined} onClick={() => { setOpen(false); item.onSelect(); }}>{item.label}</button>)}
    </OverlayMenu>}
  </div>;
}

/**
 * One connection: icon, plain name, where it comes from, one honest status, at most one primary action. The row is the
 * entry to the connection's page (`onOpen`), not a container: nothing expands inline. A row without `onOpen` (a plugin the
 * server has but Tofi does not use yet) keeps a "more actions" menu instead. Pure presentation: MCPRow owns the behaviour.
 */
export function ConnectionRowView({ name, mark, state, kind, statusText, detail, checkedText, source, primary, onOpen, openRef, menu, menuBusy = false, error, children }: {
  name: string; mark?: string; state: string; kind: ConnectionKind; statusText: string; detail?: string; checkedText?: string; source?: string;
  primary?: RowPrimary | null; onOpen?: () => void; openRef?: React.Ref<HTMLButtonElement>; menu?: RowMenuItem[]; menuBusy?: boolean; error?: string; children?: ReactNode;
}) {
  const { t } = useTranslation("extensions");
  const identity = <>
    <ServiceMark name={mark ?? name} />
    <span className="mcp-service-identity">
      <strong>{name}</strong>
      {source && <small className="mcp-service-sourceline">{source}</small>}
      <span className="mcp-service-status" role="status"><i aria-hidden="true" />{statusText}{detail && <small className="mcp-service-detail">{detail}</small>}{checkedText && <small className="mcp-service-checked">{checkedText}</small>}</span>
    </span>
  </>;
  return <article className="mcp-service" aria-label={name} data-state={state} data-kind={kind}>
    <div className="mcp-service-row">
      {onOpen
        ? <button ref={openRef} type="button" className="mcp-service-open" aria-label={t("connections.open", { name })} onClick={onOpen}>{identity}</button>
        : <div className="mcp-service-open">{identity}</div>}
      <div className="mcp-service-actions">
        {primary && <PrimaryButton primary={primary} />}
        {menu && menu.length > 0 && <MoreMenu name={name} menu={menu} busy={menuBusy} />}
        {onOpen && <TofiIcon name="chevron-right" size={16} aria-hidden="true" className="mcp-service-chevron" />}
      </div>
    </div>
    {error && <p className="mcp-service-error" role="alert">{error}</p>}
    {children}
  </article>;
}
