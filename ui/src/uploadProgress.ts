import type { Attachment } from "./types";

/** What the XHR upload needs from the app (so this chunk imports nothing): the same error shape request() throws, and its proxy-challenge recovery. */
export type UploadEnv = { fail: (status: number, body: unknown) => Error; viaRequest: (path: string, body: FormData) => Promise<Attachment> };

/** An upload with progress. fetch reports none, so the same request goes out through XHR (same path, body and credentials). */
export function uploadWithProgress(env: UploadEnv, path: string, file: File, onProgress: (fraction: number) => void) {
  const body = new FormData(); body.append("file", file);
  return new Promise<Attachment>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", path);
    xhr.withCredentials = true;
    xhr.upload.onprogress = event => { if (event.lengthComputable && event.total > 0) onProgress(Math.min(1, event.loaded / event.total)); };
    // A dropped connection is the same TypeError fetch would throw, so callers tell "offline" from a refusal one way.
    xhr.onerror = xhr.ontimeout = () => reject(new TypeError("Network request failed"));
    xhr.onload = () => {
      let parsed: unknown = null;
      try { parsed = JSON.parse(xhr.responseText); } catch { /* an empty or non-JSON body */ }
      if (xhr.status >= 200 && xhr.status < 300) { onProgress(1); resolve(parsed as Attachment); return; }
      // A proxy challenge has its own recovery in request(); hand the upload to it once (it reports no progress).
      if (xhr.status === 403 || xhr.status === 503) { env.viaRequest(path, body).then(resolve, reject); return; }
      if (xhr.status === 401) window.dispatchEvent(new Event("tofi:unauthorized"));
      reject(env.fail(xhr.status, parsed));
    };
    xhr.send(body);
  });
}
