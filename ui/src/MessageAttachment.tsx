import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";
import { isDesktop, type NativeFile } from "./desktop";
import type { Attachment } from "./types";
import { TofiIcon } from "./icons";
import { useTranslation } from "./i18n";
import { FileTile } from "./FileTile";
import { AttachmentNotice } from "./AttachmentNotice";
import { attachmentUrl, dedupeAttachments, type AttachmentProblem, fileKind, fileSize, isPreviewable, openFilePreview, rememberAttachments, targetOf } from "./filePreviewModel";
import { galleryColumns, hasPreview, imageSize, rememberImageSize, singleBox, thumbnailUrl } from "./attachmentImages";
import "./message-attachment.css";

const isImage = (a: Attachment) => a.mime.startsWith("image/") && a.mime !== "image/svg+xml";
/** The lightbox is only drawn once a picture is opened: its code is a lazy chunk (kept out of the entry bundle). */
const Lightbox = lazy(() => import("./AttachmentLightbox"));
const loadLightbox = () => void import("./AttachmentLightbox");

/** Optimistic send: how far the file has gone up. A real progressbar for assistive tech; the bar is a transform, never a layout change. */
function UploadProgress({ pending, name, ring = false }: { pending: NonNullable<Attachment["pending"]>; name: string; ring?: boolean }) {
  const { t } = useTranslation("chat");
  const percent = Math.round(pending.progress * 100);
  return <span className={`upload-progress${ring ? " is-overlay" : ""}${pending.failed ? " is-failed" : ""}`} role="progressbar" aria-label={t(pending.failed ? "attachment.upload_failed" : "attachment.uploading", { name })} aria-valuemin={0} aria-valuemax={100} aria-valuenow={pending.failed ? undefined : percent}>
    <span className="upload-progress-bar" style={{ transform: `scaleX(${pending.failed ? 1 : Math.max(.04, pending.progress)})` }} />
  </span>;
}

/** One non-image file: the type tile, name, size and Download (or Save/Reveal in the desktop app). Opens the preview. */
function FileCard({ attachment }: { attachment: Attachment }) {
  const { t } = useTranslation(["chat", "common"]);
  useEffect(() => { rememberAttachments([attachment]); }, [attachment]);
  const [nativeFile, setNativeFile] = useState<NativeFile | null>(null);
  const [busy, setBusy] = useState(false);
  const [nativeError, setNativeError] = useState("");
  const [problem, setProblem] = useState<AttachmentProblem | null>(null);
  const url = attachmentUrl(attachment.id);
  const kind = fileKind(attachment.name, attachment.mime);
  if (attachment.pending) return <div className="delivered-attachment is-file is-pending">
    <div className="attachment-file-row">
      <FileTile name={attachment.name} kind={kind} />
      <div className="attachment-file-open"><b className="attachment-name">{attachment.name}</b><small>{[fileSize(attachment.size), t(attachment.pending.failed ? "attachment.upload_failed_short" : "attachment.uploading_short")].join(" · ")}</small></div>
    </div>
    <UploadProgress pending={attachment.pending} name={attachment.name} />
  </div>;
  async function nativeAction(action: "save" | "reveal") {
    if (busy || !window.tofiDesktop) return;
    setBusy(true); setNativeError("");
    try {
      const file = nativeFile ?? await window.tofiDesktop.prepareAttachment(attachment.id); setNativeFile(file);
      if (action === "save") await window.tofiDesktop.saveAttachment(file.handle);
      else await window.tofiDesktop.revealAttachment(file.handle);
    } catch (cause) { setNativeFile(null); setNativeError(cause instanceof Error ? cause.message : t("attachment.unavailable")); }
    finally { setBusy(false); }
  }
  return <div className="delivered-attachment is-file">
    <div className="attachment-file-row">
      <FileTile name={attachment.name} kind={kind} />
      <button type="button" className="attachment-file-open" onClick={() => openFilePreview(targetOf(attachment))} aria-label={t("attachment.open_preview", { name: attachment.name })} draggable={isDesktop ? Boolean(nativeFile) : undefined} onDragStart={event => { if (!isDesktop) return; event.preventDefault(); if (nativeFile) window.tofiDesktop?.dragAttachment(nativeFile.handle); }}>
        <b className="attachment-name">{attachment.name}</b>
        <small>{[fileSize(attachment.size), isPreviewable(kind) ? t("attachment.preview_hint") : t("attachment.download_only")].join(" · ")}</small>
      </button>
      {isDesktop
        ? <div className="attachment-native-actions">
            <button type="button" className="attachment-download" disabled={busy} onClick={() => void nativeAction("save")} aria-label={t("attachment.save", { name: attachment.name })}><TofiIcon name="download" size={16} /></button>
            {nativeFile && <button type="button" onClick={() => void nativeAction("reveal")} draggable onDragStart={event => { event.preventDefault(); window.tofiDesktop?.dragAttachment(nativeFile.handle); }} title={t("attachment.reveal_hint")}>{t("attachment.reveal")}</button>}
          </div>
        : <a className="attachment-download" href={url} download={attachment.name} onClick={event => {
            // Fetch first, so a file that is only on a detached computer says so instead of saving an error page.
            event.preventDefault();
            void import("./attachmentDownload").then(module => module.downloadAttachment(url, attachment.name)).then(setProblem);
          }} aria-label={t("attachment.download", { name: attachment.name })}><TofiIcon name="download" size={16} /></a>}
    </div>
    <AttachmentNotice problem={problem} />
    {nativeError && <div className="attachment-recovery" role="alert"><span>{nativeError}</span><button onClick={() => void nativeAction("save")}>{t("common:action.retry")}</button></div>}
  </div>;
}

