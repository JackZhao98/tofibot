import { tileLabel, tileTone, type FileKind } from "./filePreviewModel";
import "./file-tile.css";

/** One glyph per family, drawn on a 16x14 box with fills (no heavy outlines); colour comes from the tile's --ft. */
function Glyph({ tone }: { tone: string }) {
  switch (tone) {
    case "sheet": return <g><rect x="0" y="1" width="7" height="5" rx="1.4"/><rect x="9" y="1" width="7" height="5" rx="1.4"/><rect x="0" y="8" width="7" height="5" rx="1.4"/><rect x="9" y="8" width="7" height="5" rx="1.4" opacity=".55"/></g>;
    case "slides": return <g><rect x="0" y="0.5" width="16" height="10" rx="2.2" opacity=".28"/><rect x="3" y="5" width="2.4" height="3.6" rx=".8"/><rect x="6.8" y="3" width="2.4" height="5.6" rx=".8"/><rect x="10.6" y="4" width="2.4" height="4.6" rx=".8"/><rect x="6" y="12" width="4" height="1.6" rx=".8"/></g>;
    case "code": return <g fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round"><path d="M5 2.5 1.2 7 5 11.5M11 2.5 14.8 7 11 11.5"/></g>;
    case "json": return <g fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><path d="M5.6 1.5C3.6 1.5 4.4 4 3.4 5.2 3 5.8 2.4 6.6 1.6 7 2.4 7.4 3 8.2 3.4 8.8 4.4 10 3.6 12.5 5.6 12.5M10.4 1.5C12.4 1.5 11.6 4 12.6 5.2 13 5.8 13.6 6.6 14.4 7 13.6 7.4 13 8.2 12.6 8.8 11.6 10 12.4 12.5 10.4 12.5"/></g>;
    case "archive": return <g><rect x="6" y="0" width="4" height="2.4" rx=".8"/><rect x="6" y="3.6" width="4" height="2.4" rx=".8" opacity=".6"/><rect x="6" y="7.2" width="4" height="2.4" rx=".8"/><rect x="5" y="10.4" width="6" height="3.4" rx="1.4" opacity=".6"/></g>;
    case "image": return <g><circle cx="11.6" cy="3.6" r="2"/><path d="M0 13.4 5.2 5.6a1 1 0 0 1 1.7 0L10 10.2l1.4-1.9a1 1 0 0 1 1.6 0L16 13.4z" opacity=".78"/></g>;
    case "media": return <g><rect x="0" y="1" width="16" height="12" rx="3.2" opacity=".28"/><path d="M6.4 4.4v5.2a.6.6 0 0 0 .9.5l4.1-2.6a.6.6 0 0 0 0-1L7.3 3.9a.6.6 0 0 0-.9.5z"/></g>;
    case "other": return <g><circle cx="3" cy="7" r="1.5"/><circle cx="8" cy="7" r="1.5"/><circle cx="13" cy="7" r="1.5"/></g>;
    default: return <g><rect x="0" y="1" width="16" height="2.2" rx="1.1"/><rect x="0" y="5.9" width="16" height="2.2" rx="1.1" opacity=".6"/><rect x="0" y="10.8" width="10" height="2.2" rx="1.1" opacity=".6"/></g>;
  }
}

/**
 * Type tile: a rounded page with a soft fold, a glyph for the family and the format in mono.
 * Size follows --tile (default 40px high); below 36px the format label is dropped (it cannot fit) and the glyph, enlarged, carries the family by shape and colour; colours are the --ft-* family tokens, light and dark.
 * With `thumb` an image shows its own picture, rounded, instead of the page.
 */
export function FileTile({ name, kind, thumb, size }: { name: string; kind: FileKind; thumb?: string; size?: number }) {
  const tone = tileTone(kind);
  const small = Boolean(size && size < 36);
  const style = size ? { "--tile": `${size}px` } as React.CSSProperties : undefined;
  if (thumb && kind === "image") return <span className="file-tile is-thumb" style={style} data-file-tone={tone} data-file-kind={kind} aria-hidden="true"><img src={thumb} alt="" loading="lazy" decoding="async" /></span>;
  return <span className={`file-tile${small ? " is-small" : ""}`} style={style} data-file-tone={tone} data-file-kind={kind} aria-hidden="true">
    <svg viewBox="0 0 32 40" focusable="false"><path className="ft-page" d="M5.5.5h13.6c.5 0 1 .2 1.4.6l10.4 10.4c.4.4.6.9.6 1.4v22.6a5 5 0 0 1-5 5H5.5a5 5 0 0 1-5-5V5.5a5 5 0 0 1 5-5z"/><path className="ft-fold" d="M19.6.7 31.3 12.4h-7.2a4.5 4.5 0 0 1-4.5-4.5z"/><svg className="ft-glyph" {...(small ? { x: 5, y: 14, width: 22, height: 19 } : { x: 8, y: 13, width: 16, height: 14 })} viewBox="0 0 16 14"><Glyph tone={tone} /></svg></svg>
    {!small && <span className="ft-label">{tileLabel(name, kind)}</span>}
  </span>;
}
