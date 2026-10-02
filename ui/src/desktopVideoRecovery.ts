// A hidden viewer can reconnect before its previous encoder has exited. Retry
// only that temporary slot conflict, with a small finite budget. These requests
// remain read-only and never start a desktop or renew its idle deadline.
const busyDelays = [250, 500, 1000];

function delay(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const cancelled = () => {
      clearTimeout(timer);
      signal.removeEventListener("abort", cancelled);
      reject(new DOMException("Desktop video cancelled", "AbortError"));
    };
    const timer = setTimeout(() => {
      signal.removeEventListener("abort", cancelled);
      resolve();
    }, ms);
    signal.addEventListener("abort", cancelled, { once: true });
    if (signal.aborted) cancelled();
  });
}

export async function openDesktopVideo(botId: string, signal: AbortSignal, cursor: "visible" | "hidden" = "visible"): Promise<Response> {
  for (let attempt = 0; ; attempt++) {
    signal.throwIfAborted();
    const query = cursor === "hidden" ? "?cursor=hidden" : "";
    const response = await fetch(`/api/bots/${encodeURIComponent(botId)}/computer/stream${query}`, { signal, credentials: "same-origin", cache: "no-store" });
    if (response.status !== 409 || attempt === busyDelays.length) return response;
    await response.body?.cancel();
    await delay(busyDelays[attempt], signal);
  }
}
