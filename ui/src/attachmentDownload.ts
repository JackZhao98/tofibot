import { attachmentProblem, type AttachmentProblem } from "./filePreviewModel";

/** Lazy: loaded on the first Download click. */
function saveFromLink(href: string, name: string) {
  const link = document.createElement("a");
  link.href = href;
  link.download = name;
  link.rel = "noopener";
  document.body.append(link);
  link.click();
  link.remove();
}

/**
 * Downloads one stored file in the browser. A file whose bytes are only on a detached computer (409), or that the
 * computer no longer has (404), comes back as its problem so the card can say so; any other failure falls back to the
 * plain link, which the browser reports the way it always did.
 */
export async function downloadAttachment(url: string, name: string): Promise<AttachmentProblem | null> {
  let response: Response;
  try { response = await fetch(url, { credentials: "same-origin" }); }
  catch { saveFromLink(url, name); return null; }
  if (!response.ok) {
    const problem = await attachmentProblem(response);
    if (!problem) saveFromLink(url, name);
    return problem;
  }
  const blobUrl = URL.createObjectURL(await response.blob());
  saveFromLink(blobUrl, name);
  window.setTimeout(() => URL.revokeObjectURL(blobUrl), 30_000);
  return null;
}
