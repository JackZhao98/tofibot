import { Icon } from "../Icon/Icon";
import { cx } from "../cx";
import "../file.css";

export type FileKind = "pdf" | "doc" | "sheet" | "slides" | "image" | "code" | "md" | "archive" | "media" | "video" | "audio" | "page" | "text";
export type FilePreviewData = {
  type: "image" | "pdf" | "page" | "video" | "code" | "text" | "md" | "table" | "audio";
  /** Pictures, posters and page shots. */
  src?: string;
  /** First lines of code, text and Markdown. */
  lines?: string[];
  /** First rows of a table; the first row is the head. */
  rows?: string[][];
  /** The corner chip ("12 pages", "0:42"). */
  label?: string;
};

/** One hue slot per kind (V3 PLAN: pdf tomato, sheet lime, doc cobalt, md grape, code teal, slides mustard). */
const TONE: Record<FileKind, string> = { pdf: "tomato", doc: "cobalt", sheet: "lime", slides: "mustard", image: "grape", code: "teal", md: "grape", archive: "mustard", media: "cobalt", video: "cobalt", audio: "teal", page: "tomato", text: "neutral" };

/** A file glyph with a folded corner and the extension in mono. */
export function FileGlyph({ kind = "text", ext, big, className }: { kind?: FileKind; ext?: string; big?: boolean; className?: string }) {
  return <span className={cx("tb-file-glyph", `tb-file--${TONE[kind] ?? "neutral"}`, big && "tb-file-glyph--big", className)}>
    <span className="tb-file-ext">{(ext || kind).slice(0, 4).toUpperCase()}</span>
  </span>;
}

/** The name and meta of a file, one under the other. */
export function FileMain({ name, meta }: { name: string; meta?: string }) {
  return <span className="tb-file-main"><span className="tb-file-name" title={name}>{name}</span>{meta ? <span className="tb-file-meta">{meta}</span> : null}</span>;
}

/** A stable pseudo-waveform from the file name, so the same file always draws the same bars. */
function waveBars(seed: string, count: number): number[] {
  let x = 0;
  for (let i = 0; i < seed.length; i++) x = (x * 31 + seed.charCodeAt(i)) >>> 0;
  const out: number[] = [];
  for (let k = 0; k < count; k++) { x = (x * 1103515245 + 12345) >>> 0; out.push(22 + (x % 78)); }
  return out;
}

/** The real first page, first lines or first rows of a file. Never a fake thumbnail. */
export function FilePreview({ preview, name }: { preview: FilePreviewData; name?: string }) {
  const type = preview.type;
  if (type === "code" || type === "text" || type === "md") {
    return <span className={cx("tb-fv-lines", type === "code" ? "tb-fv-code" : "tb-fv-paper")}>
      {(preview.lines ?? []).slice(0, 7).map((line, i) => <span key={i}>{line || " "}</span>)}
    </span>;
  }
  if (type === "table") {
    const rows = (preview.rows ?? []).slice(0, 4);
    const cols = Math.min(4, rows[0] ? rows[0].length : 3);
    return <span className="tb-fv-table">{rows.map((row, i) => <span key={i} className={cx("tb-fv-row", i === 0 && "is-head")} style={{ ["--cols" as string]: cols }}>
      {row.slice(0, cols).map((cell, j) => <span key={j}>{cell}</span>)}
    </span>)}</span>;
  }
  if (type === "audio") {
    return <span className="tb-fv-audio">
      <span className="tb-fv-play" aria-hidden="true"><Icon name="play" size={18} variant="filled" /></span>
      <span className="tb-fv-wave" aria-hidden="true">{waveBars(name || "audio", 34).map((v, i) => <i key={i} style={{ height: `${v}%` }} />)}</span>
      {preview.label ? <span className="tb-fv-chip">{preview.label}</span> : null}
    </span>;
  }
  const img = <img src={preview.src} alt="" />;
  return <span className={cx("tb-fv-media", `tb-fv-${type}`)}>
    {type === "pdf" ? <span className="tb-fv-sheet">{img}</span> : img}
    {type === "video" ? <span className="tb-fv-play tb-fv-play--center" aria-hidden="true"><Icon name="play" size={18} variant="filled" /></span> : null}
    {preview.label ? <span className="tb-fv-chip">{preview.label}</span> : null}
  </span>;
}
