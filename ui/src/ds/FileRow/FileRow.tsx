import type { ButtonHTMLAttributes } from "react";
import { FileGlyph, FileMain, type FileKind } from "../FileTile/parts";
import { cx } from "../cx";
import "./FileRow.css";

export type FileRowProps = Omit<ButtonHTMLAttributes<HTMLButtonElement>, "children" | "onClick"> & {
  name: string;
  kind?: FileKind;
  ext?: string;
  /** A picture's thumbnail, instead of the glyph. */
  thumb?: string;
  meta?: string;
  /** A file that just arrived: a mustard bar at the edge. */
  fresh?: boolean;
  /** Said to screen readers for a fresh row ("New"). */
  freshLabel?: string;
  onOpen?: () => void;
};

/** A one-line file in a list: Artifacts widget, sidebar lists, pickers. Not for messages: use ArtifactCard or FileTile. */
export function FileRow({ name, kind, ext, thumb, meta, fresh, freshLabel, onOpen, className, type = "button", ...rest }: FileRowProps) {
  return <button {...rest} type={type} className={cx("tb-filerow", fresh && "is-fresh", className)} onClick={onOpen}>
    {thumb ? <img className="tb-filerow-thumb" src={thumb} alt="" /> : <FileGlyph kind={kind} ext={ext} />}
    <FileMain name={name} meta={meta} />
    {fresh && freshLabel ? <span className="tb-filerow-sr">{freshLabel}</span> : null}
  </button>;
}