/**
 * The images of one message as thumbnails: one image keeps its aspect ratio (fitted into 320 x 360), several share a
 * tidy grid (2 -> 2 columns, 3 -> 3, 4 -> 2x2, 5-6 -> 3, 7-8 -> 4). Every box is reserved from the known size (or 4:3)
 * before the picture loads, because WebKit has no scroll anchoring. A click opens the lightbox.
 */
function ImageGallery({ images }: { images: Attachment[] }) {
  const { t } = useTranslation("chat");
  const [zoom, setZoom] = useState<number | null>(null);
  const [failed, setFailed] = useState<ReadonlySet<string>>(new Set());
  const tiles = useRef<(HTMLElement | null)[]>([]);
  const origin = useCallback(() => (zoom === null ? null : tiles.current[zoom]?.querySelector("img") ?? tiles.current[zoom] ?? null), [zoom]);
  const single = images.length === 1;
  const box = single ? singleBox(images[0].id) : null;
  const columns = galleryColumns(images.length);
  const tile = (a: Attachment, index: number) => {
    if (failed.has(a.id)) return <button type="button" key={a.id} className="attachment-thumb is-failed" onClick={() => setFailed(current => { const next = new Set(current); next.delete(a.id); return next; })} aria-label={t("attachment.preview_failed")}><FileTile name={a.name} kind="image" size={32} /></button>;
    if (a.pending) return <div key={a.clientKey ?? a.id} className="attachment-thumb is-pending" role="img" aria-label={a.name}>
      {hasPreview(a.id) ? <img src={thumbnailUrl(a.id)} alt="" width={box?.width ?? 160} height={box?.height ?? 160} decoding="async" draggable={false} /> : <FileTile name={a.name} kind="image" size={32} />}
      <UploadProgress pending={a.pending} name={a.name} ring />
    </div>;
    return <button type="button" key={a.clientKey ?? a.id} ref={node => { tiles.current[index] = node; }} className="attachment-thumb" onPointerEnter={loadLightbox} onFocus={loadLightbox} onClick={() => setZoom(index)} aria-label={t("attachment.zoom_image", { name: a.name })}>
      <img src={thumbnailUrl(a.id)} alt={a.name} width={box?.width ?? 160} height={box?.height ?? 160} loading="lazy" decoding="async" draggable={false}
        onLoad={event => { const img = event.currentTarget; if (!imageSize(a.id)) rememberImageSize(a.id, img.naturalWidth, img.naturalHeight); }}
        onError={() => setFailed(current => new Set(current).add(a.id))} />
    </button>;
  };
  return <div className={`attachment-gallery${single ? " is-single" : ""}`} role="group" aria-label={t("attachment.image_group", { count: images.length })}
    style={single && box ? { width: box.width, aspectRatio: `${box.width} / ${box.height}` } : { gridTemplateColumns: `repeat(${columns}, 1fr)` }} data-count={images.length}>
    {images.map(tile)}
    {zoom !== null && <Suspense fallback={null}><Lightbox images={images} index={zoom} onIndex={setZoom} onClose={() => setZoom(null)} origin={origin} /></Suspense>}
  </div>;
}

/** All attachments of one message: a re-published file shows once (newest wins); images become one gallery, other files cards. */
export function AttachmentList({ attachments }: { attachments?: Attachment[] }) {
  const list = dedupeAttachments(attachments);
  rememberAttachments(attachments ?? []);
  const images = list.filter(isImage);
  return <>
    {images.length > 0 && <ImageGallery key="images" images={images} />}
    {list.filter(a => !isImage(a)).map(a => <FileCard attachment={a} key={a.clientKey ?? a.id} />)}
  </>;
}
