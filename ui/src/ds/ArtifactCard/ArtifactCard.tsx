import type { MouseEventHandler, ReactNode } from "react";
import { useTranslation } from "../../i18n";
import { Icon } from "../Icon/Icon";
import { IconButton } from "../IconButton/IconButton";
import { FileGlyph, FileMain, FilePreview, type FileKind, type FilePreviewData } from "../FileTile/parts";
import { cx } from "../cx";
import "../FileTile/FileTile.css";
import "./ArtifactCard.css";

export type ArtifactCardProps = {
  name: string;
  kind?: FileKind;
  ext?: string;
  meta?: string;
  /** As FileTile. Without it the card shows the big file glyph. */
  preview?: FilePreviewData;
  /** The stamp on the preview; defaults to "ARTIFACT". null for none. */
  tag?: string | null;
  /** "v3". */
  version?: string;
  /** Who made it, on the Artifacts cover. */
  from?: ReactNode;
  onGoto?: MouseEventHandler<HTMLButtonElement>;
  gotoLabel?: string;
  onOpen?: MouseEventHandler<HTMLButtonElement>;
  onDownload?: MouseEventHandler<HTMLButtonElement>;
  openLabel?: string;
  downloadLabel?: string;
  /** Fill the width (lists); in a message it is 224px. */
  wide?: boolean;
  className?: string;
};

/** A Bot's deliverable: a preview card with the stamp, the version and, in lists, who made it and a jump to the message. */
export function ArtifactCard({ name, kind, ext, meta, preview, tag, version, from, onGoto, gotoLabel, onOpen, onDownload, openLabel, downloadLabel, wide, className }: ArtifactCardProps) {
  const { t } = useTranslation();
  const stamp = tag === undefined ? t("ds.artifact.tag") : tag;
  return <div className={cx("tb-art", wide && "tb-art--wide", className)}>
    <button type="button" className="tb-filecard-view tb-art-view" aria-label={`${openLabel ?? t("ds.file.open")} ${name}`} onClick={onOpen}>
      {preview ? <FilePreview preview={preview} name={name} /> : <span className="tb-art-blank"><FileGlyph kind={kind} ext={ext} big /></span>}
      {stamp ? <span className="tb-art-tag">{stamp}</span> : null}
      {version ? <span className="tb-art-ver">{version}</span> : null}
    </button>
    <div className="tb-filecard-foot">
      {preview ? <FileGlyph kind={kind} ext={ext} /> : null}
      <FileMain name={name} meta={meta} />
      {onDownload ? <IconButton icon="download" label={`${downloadLabel ?? t("ds.file.download")} ${name}`} size="sm" onClick={onDownload} /> : null}
    </div>
    {from || onGoto ? <div className="tb-art-from">
      {from ? <span className="tb-art-who">{from}</span> : null}
      {onGoto ? <button type="button" className="tb-art-goto" onClick={onGoto}>{gotoLabel ?? t("ds.artifact.goto")}<Icon name="arrow-right" size={12} /></button> : null}
    </div> : null}
  </div>;
}
