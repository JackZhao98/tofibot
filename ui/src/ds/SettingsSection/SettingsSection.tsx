import type { ReactNode } from "react";
import { cx } from "../cx";
import "./SettingsSection.css";

export type SettingsSectionProps = {
  title: ReactNode;
  /** One line under the title. */
  description?: ReactNode;
  actions?: ReactNode;
  /** The danger zone: a danger edge and title. */
  danger?: boolean;
  id?: string;
  className?: string;
  /** SettingsRow, KeyRow or DataTable. */
  children?: ReactNode;
};

/** A titled group of related settings: title, one line of description, actions, then rows in a hairline card. */
export function SettingsSection({ title, description, actions, danger, id, className, children }: SettingsSectionProps) {
  return <section className={cx("tb-ssec", danger && "tb-ssec--danger", className)} id={id}>
    <header className="tb-ssec-head">
      <div><h2 className="tb-ssec-title">{title}</h2>{description ? <p className="tb-ssec-desc">{description}</p> : null}</div>
      {actions ? <div className="tb-ssec-actions">{actions}</div> : null}
    </header>
    <div className="tb-ssec-body">{children}</div>
  </section>;
}
