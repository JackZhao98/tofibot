import { attachmentUrl } from "./filePreviewModel";

/**
 * What the chat knows about an image attachment before it is on screen: its pixel size (so a thumbnail reserves
 * its box and the list never shifts when the picture arrives) and, for a picture this tab just sent, the local
 * file (so the thumbnail is there at once and is not fetched back from the server).
 * The size is learned from the file when it is sent and from the picture when it first loads, and is kept in
 * localStorage; an image whose size is unknown reserves a 4:3 box and is cropped to it (never re-laid out).
 */
const STORE = "tofi:image-sizes";
const KEEP = 400;
const sizes = new Map<string, readonly [number, number]>();
let loaded = false;
let saveTimer = 0;

function load() {
  if (loaded) return;
  loaded = true;
  try {
    const raw = JSON.parse(localStorage.getItem(STORE) ?? "[]") as [string, number, number][];
    for (const [id, w, h] of raw) if (id && w > 0 && h > 0) sizes.set(id, [w, h]);
  } catch { /* A blocked or corrupt store only costs the reserved box being a guess. */ }
}
function persist() {
  if (saveTimer) return;
  saveTimer = window.setTimeout(() => {
    saveTimer = 0;
    try { localStorage.setItem(STORE, JSON.stringify([...sizes].slice(-KEEP).map(([id, [w, h]]) => [id, w, h]))); } catch { /* ignore */ }
  }, 800);
}

export function imageSize(id: string): readonly [number, number] | undefined { load(); return sizes.get(id); }
export function rememberImageSize(id: string, width: number, height: number) {
  load();
  if (!(width > 0 && height > 0)) return;
  const known = sizes.get(id);
  if (known && known[0] === width && known[1] === height) return;
  sizes.delete(id);
  sizes.set(id, [width, height]);
  persist();
}

const local = new Map<string, string>();
const LOCAL_KEEP = 8;
/** Called with the File the person just sent: learns its size and keeps a local URL for the thumbnail. */
export async function rememberSentImage(id: string, file: Blob): Promise<void> {
  if (!file.type.startsWith("image/")) return;
  try {
    const bitmap = await createImageBitmap(file);
    rememberImageSize(id, bitmap.width, bitmap.height);
    bitmap.close();
  } catch { /* An undecodable file just has no size yet. */ }
  const first = local.keys().next().value;
  if (local.size >= LOCAL_KEEP && first !== undefined) { URL.revokeObjectURL(local.get(first)!); local.delete(first); }
  local.set(id, URL.createObjectURL(file));
}
/** Optimistic send: a picture still on its way up shows from the file itself (object URL) under a temporary id. */
const previews = new Map<string, string>();
export async function registerPreview(tempId: string, file: Blob): Promise<void> {
  if (!file.type.startsWith("image/") || file.type === "image/svg+xml" || previews.has(tempId)) return;
  previews.set(tempId, URL.createObjectURL(file));
  // The size is read before the bubble is drawn (capped at 120 ms) so the box is right the first time and the swap moves nothing.
  const sized = createImageBitmap(file).then(bitmap => { sizes.set(tempId, [bitmap.width, bitmap.height]); bitmap.close(); }, () => {});
  await Promise.race([sized, new Promise<void>(resolve => setTimeout(resolve, 120))]);
}
/** The upload finished: the same object URL now stands for the server id, so the thumbnail does not reload or flicker. */
export function adoptPreview(tempId: string, id: string, file: Blob) {
  const url = previews.get(tempId);
  previews.delete(tempId);
  sizes.delete(tempId);
  if (!url) { void rememberSentImage(id, file); return; }
  const first = local.keys().next().value;
  if (local.size >= LOCAL_KEEP && first !== undefined) { URL.revokeObjectURL(local.get(first)!); local.delete(first); }
  local.set(id, url);
  void createImageBitmap(file).then(bitmap => { rememberImageSize(id, bitmap.width, bitmap.height); bitmap.close(); }, () => {});
}
export function dropPreview(tempId: string) {
  const url = previews.get(tempId);
  if (url) { URL.revokeObjectURL(url); previews.delete(tempId); }
  sizes.delete(tempId);
}
export const hasPreview = (id: string) => previews.has(id);
export const thumbnailUrl = (id: string) => previews.get(id) ?? local.get(id) ?? attachmentUrl(id);

/** The box a single image takes: fitted into max x maxHeight, never below `min` on the short side. Unknown size: 4:3. */
export function singleBox(id: string, max = 320, maxHeight = 360, min = 96): { width: number; height: number; known: boolean } {
  const size = imageSize(id);
  const [w, h] = size ?? [4, 3];
  const scale = Math.min(1, max / w, maxHeight / h);
  let width = w * scale;
  let height = h * scale;
  if (Math.min(width, height) < min) { const up = min / Math.min(width, height); width = Math.min(max, width * up); height = Math.min(maxHeight, height * up); }
  return { width: Math.round(width), height: Math.round(height), known: Boolean(size) };
}

/** Columns for n images in a gallery: 2 -> 2, 3 -> 3, 4 -> 2x2, 5-6 -> 3, 7-8 -> 4. */
export const galleryColumns = (count: number) => count <= 1 ? 1 : count === 4 ? 2 : count <= 3 ? count : count <= 6 ? 3 : 4;
