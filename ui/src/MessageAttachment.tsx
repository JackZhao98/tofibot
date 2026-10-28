import { useCallback, useEffect, useRef, useState } from "react";
import { isDesktop, type NativeFile } from "./desktop";
import type { Attachment } from "./types";
import { TofiIcon } from "./icons";
import { CopyFeedbackIcon } from "./CopyFeedbackIcon";
import { useTranslation } from "./i18n";
import { FileTile } from "./FileTile";
import { AttachmentNotice } from "./AttachmentNotice";
import { attachmentUrl, dedupeAttachments, type AttachmentProblem, fileKind, fileSize, isPreviewable, openFilePreview, rememberAttachments, targetOf } from "./filePreviewModel";
import { galleryColumns, imageSize, rememberImageSize, singleBox, thumbnailUrl } from "./attachmentImages";
import "./message-attachment.css";
import { MOTION, EASE } from "./motion";

const isImage = (a: Attachment) => a.mime.startsWith("image/") && a.mime !== "image/svg+xml";
const reducedMotion = () => matchMedia("(prefers-reduced-motion: reduce)").matches;

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

/** The image lightbox (native <dialog>, top layer): zooms from the thumbnail, Copy / Download, arrows step through the message's images. */
function Lightbox({ images, index, onIndex, onClose, origin }: { images: Attachment[]; index: number | null; onIndex: (index: number) => void; onClose: () => void; origin: () => HTMLElement | null }) {
  const { t } = useTranslation(["chat", "common"]);
  const dialog = useRef<HTMLDialogElement>(null);
  const zoomImage = useRef<HTMLImageElement>(null);
  const closing = useRef(false);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState("");
  const current = index === null ? null : images[index] ?? null;
  const delta = useCallback(() => {
    const from = origin()?.getBoundingClientRect(); const to = zoomImage.current?.getBoundingClientRect();
    return from && to && from.width && from.height && to.width && to.height ? `translate(${from.left - to.left}px,${from.top - to.top}px) scale(${from.width / to.width},${from.height / to.height})` : null;
  }, [origin]);
  useEffect(() => {
    const d = dialog.current; if (!d) return;
    if (index !== null && !d.open) {
      d.showModal();
      if (!isDesktop && !reducedMotion()) { const start = delta(); if (start) zoomImage.current?.animate({ transform: [start, "none"] }, { duration: MOTION.panelIn, easing: EASE.out }); }
    }
    if (index === null && d.open) d.close();
  }, [index, delta]);
  const close = useCallback(async () => {
    const d = dialog.current; if (!d || closing.current) return;
    if (isDesktop || reducedMotion()) { onClose(); return; }
    closing.current = true; d.classList.add("is-closing");
    const end = delta();
    if (end) { try { await zoomImage.current?.animate({ transform: ["none", end] }, { duration: MOTION.panelOut, easing: EASE.inout, fill: "forwards" }).finished; } catch { /* Closing still proceeds if animation is interrupted. */ } }
    d.classList.remove("is-closing"); closing.current = false; onClose();
  }, [delta, onClose]);
  useEffect(() => { setCopied(false); setError(""); }, [index]);
  async function copy() {
    if (!current) return;
    try {
      const response = await fetch(attachmentUrl(current.id));
      if (!response.ok) throw new Error(t("attachment.read_failed"));
      const blob = await response.blob();
      if (!blob.type.startsWith("image/")) throw new Error(t("attachment.not_image"));
      await navigator.clipboard.write([new ClipboardItem({ [blob.type]: blob })]);
      setCopied(true); window.setTimeout(() => setCopied(false), 1800);
    } catch { setError(t("attachment.copy_failed")); }
  }
  async function save() {
    if (!current || !window.tofiDesktop) return;
    try { const file = await window.tofiDesktop.prepareAttachment(current.id); await window.tofiDesktop.saveAttachment(file.handle); } catch (cause) { setError(cause instanceof Error ? cause.message : t("attachment.unavailable")); }
  }
  return <dialog ref={dialog} className="attachment-lightbox" onClose={onClose} onCancel={event => { event.preventDefault(); void close(); }} onClick={event => { if (event.target === dialog.current) void close(); }}
    onKeyDown={event => { if (index === null || images.length < 2) return; if (event.key === "ArrowRight") onIndex((index + 1) % images.length); if (event.key === "ArrowLeft") onIndex((index + images.length - 1) % images.length); }}>
    <button type="button" onClick={() => void close()} aria-label={t("attachment.close_image")}><TofiIcon name="close" size={20} /></button>
    {current && <>
      <img ref={zoomImage} src={attachmentUrl(current.id)} alt={current.name} />
      <div className="attachment-lightbox-bar">
        <small>{current.name}{images.length > 1 && ` · ${(index ?? 0) + 1}/${images.length}`}</small>
        <button type="button" onClick={() => void copy()} aria-label={t("attachment.copy", { name: current.name })}>{isDesktop ? <TofiIcon name="copy" size={16} /> : <CopyFeedbackIcon copied={copied} />}</button>
        {isDesktop ? <button type="button" onClick={() => void save()} aria-label={t("attachment.save", { name: current.name })}><TofiIcon name="download" size={16} /></button> : <a href={attachmentUrl(current.id)} download={current.name} aria-label={t("attachment.download", { name: current.name })}><TofiIcon name="download" size={16} /></a>}
      </div>
      {error && <small className="attachment-lightbox-error" role="alert">{error}</small>}
      {copied && <span className="sr-only" role="status">{t("attachment.copied")}</span>}
    </>}
  </dialog>;
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
    return <button type="button" key={a.id} ref={node => { tiles.current[index] = node; }} className="attachment-thumb" onClick={() => setZoom(index)} aria-label={t("attachment.zoom_image", { name: a.name })}>
      <img src={thumbnailUrl(a.id)} alt={a.name} width={box?.width ?? 160} height={box?.height ?? 160} loading="lazy" decoding="async" draggable={false}
        onLoad={event => { const img = event.currentTarget; if (!imageSize(a.id)) rememberImageSize(a.id, img.naturalWidth, img.naturalHeight); }}
        onError={() => setFailed(current => new Set(current).add(a.id))} />
    </button>;
  };
  return <div className={`attachment-gallery${single ? " is-single" : ""}`} role="group" aria-label={t("attachment.image_group", { count: images.length })}
    style={single && box ? { width: box.width, aspectRatio: `${box.width} / ${box.height}` } : { gridTemplateColumns: `repeat(${columns}, 1fr)` }} data-count={images.length}>
    {images.map(tile)}
    <Lightbox images={images} index={zoom} onIndex={setZoom} onClose={() => setZoom(null)} origin={origin} />
  </div>;
}

/** All attachments of one message: a re-published file shows once (newest wins); images become one gallery, other files cards. */
export function AttachmentList({ attachments }: { attachments?: Attachment[] }) {
  const list = dedupeAttachments(attachments);
  rememberAttachments(attachments ?? []);
  const images = list.filter(isImage);
  return <>
    {images.length > 0 && <ImageGallery key="images" images={images} />}
    {list.filter(a => !isImage(a)).map(a => <FileCard attachment={a} key={a.id} />)}
  </>;
}
