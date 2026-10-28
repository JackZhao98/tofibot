import { useCallback, useEffect, useRef, useState } from "react";
import { isDesktop } from "./desktop";
import type { Attachment } from "./types";
import { TofiIcon } from "./icons";
import { CopyFeedbackIcon } from "./CopyFeedbackIcon";
import { useTranslation } from "./i18n";
import { attachmentUrl } from "./filePreviewModel";
import { MOTION, EASE } from "./motion";

const reducedMotion = () => matchMedia("(prefers-reduced-motion: reduce)").matches;

/** The image lightbox (native <dialog>, top layer): zooms from the thumbnail, Copy / Download, arrows step through the message's images. */
export default function AttachmentLightbox({ images, index, onIndex, onClose, origin }: { images: Attachment[]; index: number | null; onIndex: (index: number) => void; onClose: () => void; origin: () => HTMLElement | null }) {
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

