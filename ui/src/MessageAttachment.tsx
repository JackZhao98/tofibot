import {useRef,useState} from "react";
import {isDesktop, type NativeFile} from "./desktop";
import type {Attachment} from "./types";
import { TofiIcon } from "./icons";
import { CopyFeedbackIcon } from "./CopyFeedbackIcon";
import { useTranslation } from "./i18n";
import "./message-attachment.css";
function fileSize(bytes:number){if(bytes<1024)return `${bytes} B`;if(bytes<1024*1024)return `${Math.round(bytes/1024)} KB`;return `${Number((bytes/1024/1024).toFixed(1))} MB`}
export function MessageAttachment({attachment,compact=false}:{attachment:Attachment;compact?:boolean}){
 const {t}=useTranslation(["chat","common"]);
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
 const url=`/api/attachments/${encodeURIComponent(attachment.id)}`;
 const image=attachment.mime.startsWith("image/")&&attachment.mime!=="image/svg+xml";
 const extension=(attachment.name.split(".").at(-1)??"FILE").replace(/[^a-z0-9]/gi,"").slice(0,5).toUpperCase()||"FILE";
 if(compact) return <span className="sent-attachment-pill">
  {image ? <button ref={preview} type="button" className="sent-attachment-main" onClick={openZoom} aria-label={t("attachment.zoom_image",{name:attachment.name})}><img src={url} alt="" loading="lazy"/><span className="sent-attachment-name">{attachment.name}</span><small>{fileSize(attachment.size)}</small></button> : <a className="sent-attachment-main" href={url} target="_blank" rel="noreferrer"><span className="sent-attachment-type" aria-hidden="true">{extension}</span><span className="sent-attachment-name">{attachment.name}</span><small>{fileSize(attachment.size)}</small></a>}
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
  {!image&&<div className="attachment-file-row"><span className="attachment-file-type" data-file-type={extension.toLowerCase()} aria-hidden="true">{extension}</span><div><a href={url} target="_blank" rel="noreferrer" className="attachment-name" draggable={isDesktop?Boolean(nativeFile):undefined} onDragStart={event=>{if(!isDesktop)return;event.preventDefault();if(nativeFile)window.tofiDesktop?.dragAttachment(nativeFile.handle);}}>{attachment.name}</a><small>{fileSize(attachment.size)}</small></div>{isDesktop?<div className="attachment-native-actions"><button disabled={busy} onClick={()=>void nativeAction("save")} aria-label={t("attachment.save",{name:attachment.name})}><TofiIcon name="download" size={16}/></button>{nativeFile&&<button onClick={()=>void nativeAction("reveal")} draggable onDragStart={event=>{event.preventDefault();window.tofiDesktop?.dragAttachment(nativeFile.handle);}} title={t("attachment.reveal_hint")}>{t("attachment.reveal")}</button>}</div>:<a className="attachment-download" href={url} download={attachment.name} aria-label={t("attachment.download",{name:attachment.name})}><TofiIcon name="download" size={16}/></a>}</div>}
  {image&&!failed&&<dialog ref={lightbox} className="attachment-lightbox" onCancel={event=>{event.preventDefault();void closeZoom()}} onClick={event=>{if(event.target===lightbox.current)void closeZoom()}}><button type="button" onClick={()=>void closeZoom()} aria-label={t("attachment.close_image")}><TofiIcon name="close" size={20}/></button><img ref={zoomImage} src={url} alt={attachment.name}/><small>{attachment.name}</small></dialog>}
  {copied&&<span className="sr-only" role="status">{t("attachment.copied")}</span>}
  {nativeError&&<div className="attachment-recovery" role="alert"><span>{nativeError}</span><button onClick={()=>void nativeAction("save")}>{t("common:action.retry")}</button></div>}
  {failed&&<div className="attachment-recovery" role="status"><span>{t("attachment.preview_failed")}</span><button type="button" onClick={()=>{setFailed(false);setAttempt(value=>value+1)}}>{t("common:action.retry")}</button></div>}
 </div>;
}
