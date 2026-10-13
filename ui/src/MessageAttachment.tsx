import {useEffect,useRef,useState} from "react";
import {isDesktop, type NativeFile} from "./desktop";
import type {Attachment} from "./types";
import { TofiIcon } from "./icons";
import { CopyFeedbackIcon } from "./CopyFeedbackIcon";
import { useTranslation } from "./i18n";
import { FileTile } from "./FileViewer";
import { attachmentUrl, dedupeAttachments, fileKind, fileSize, isPreviewable, openFilePreview, rememberAttachments, targetOf } from "./filePreviewModel";
import "./message-attachment.css";
export function MessageAttachment({attachment,compact=false}:{attachment:Attachment;compact?:boolean}){
 const {t}=useTranslation(["chat","common"]);
 useEffect(()=>{rememberAttachments([attachment])},[attachment]);
 const [failed,setFailed]=useState(false);const [attempt,setAttempt]=useState(0);
 const [nativeFile,setNativeFile]=useState<NativeFile|null>(null);
 const [busy,setBusy]=useState(false);
 const [nativeError,setNativeError]=useState("");
 const [copied,setCopied]=useState(false);
 const lightbox=useRef<HTMLDialogElement>(null);
 const preview=useRef<HTMLButtonElement>(null);
 const zoomImage=useRef<HTMLImageElement>(null);
 const closing=useRef(false);
 const reducedMotion=()=>matchMedia("(prefers-reduced-motion: reduce)").matches;
 const delta=()=>{const from=preview.current?.querySelector("img")?.getBoundingClientRect();const to=zoomImage.current?.getBoundingClientRect();return from&&to&&from.width&&from.height&&to.width&&to.height?`translate(${from.left-to.left}px,${from.top-to.top}px) scale(${from.width/to.width},${from.height/to.height})`:null};
 function openZoom(){const dialog=lightbox.current;if(!dialog)return;dialog.showModal();if(isDesktop||reducedMotion())return;const start=delta();if(start)zoomImage.current?.animate({transform:[start,"none"]},{duration:420,easing:"cubic-bezier(.22,1,.36,1)"});}
 async function closeZoom(){const dialog=lightbox.current;if(!dialog||closing.current)return;if(isDesktop||reducedMotion()){dialog.close();preview.current?.focus({preventScroll:true});return;}closing.current=true;dialog.classList.add("is-closing");const end=delta();if(end){try{await zoomImage.current?.animate({transform:["none",end]},{duration:340,easing:"cubic-bezier(.4,0,.2,1)",fill:"forwards"}).finished;}catch{/* Closing still proceeds if animation is interrupted. */}}dialog.close();dialog.classList.remove("is-closing");closing.current=false;preview.current?.focus({preventScroll:true});}
 const url=attachmentUrl(attachment.id);
 const kind=fileKind(attachment.name,attachment.mime);
 const previewable=isPreviewable(kind);
 const image=attachment.mime.startsWith("image/")&&attachment.mime!=="image/svg+xml";
 const extension=(attachment.name.split(".").at(-1)??"FILE").replace(/[^a-z0-9]/gi,"").slice(0,5).toUpperCase()||"FILE";
 if(compact) return <span className="sent-attachment-pill">
  {image ? <button ref={preview} type="button" className="sent-attachment-main" onClick={openZoom} aria-label={t("attachment.zoom_image",{name:attachment.name})}><img src={url} alt="" loading="lazy"/><span className="sent-attachment-name">{attachment.name}</span><small>{fileSize(attachment.size)}</small></button> : <button type="button" className="sent-attachment-main" onClick={()=>openFilePreview(targetOf(attachment))} aria-label={t("attachment.open_preview",{name:attachment.name})}><span className="sent-attachment-type" aria-hidden="true">{extension}</span><span className="sent-attachment-name">{attachment.name}</span><small>{fileSize(attachment.size)}</small></button>}
  {image&&<dialog ref={lightbox} className="attachment-lightbox" onCancel={event=>{event.preventDefault();void closeZoom()}} onClick={event=>{if(event.target===lightbox.current)void closeZoom()}}><button type="button" onClick={()=>void closeZoom()} aria-label={t("attachment.close_image")}><TofiIcon name="close" size={20}/></button><img ref={zoomImage} src={url} alt={attachment.name}/><small>{attachment.name}</small></dialog>}
 </span>;
 async function copyImage(){
  try{
   const response=await fetch(url);
   if(!response.ok)throw new Error(t("attachment.read_failed"));
   const blob=await response.blob();
   if(!blob.type.startsWith("image/"))throw new Error(t("attachment.not_image"));
   await navigator.clipboard.write([new ClipboardItem({[blob.type]:blob})]);
   setCopied(true);window.setTimeout(()=>setCopied(false),1800);
  }catch{setNativeError(t("attachment.copy_failed"));}
 }
 async function nativeAction(action:"save"|"reveal") {
  if(busy||!window.tofiDesktop)return;
  setBusy(true);setNativeError("");
  try{
   const file=nativeFile??await window.tofiDesktop.prepareAttachment(attachment.id);setNativeFile(file);
   if(action==="save")await window.tofiDesktop.saveAttachment(file.handle);
   else await window.tofiDesktop.revealAttachment(file.handle);
  }catch(cause){setNativeFile(null);setNativeError(cause instanceof Error?cause.message:t("attachment.unavailable"));}
  finally{setBusy(false);}
 }
 return <div className={`delivered-attachment ${image?"is-image":"is-file"}`}>
  {image&&!failed&&<div className="attachment-image-frame"><button ref={preview} type="button" className="attachment-preview" onClick={openZoom} aria-label={t("attachment.zoom_image",{name:attachment.name})}><img key={attempt} src={attempt?`${url}?preview_retry=${attempt}`:url} alt={attachment.name} loading="lazy" onError={()=>setFailed(true)}/></button><span className="attachment-image-caption">{attachment.name}</span><div className="attachment-image-actions"><button type="button" onClick={openZoom} aria-label={t("attachment.zoom",{name:attachment.name})}><TofiIcon name="image" size={16}/></button><button type="button" onClick={()=>void copyImage()} aria-label={t("attachment.copy",{name:attachment.name})}>{isDesktop?<TofiIcon name="copy" size={16}/>:<CopyFeedbackIcon copied={copied}/>}</button>{isDesktop?<button type="button" disabled={busy} onClick={()=>void nativeAction("save")} aria-label={t("attachment.save",{name:attachment.name})}><TofiIcon name="download" size={16}/></button>:<a href={url} download={attachment.name} aria-label={t("attachment.download",{name:attachment.name})}><TofiIcon name="download" size={16}/></a>}</div></div>}
  {!image&&<div className="attachment-file-row"><FileTile name={attachment.name} kind={kind}/><button type="button" className="attachment-file-open" onClick={()=>openFilePreview(targetOf(attachment))} aria-label={t("attachment.open_preview",{name:attachment.name})} draggable={isDesktop?Boolean(nativeFile):undefined} onDragStart={event=>{if(!isDesktop)return;event.preventDefault();if(nativeFile)window.tofiDesktop?.dragAttachment(nativeFile.handle);}}><b className="attachment-name">{attachment.name}</b><small>{[fileSize(attachment.size),previewable?t("attachment.preview_hint"):t("attachment.download_only")].join(" · ")}</small></button>{isDesktop?<div className="attachment-native-actions"><button type="button" className="attachment-download" disabled={busy} onClick={()=>void nativeAction("save")} aria-label={t("attachment.save",{name:attachment.name})}><TofiIcon name="download" size={16}/></button>{nativeFile&&<button type="button" onClick={()=>void nativeAction("reveal")} draggable onDragStart={event=>{event.preventDefault();window.tofiDesktop?.dragAttachment(nativeFile.handle);}} title={t("attachment.reveal_hint")}>{t("attachment.reveal")}</button>}</div>:<a className="attachment-download" href={url} download={attachment.name} aria-label={t("attachment.download",{name:attachment.name})}><TofiIcon name="download" size={16}/></a>}</div>}
  {image&&!failed&&<dialog ref={lightbox} className="attachment-lightbox" onCancel={event=>{event.preventDefault();void closeZoom()}} onClick={event=>{if(event.target===lightbox.current)void closeZoom()}}><button type="button" onClick={()=>void closeZoom()} aria-label={t("attachment.close_image")}><TofiIcon name="close" size={20}/></button><img ref={zoomImage} src={url} alt={attachment.name}/><small>{attachment.name}</small></dialog>}
  {copied&&<span className="sr-only" role="status">{t("attachment.copied")}</span>}
  {nativeError&&<div className="attachment-recovery" role="alert"><span>{nativeError}</span><button onClick={()=>void nativeAction("save")}>{t("common:action.retry")}</button></div>}
  {failed&&<div className="attachment-recovery" role="status"><span>{t("attachment.preview_failed")}</span><button type="button" onClick={()=>{setFailed(false);setAttempt(value=>value+1)}}>{t("common:action.retry")}</button></div>}
 </div>;
}

