import { useRef, useState, type ReactNode } from "react";
import { arrowOpens, OverlayMenu } from "../overlays/OverlayHost";
import { TofiIcon } from "../icons";
import { ServiceMark } from "../ServiceMark";
import { useTranslation } from "../i18n";
import type { ConnectionKind } from "./status";

export type RowMenuItem = { key: string; label: string; danger?: boolean; onSelect: () => void };
export type RowPrimary = { label: string; onClick?: () => void; href?: string; disabled?: boolean };

/**
 * One connection: icon, plain name, one honest status, at most one primary action and a "more actions" menu.
 * Anything else a connection has (its tools, a pending confirmation, setup help after a failure) is passed in as a
 * child and appears only when it applies. Pure presentation: MCPRow owns the behaviour.
 */
export function ConnectionRowView({ name, mark, state, kind, statusText, detail, checkedText, primary, menu, menuBusy = false, error, children }: {
  name: string; mark?: string; state: string; kind: ConnectionKind; statusText: string; detail?: string; checkedText?: string;
  primary?: RowPrimary | null; menu: RowMenuItem[]; menuBusy?: boolean; error?: string; children?: ReactNode;
}) {
  const { t } = useTranslation("extensions");
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const anchor = useRef<HTMLDivElement>(null);
  const label = t("mcp.more_actions", { name });
  return <article className="mcp-service" aria-label={name} data-state={state} data-kind={kind}>
    <div className="mcp-service-row">
      <ServiceMark name={mark ?? name} />
      <div className="mcp-service-identity">
        <strong>{name}</strong>
        <span className="mcp-service-status" role="status"><i aria-hidden="true" />{statusText}{detail && <small className="mcp-service-detail">{detail}</small>}{checkedText && <small className="mcp-service-checked">{checkedText}</small>}</span>
      </div>
      <div className="mcp-service-actions">
        {primary && (primary.href
          ? <a className="mcp-button" href={primary.href} target="_blank" rel="noopener noreferrer">{primary.label}</a>
          : <button type="button" className="mcp-button" data-primary disabled={primary.disabled} onClick={primary.onClick}>{primary.label}</button>)}
        <div className="mcp-menu-anchor" ref={anchor}>
          <button ref={trigger} type="button" className="mcp-button mcp-menu-trigger" aria-label={label} aria-haspopup="menu" aria-expanded={open} disabled={menuBusy} onClick={() => setOpen(value => !value)} onKeyDown={arrowOpens(setOpen)}>
            <TofiIcon name="more" size={20} variant={open ? "filled" : "outline"} />
          </button>
          {open && <OverlayMenu open onOpenChange={setOpen} className="mcp-menu" label={label} trigger={trigger} anchor={anchor} align="end" sideOffset={7}>
            {menu.map(item => <button key={item.key} className={item.danger ? "mcp-menu-danger" : undefined} onClick={() => { setOpen(false); item.onSelect(); }}>{item.label}</button>)}
          </OverlayMenu>}
        </div>
      </div>
    </div>
    {error && <p className="mcp-service-error" role="alert">{error}</p>}
    {children}
  </article>;
}
