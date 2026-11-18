import type { ReactNode } from "react";
import { BrandMark } from "../BrandMark/BrandMark";
import { cx } from "../cx";
import "../shared.css";
import "./DataTable.css";

export type DataRow = { id?: string | number } & Record<string, unknown>;

export type DataColumn<R extends DataRow = DataRow> = {
  key: string;
  label: ReactNode;
  align?: "left" | "right" | "center";
  width?: number | string;
  /** Mono figures, for amounts and counts. */
  mono?: boolean;
  /** The row field that holds an official mark; the cell shows that mark before its content. For outside vendors. */
  logoKey?: string;
  render?: (value: unknown, row: R) => ReactNode;
};

export type DataTableProps<R extends DataRow = DataRow> = {
  /** Six columns at most: move detail into a row page. */
  columns: DataColumn<R>[];
  rows: R[];
  /** Shown under the heads when there are no rows. */
  empty?: ReactNode;
  /** Names the table for screen readers. With it the frame becomes a focusable scroll region, so a keyboard can reach the
   * columns that run off a phone. */
  label?: string;
  className?: string;
};

/** A plain table in a hairline frame: mono uppercase heads, hairline rows, a hover wash. Scrolls sideways when it must. */
export function DataTable<R extends DataRow = DataRow>({ columns, rows, empty, label, className }: DataTableProps<R>) {
  return <div className={cx("tb-table-wrap", className)} {...(label ? { role: "region", "aria-label": label, tabIndex: 0 } : {})}>
    <table className="tb-table">
      <thead><tr>{columns.map(c => <th key={c.key} scope="col" style={{ textAlign: c.align ?? "left", width: c.width }}>{c.label}</th>)}</tr></thead>
      <tbody>{rows.map((r, i) => <tr key={r.id ?? i}>{columns.map(c => {
        const v = r[c.key];
        let cell: ReactNode = c.render ? c.render(v, r) : (v as ReactNode);
        const logo = c.logoKey ? r[c.logoKey] : undefined;
        if (typeof logo === "string" && logo) cell = <span className="tb-cell-brand"><BrandMark src={logo} size={24} />{cell}</span>;
        return <td key={c.key} className={c.mono ? "tb-mono" : undefined} style={{ textAlign: c.align ?? "left" }}>{cell}</td>;
      })}</tr>)}</tbody>
    </table>
    {empty && !rows.length ? <div className="tb-table-empty">{empty}</div> : null}
  </div>;
}