const isImage = (a: Attachment) => a.mime.startsWith("image/") && a.mime !== "image/svg+xml";

/** Several images in one message: a 3-up square grid; the last tile carries +N and expands the rest. */
function ImageGrid({ images }: { images: Attachment[] }) {
  const { t } = useTranslation("chat");
  const [expanded, setExpanded] = useState(false);
  const [zoom, setZoom] = useState<number | null>(null);
  const dialog = useRef<HTMLDialogElement>(null);
  const hidden = expanded ? 0 : Math.max(0, images.length - 3);
  const shown = hidden ? images.slice(0, 3) : images;
  useEffect(() => { const d = dialog.current; if (!d) return; if (zoom !== null && !d.open) d.showModal(); if (zoom === null && d.open) d.close(); }, [zoom]);
  const current = zoom === null ? null : images[zoom];
  return <div className="delivered-attachment-grid" role="group" aria-label={t("attachment.image_group",{count:images.length})}>
    {shown.map((a, index) => {
      const more = hidden > 0 && index === 2;
      return <button type="button" key={a.id} className={`attachment-grid-tile${more ? " is-more" : ""}`} onClick={() => more ? setExpanded(true) : setZoom(index)} aria-label={more ? t("attachment.show_more_images", { count: hidden }) : t("attachment.zoom_image", { name: a.name })}>
        <img src={attachmentUrl(a.id)} alt={more ? "" : a.name} loading="lazy" />{more && <span className="attachment-grid-more" aria-hidden="true">+{hidden}</span>}
      </button>;
    })}
    <dialog ref={dialog} className="attachment-lightbox" onClose={() => setZoom(null)} onCancel={() => setZoom(null)} onClick={event => { if (event.target === dialog.current) setZoom(null); }}>
      <button type="button" onClick={() => setZoom(null)} aria-label={t("attachment.close_image")}><TofiIcon name="close" size={20} /></button>
      {current && <><img src={attachmentUrl(current.id)} alt={current.name} /><small>{current.name}</small></>}
    </dialog>
  </div>;
}

/** All attachments of one message: a re-published file shows once (newest wins), images group, files stay rows. */
export function AttachmentList({ attachments, compact = false }: { attachments?: Attachment[]; compact?: boolean }) {
  const list = dedupeAttachments(attachments);
  rememberAttachments(attachments ?? []);
  const images = compact ? [] : list.filter(isImage);
  const grouped = images.length >= 2;
  return <>
    {list.map(a => grouped && isImage(a) ? (a.id === images[0].id ? <ImageGrid key="image-grid" images={images} /> : null) : <MessageAttachment attachment={a} compact={compact} key={a.id} />)}
  </>;
}
