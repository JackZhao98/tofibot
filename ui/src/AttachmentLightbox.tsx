import { useCallback, useEffect, useRef, useState } from "react";
import { isDesktop } from "./desktop";
import type { Attachment } from "./types";
import { TofiIcon } from "./icons";
import { CopyFeedbackIcon } from "./CopyFeedbackIcon";
import { useTranslation } from "./i18n";
import { attachmentUrl } from "./filePreviewModel";
import { MOTION, EASE } from "./motion";

const reducedMotion = () => matchMedia("(prefers-reduced-motion: reduce)").matches;

/** The image lightbox (native <dialog>, top layer): opens in place, Copy / Download, arrows step through the message's images. */
export default function AttachmentLightbox({ images, index, onIndex, onClose, origin, cellAt, stepButtons, stepsLabel }: { images: Attachment[]; index: number | null; onIndex: (index: number) => void; onClose: () => void; origin: () => HTMLElement | null; /** A click on the picture asks which image to show next, from the point as fractions of its width and height (the work-trace strip). */ cellAt?: (index: number, fx: number, fy: number) => number | null; /** The same step pick for the keyboard and screen readers: one button per image, shown when focused. */ stepButtons?: { label: string; to: number }[]; stepsLabel?: string }) {
  const { t } = useTranslation(["chat", "common"]);
  const dialog = useRef<HTMLDialogElement>(null);
  const zoomImage = useRef<HTMLImageElement>(null);
  const closing = useRef(false);
  const touch = useRef<{ x: number; y: number } | null>(null);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState("");
  const originRef = useRef(origin);
  originRef.current = origin;
  useEffect(() => () => { originRef.current()?.focus({ preventScroll: true }); }, []);
  const current = index === null ? null : images[index] ?? null;
  useEffect(() => {
    const d = dialog.current; if (!d) return;
    if (index !== null && !d.open) {
      d.showModal();
      if (!reducedMotion()) d.animate({ opacity: [0, 1] }, { duration: MOTION.quick, easing: EASE.out });
    }
    if (index === null && d.open) d.close();
  }, [index]);
  const close = useCallback(async () => {
    const d = dialog.current; if (!d || closing.current) return;
    if (reducedMotion()) { onClose(); return; }
    closing.current = true; d.classList.add("is-closing");
    try { await d.animate({ opacity: [1, 0] }, { duration: MOTION.instant, easing: EASE.settle }).finished; } catch { /* Closing still proceeds if animation is interrupted. */ }
    d.classList.remove("is-closing"); closing.current = false; onClose();
  }, [onClose]);
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
  return <dialog ref={dialog} className="attachment-lightbox" aria-label={current?.name}
    onTouchStart={event => { const point = event.touches[0]; if (!point) return; touch.current = { x: point.clientX, y: point.clientY }; }}
    onTouchEnd={event => { const start = touch.current; touch.current = null; const point = event.changedTouches[0]; if (!start || index === null || images.length < 2) return; const dx = point.clientX - start.x, dy = point.clientY - start.y; if (Math.abs(dx) > 45 && Math.abs(dx) > Math.abs(dy) * 1.5) onIndex((index + (dx < 0 ? 1 : images.length - 1)) % images.length); }} onClose={onClose} onCancel={event => { event.preventDefault(); void close(); }} onClick={event => { if (event.target === dialog.current) void close(); }}
    onKeyDown={event => { if (index === null || images.length < 2) return; if (event.key === "ArrowRight" || event.key === "ArrowLeft") event.preventDefault(); if (event.key === "ArrowRight") onIndex((index + 1) % images.length); if (event.key === "ArrowLeft") onIndex((index + images.length - 1) % images.length); }}>
    <button type="button" onClick={() => void close()} aria-label={t("attachment.close_image")}><TofiIcon name="close" size={20} /></button>
    {current && <>
      <img ref={zoomImage} src={attachmentUrl(current.id)} alt={current.name} className={cellAt ? "is-pickable" : undefined} onClick={cellAt ? event => { const box = event.currentTarget.getBoundingClientRect(); const next = box.width && box.height && index !== null ? cellAt(index, (event.clientX - box.left) / box.width, (event.clientY - box.top) / box.height) : null; if (next !== null) onIndex(next); } : undefined} />
      <div className="attachment-lightbox-bar">
        <small>{current.name}{images.length > 1 && ` · ${(index ?? 0) + 1}/${images.length}`}</small>
        <button type="button" onClick={() => void copy()} aria-label={t("attachment.copy", { name: current.name })}><CopyFeedbackIcon copied={copied} /></button>
        {isDesktop ? <button type="button" onClick={() => void save()} aria-label={t("attachment.save", { name: current.name })}><TofiIcon name="download" size={16} /></button> : <a href={attachmentUrl(current.id)} download={current.name} aria-label={t("attachment.download", { name: current.name })}><TofiIcon name="download" size={16} /></a>}
      </div>
      {stepButtons && stepButtons.length > 1 && <ul className="attachment-lightbox-steps" aria-label={stepsLabel}>
        {stepButtons.map(step => <li key={step.to}><button type="button" className="text-button" aria-current={step.to === index ? "true" : undefined} onClick={() => onIndex(step.to)}>{step.label}</button></li>)}
      </ul>}
      {error && <small className="attachment-lightbox-error" role="alert">{error}</small>}
      {copied && <span className="sr-only" role="status">{t("attachment.copied")}</span>}
    </>}
  </dialog>;
}

