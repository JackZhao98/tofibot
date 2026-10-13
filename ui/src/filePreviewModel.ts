import { useSyncExternalStore } from "react";
import type { Attachment } from "./types";

export type FileKind = "md" | "txt" | "code" | "csv" | "tsv" | "json" | "pdf" | "image" | "xlsx" | "docx" | "pptx" | "zip" | "other";

const codeExtensions = new Set(["js", "jsx", "ts", "tsx", "mjs", "cjs", "py", "go", "rs", "rb", "java", "kt", "swift", "c", "h", "cc", "cpp", "hpp", "cs", "php", "sh", "bash", "zsh", "sql", "css", "scss", "html", "htm", "xml", "yml", "yaml", "toml", "ini", "env", "lua", "r", "pl", "vue", "svelte", "diff", "patch", "dockerfile"]);
const textExtensions = new Set(["txt", "log", "text", "conf", "cfg", "rst", "tex", "srt", "vtt"]);

export function extensionOf(name: string): string {
  const base = name.split("/").at(-1) ?? name;
  const dot = base.lastIndexOf(".");
  return dot > 0 ? base.slice(dot + 1).toLowerCase() : "";
}

export function fileKind(name: string, mime = ""): FileKind {
  const ext = extensionOf(name);
  const type = mime.split(";")[0].trim().toLowerCase();
  if (ext === "md" || ext === "markdown" || ext === "mdx" || type === "text/markdown") return "md";
  if (ext === "csv" || type === "text/csv") return "csv";
  if (ext === "tsv" || type === "text/tab-separated-values") return "tsv";
  if (ext === "json" || ext === "jsonl" || ext === "ndjson" || type === "application/json") return "json";
  if (ext === "pdf" || type === "application/pdf") return "pdf";
  if (type.startsWith("image/") && type !== "image/svg+xml") return "image";
  if (ext === "xlsx" || ext === "xls" || ext === "xlsm" || ext === "ods") return "xlsx";
  if (ext === "docx" || ext === "doc" || ext === "odt" || ext === "rtf") return "docx";
  if (ext === "pptx" || ext === "ppt" || ext === "key" || ext === "odp") return "pptx";
  if (["zip", "tar", "gz", "tgz", "7z", "rar", "bz2", "xz"].includes(ext)) return "zip";
  if (textExtensions.has(ext)) return "txt";
  if (codeExtensions.has(ext)) return "code";
  if (type === "text/plain" || type.startsWith("text/")) return ext ? "code" : "txt";
  return "other";
}

export const previewableKinds: ReadonlySet<FileKind> = new Set(["md", "txt", "code", "csv", "tsv", "json", "pdf", "image"]);
export const isPreviewable = (kind: FileKind) => previewableKinds.has(kind);

const kindLabels: Record<FileKind, string> = { md: "MD", txt: "TXT", code: "", csv: "CSV", tsv: "TSV", json: "JSON", pdf: "PDF", image: "IMG", xlsx: "XLSX", docx: "DOCX", pptx: "PPTX", zip: "ZIP", other: "" };
/** Short mono label for the type tile: the format, never translated. */
export function tileLabel(name: string, kind: FileKind): string {
  const fixed = kindLabels[kind];
  if (fixed) return fixed;
  const ext = extensionOf(name).replace(/[^a-z0-9]/g, "").slice(0, 4).toUpperCase();
  return ext || "FILE";
}
/** Tile colour group; drives the palette in message-attachment.css. */
export function tileTone(kind: FileKind): string {
  return kind === "csv" || kind === "tsv" ? "xlsx" : kind === "pptx" ? "docx" : kind;
}

