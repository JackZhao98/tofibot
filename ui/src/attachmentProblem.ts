import type { AttachmentProblem } from "./filePreviewModel";

/** Why a stored file cannot be read right now (computer-decoupling S1/S2): its bytes are still only on a computer that is detached (409), or the computer answered and no longer has them (404). */
export class AttachmentUnavailable extends Error {
  constructor(readonly problem: AttachmentProblem) { super(problem); }
}
/** The problem a failed attachment response names, or null for any other failure. */
export async function attachmentProblem(response: Response): Promise<AttachmentProblem | null> {
  if (response.ok || (response.status !== 409 && response.status !== 404)) return null;
  try {
    const code = ((await response.clone().json()) as { error?: { code?: string } }).error?.code;
    return code === "file_on_detached_computer" || code === "file_missing_on_computer" ? code : null;
  } catch { return null; }
}
/** A failed response as an Error: `AttachmentUnavailable` when the server named a problem, else the status text. */
export async function attachmentFailure(response: Response): Promise<Error> {
  const problem = await attachmentProblem(response);
  return problem ? new AttachmentUnavailable(problem) : new Error(String(response.status));
}
/** Reads one byte to learn whether a file can be opened (the viewer shows images and PDFs through the browser, which hides the status). Null when it can, or fails for another reason. */
export async function probeProblem(url: string, signal?: AbortSignal): Promise<AttachmentProblem | null> {
  const response = await fetch(url, { credentials: "same-origin", headers: { Range: "bytes=0-0" }, signal });
  const problem = await attachmentProblem(response); // 416 (an empty file has no byte 0) is not a problem
  void response.body?.cancel();
  return problem;
}
