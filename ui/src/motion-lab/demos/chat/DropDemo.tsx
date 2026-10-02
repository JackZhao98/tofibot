import { useEffect, useRef, useState, type DragEvent } from "react";
import { flushSync } from "react-dom";
import { TofiIcon } from "../../../icons";
import { arcPoints } from "../../lib/choreo";
import { prefersReducedMotion, settled, wait } from "../../lib/hooks";
import "./chat-cards.css";

type Upload = Readonly<{ key: number; name: string; size: string; state: "landing" | "uploading" | "done" }>;

function sizeText(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

function extension(name: string): string {
  const dot = name.lastIndexOf(".");
  return dot > 0 ? name.slice(dot + 1, dot + 5).toUpperCase() : "FILE";
}

/** Drop a file: the thread lights up, the card falls into the composer, its edge draws progress. */
export function DropDemo() {
  const stageRef = useRef<HTMLDivElement>(null);
  const ringRef = useRef<SVGRectElement>(null);
  const nextKey = useRef(1);
  const lifetime = useRef<AbortController | null>(null);
  const [over, setOver] = useState(false);
  const [upload, setUpload] = useState<Upload | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    lifetime.current = controller;
    return () => controller.abort();
  }, []);

  const land = async (name: string, bytes: number, clientX: number, clientY: number) => {
    const stage = stageRef.current;
    const signal = lifetime.current?.signal;
    if (!stage || !signal) return;
    const key = nextKey.current++;
    flushSync(() => setUpload({ key, name, size: sizeText(bytes), state: "landing" }));
    const slot = stage.querySelector<HTMLElement>(`[data-upload="${key}"]`);
    if (slot && !prefersReducedMotion()) {
      const base = stage.getBoundingClientRect();
      const to = slot.getBoundingClientRect();
      const ghost = slot.cloneNode(true) as HTMLElement;
      ghost.classList.add("drop-ghost");
      Object.assign(ghost.style, { left: `${to.left - base.left}px`, top: `${to.top - base.top}px`, width: `${to.width}px` });
      stage.append(ghost);
      const start = { x: clientX - to.left - to.width / 2, y: clientY - to.top - to.height / 2 };
      const path = arcPoints(start, { x: 0, y: 0 }, 40, 12);
      await settled(ghost.animate([
        ...path.map((point, index) => ({ transform: `translate(${point.x}px, ${point.y}px) rotate(${(1 - index / (path.length - 1)) * -8}deg) scale(${1.1 - 0.1 * (index / (path.length - 1))})` })),
        { transform: "translate(0, 0) scale(1.06, .9)", offset: 0.9 },
        { transform: "translate(0, 0) scale(1)" },
      ], { duration: 620, easing: "cubic-bezier(.45,0,.3,1)" }));
      ghost.remove();
    }
    if (signal.aborted) return;
    setUpload((current) => (current?.key === key ? { ...current, state: "uploading" } : current));
    const ring = ringRef.current;
    const duration = prefersReducedMotion() ? 0 : 1500;
    if (ring) await settled(ring.animate({ strokeDashoffset: ["1", "0"] }, { duration, easing: "cubic-bezier(.4,0,.2,1)" }));
    if (!signal.aborted) setUpload((current) => (current?.key === key ? { ...current, state: "done" } : current));
  };

  const hasFiles = (event: DragEvent) => Array.from(event.dataTransfer.types).includes("Files");
  const onDragOver = (event: DragEvent) => { if (!hasFiles(event)) return; event.preventDefault(); setOver(true); };
  const onDragLeave = (event: DragEvent) => { if (!event.currentTarget.contains(event.relatedTarget as Node)) setOver(false); };
  const onDrop = (event: DragEvent) => {
    if (!hasFiles(event)) return;
    event.preventDefault();
    setOver(false);
    const file = event.dataTransfer.files[0];
    if (file) void land(file.name, file.size, event.clientX, event.clientY);
  };

  const simulate = async () => {
    const stage = stageRef.current;
    const signal = lifetime.current?.signal;
    if (!stage || !signal) return;
    setOver(true);
    if (!await wait(900, signal)) return;
    setOver(false);
    const rect = stage.getBoundingClientRect();
    await land("季度数据.xlsx", 248 * 1024, rect.left + rect.width * 0.5, rect.top + rect.height * 0.35);
  };

  return (
    <div className="drop-demo">
      <div className={`drop-stage${over ? " is-over" : ""}`} ref={stageRef} onDragEnter={onDragOver} onDragOver={onDragOver} onDragLeave={onDragLeave} onDrop={onDrop}>
        <p className="drop-thread">拖一个文件到这张卡片上。</p>
        <div className="drop-zone" aria-hidden={!over}>
          <span className="drop-arrow"><TofiIcon name="upload" size={26} /></span>
          <span>松手，交给研究助理</span>
        </div>
        <div className={`drop-composer${upload ? ` is-${upload.state}` : ""}`}>
          <svg className="drop-ring" aria-hidden="true"><rect ref={ringRef} pathLength={1} /></svg>
          {upload && (
            <span className="drop-file" data-upload={upload.key}>
              <span className="drop-type">{extension(upload.name)}</span>
              <span className="drop-name">{upload.name}</span>
              <small>{upload.state === "done" ? <><TofiIcon name="check" size={12} variant="filled" />已上传</> : upload.size}</small>
            </span>
          )}
          <span className="drop-placeholder">{upload ? "" : "给研究助理发消息"}</span>
        </div>
      </div>
      <div className="demo-actions">
        <button type="button" className="lab-btn" onClick={() => void simulate()}><TofiIcon name="upload" size={16} animated />模拟拖入</button>
      </div>
    </div>
  );
}