export function fileSize(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${Number((bytes / 1024 / 1024).toFixed(1))} MB`;
}

export const attachmentUrl = (id: string) => `/api/attachments/${encodeURIComponent(id)}`;

/** Within one message, a file published again under the same name supersedes the older one. */
export function dedupeAttachments<T extends Pick<Attachment, "name" | "created_at">>(list: readonly T[] | undefined): T[] {
  if (!list) return [];
  const newest = new Map<string, number>();
  list.forEach((a, index) => {
    const prior = newest.get(a.name);
    if (prior === undefined || list[prior].created_at <= a.created_at) newest.set(a.name, index);
  });
  return list.filter((a, index) => newest.get(a.name) === index);
}

/** `attachment:<id>` (optionally `attachment://<id>`, `/api/attachments/<id>`) -> id. */
export function attachmentIdFromHref(href: string | undefined): string | null {
  if (!href) return null;
  const trimmed = href.trim();
  const scheme = /^attachment:(?:\/\/)?([^\s/?#]+)/i.exec(trimmed);
  if (scheme) return decodeURIComponent(scheme[1]);
  const api = /^(?:https?:\/\/[^/]+)?\/api\/attachments\/([^\s/?#]+)/i.exec(trimmed);
  return api ? decodeURIComponent(api[1]) : null;
}

/** A relative link like `notes/report.md` or `./report.md#top`: candidate file name, else null. */
export function relativeLinkName(href: string | undefined): string | null {
  if (!href) return null;
  const trimmed = href.trim();
  if (!trimmed || trimmed.startsWith("#") || trimmed.startsWith("/") || /^[a-z][a-z0-9+.-]*:/i.test(trimmed) || trimmed.startsWith("//")) return null;
  const path = trimmed.split(/[?#]/)[0];
  try { return decodeURIComponent(path.split("/").filter(Boolean).at(-1) ?? "") || null; } catch { return null; }
}

// Attachments the chat has rendered, so links can resolve ids and names without a request.
const known = new Map<string, Attachment>();
export function rememberAttachments(list: readonly Attachment[]) { for (const a of list) known.set(a.id, a); }
export function knownAttachment(id: string): Attachment | undefined { return known.get(id); }
export function resolveAttachmentByName(name: string, conversationId?: string): Attachment | undefined {
  let best: Attachment | undefined;
  for (const a of known.values()) {
    if (a.name !== name || (conversationId && a.conversation_id !== conversationId)) continue;
    if (!best || best.created_at <= a.created_at) best = a;
  }
  return best;
}

export type PreviewTarget = { id: string; name: string; mime: string; size: number; conversationId?: string };
export const targetOf = (a: Attachment): PreviewTarget => ({ id: a.id, name: a.name, mime: a.mime, size: a.size, conversationId: a.conversation_id });

// One viewer for the whole app: a stack of documents (links inside a document push, Back pops).
let stack: PreviewTarget[] = [];
const listeners = new Set<() => void>();
const emit = () => { for (const fn of listeners) fn(); };
export function openFilePreview(target: PreviewTarget, options: { push?: boolean } = {}) {
  stack = options.push && stack.length ? [...stack, target] : [target];
  emit();
}
export function openAttachmentById(id: string, options: { push?: boolean } = {}) {
  const a = known.get(id);
  openFilePreview(a ? targetOf(a) : { id, name: id, mime: "", size: 0 }, options);
}
export function popFilePreview() { if (stack.length > 1) { stack = stack.slice(0, -1); emit(); } }
export function closeFilePreview() { if (stack.length) { stack = []; emit(); } }
export function useFilePreviewStack(): PreviewTarget[] {
  return useSyncExternalStore(fn => { listeners.add(fn); return () => { listeners.delete(fn); }; }, () => stack, () => stack);
}

/** Cap for text previews: bigger files show their head and offer the download. */
export const PREVIEW_TEXT_LIMIT = 2 * 1024 * 1024;

export async function readTextHead(url: string, limit = PREVIEW_TEXT_LIMIT, signal?: AbortSignal): Promise<{ text: string; truncated: boolean; name: string }> {
  const response = await fetch(url, { credentials: "same-origin", signal });
  if (!response.ok) throw new Error(String(response.status));
  const disposition = response.headers.get("Content-Disposition") ?? "";
  const name = /filename="?([^";]+)"?/i.exec(disposition)?.[1] ?? "";
  const reader = response.body?.getReader();
  if (!reader) { const text = await response.text(); return { text: text.slice(0, limit), truncated: text.length > limit, name }; }
  const chunks: Uint8Array[] = [];
  let received = 0;
  let truncated = false;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    if (received + value.length > limit) { chunks.push(value.slice(0, limit - received)); received = limit; truncated = true; void reader.cancel(); break; }
    chunks.push(value);
    received += value.length;
  }
  const bytes = new Uint8Array(received);
  let offset = 0;
  for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length; }
  return { text: new TextDecoder("utf-8").decode(bytes), truncated, name };
}

/** RFC 4180 style parser: quoted fields, escaped quotes, CRLF. */
export function parseDelimited(text: string, delimiter: string, maxRows = 2000): { rows: string[][]; more: boolean } {
  const rows: string[][] = [];
  let row: string[] = [];
  let field = "";
  let quoted = false;
  let more = false;
  const pushRow = () => { row.push(field); field = ""; rows.push(row); row = []; };
  for (let i = 0; i < text.length; i += 1) {
    const ch = text[i];
    if (quoted) {
      if (ch === '"') { if (text[i + 1] === '"') { field += '"'; i += 1; } else quoted = false; }
      else field += ch;
    } else if (ch === '"' && field === "") quoted = true;
    else if (ch === delimiter) { row.push(field); field = ""; }
    else if (ch === "\n" || ch === "\r") {
      if (ch === "\r" && text[i + 1] === "\n") i += 1;
      pushRow();
      if (rows.length >= maxRows) { more = i < text.length - 1; return { rows, more }; }
    } else field += ch;
  }
  if (field !== "" || row.length) pushRow();
  return { rows, more };
}
