import type { MouseEventHandler } from "react";
import { useTranslation } from "../../i18n";
import { IconButton } from "../IconButton/IconButton";
import { cx } from "../cx";
import { FileGlyph, FileMain, FilePreview, type FileKind, type FilePreviewData } from "./parts";
import "./FileTile.css";

export type FileTileProps = {
  name: string;
  kind?: FileKind;
  /** The extension shown on the glyph; defaults to the kind. */
  ext?: string;
  /** Size, pages or rows. */
  meta?: string;
  /** Give it whenever the file can be previewed; without it the tile is the compact row. */
  preview?: FilePreviewData;
  onOpen?: MouseEventHandler<HTMLButtonElement>;
  onDownload?: MouseEventHandler<HTMLButtonElement>;
  openLabel?: string;
  downloadLabel?: string;
  className?: string;
};

/** A file in a message. A previewable file is a preview card; anything else a compact 340px row. Pictures are AttachmentGrid. */
export function FileTile({ name, kind = "text", ext, meta, preview, onOpen, onDownload, openLabel, downloadLabel, className }: FileTileProps) {
  const { t } = useTranslation();
  const glyph = <FileGlyph kind={kind} ext={ext} />;
  const main = <FileMain name={name} meta={meta} />;
  const download = onDownload ? <IconButton icon="download" label={`${downloadLabel ?? t("ds.file.download")} ${name}`} size="sm" onClick={onDownload} /> : null;
  if (!preview) return <div className={cx("tb-file", className)}>{glyph}{main}{download}</div>;
  return <div className={cx("tb-filecard", `tb-filecard--${preview.type}`, className)}>
    <button type="button" className="tb-filecard-view" aria-label={`${openLabel ?? t("ds.file.open")} ${name}`} onClick={onOpen}><FilePreview preview={preview} name={name} /></button>
    <div className="tb-filecard-foot">{glyph}{main}{download}</div>
  </div>;
}
