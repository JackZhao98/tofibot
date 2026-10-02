import { useEffect, useRef, useState } from "react";
import { openDesktopVideo } from "./desktopVideoRecovery";

export type DesktopVideoState = "connecting" | "live" | "fallback" | "stopped";
type Props = {
  botId: string;
  enabled: boolean;
  /** Hide the remote pointer while local takeover draws its own cursor. */
  cursor?: "visible" | "hidden";
  poster?: string;
  className?: string;
  onClick?: React.MouseEventHandler<HTMLVideoElement>;
  onState: (state: DesktopVideoState) => void;
};

const codec = 'video/mp4; codecs="avc1.42C020"';

// Keep a few complete fragments available for decoding. Hysteresis avoids
// alternating rates at every append; ordinary jitter never scrubs the video.
export function desktopPlaybackAdjustment(current: number, start: number, end: number, previousRate = 1) {
  const lag = end - current;
  if (current < start || lag > 1.5) return { time: Math.max(start, end - 0.3), rate: 1 };
  if (lag < 0.2) return { time: null, rate: 0.95 };
  if (lag > 0.5) return { time: null, rate: 1.12 };
  if (lag >= 0.3 && lag <= 0.4) return { time: null, rate: 1 };
  return { time: null, rate: previousRate };
}

// Full Xvfb desktop video. Input intentionally stays in the existing action
// API: a video connection cannot claim control or extend desktop idle time.
export function DesktopVideo({ botId, enabled, poster, cursor = "visible", className = "computer-screen", onState, onClick }: Props) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const [resumePoster, setResumePoster] = useState<{ botId: string; src: string } | null>(null);
  const resumePosterRef = useRef<{ botId: string; src: string } | null>(null);
  const notifyRef = useRef(onState);
  notifyRef.current = onState;

  useEffect(() => {
    if (!enabled) return;
    const video = videoRef.current;
    if (video) video.poster = resumePosterRef.current?.botId === botId ? resumePosterRef.current.src : poster ?? "";
    if (!video || typeof MediaSource === "undefined" || !MediaSource.isTypeSupported(codec)) {
      notifyRef.current("fallback");
      return;
    }
    let disposed = false;
    let session: AbortController | null = null;
    let objectURL = "";
    let lastState: DesktopVideoState | null = null;
    const notify = (state: DesktopVideoState) => {
      if (!disposed && lastState !== state) { lastState = state; notifyRef.current(state); }
    };
    const detach = () => {
      session?.abort();
      session = null;
      video.pause();
      video.removeAttribute("src");
      video.load();
      if (objectURL) URL.revokeObjectURL(objectURL);
      objectURL = "";
    };
    const freezeFrame = () => {
      // If a cursor-policy reconnect has not decoded its first frame yet,
      // retain the frame captured by the previous session. A failed capture
      // must only blank the poster when there is no same-Bot frame to keep.
      let src = resumePosterRef.current?.botId === botId ? resumePosterRef.current.src : "";
      // Capture once when hiding, never on a frame timer. An empty override is
      // intentional when decoding/canvas capture is unavailable: the initial
      // connection poster may show an entirely different, earlier page.
      if (video.readyState >= 2 && video.videoWidth > 0 && video.videoHeight > 0 && video.videoWidth * video.videoHeight <= 1280 * 800) {
        const canvas = document.createElement("canvas");
        try {
          canvas.width = video.videoWidth;
          canvas.height = video.videoHeight;
          const context = canvas.getContext("2d");
          if (context) {
            context.drawImage(video, 0, 0);
            const captured = canvas.toDataURL("image/jpeg", 0.85);
            if (captured.startsWith("data:image/")) src = captured;
          }
        } catch { /* A blocked capture must not bring back a stale poster. */ }
        finally { canvas.width = 0; canvas.height = 0; }
      }
      // Set the DOM property before load() resets the decoded frame; state
      // retains this override through parent renders until a new frame arrives.
      video.poster = src;
      const preserved = { botId, src };
      resumePosterRef.current = preserved;
      setResumePoster(preserved);
    };
    const start = async () => {
      detach();
      if (disposed || document.hidden) return;
      const controller = new AbortController();
      session = controller;
      const { signal } = controller;
      notify("connecting");
      const source = new MediaSource();
      objectURL = URL.createObjectURL(source);
      video.src = objectURL;
      let buffer: SourceBuffer | null = null;
      let watchdog = window.setTimeout(() => controller.abort(), 12000);
      let receivedFrame = false;
      let playPending = false;
      const decodeDeadline = Date.now() + 12000;
      let playbackTime = 0;
      let playbackChangedAt = Date.now();
      const waitEvent = (target: EventTarget, event: string, operation?: () => void) => new Promise<void>((resolve, reject) => {
        const cleanup = () => {
          target.removeEventListener(event, success);
          target.removeEventListener("error", failure);
          signal.removeEventListener("abort", failure);
        };
        const success = () => { cleanup(); resolve(); };
        const failure = () => { cleanup(); reject(new Error("Desktop video interrupted")); };
        if (signal.aborted) { reject(new Error("Desktop video cancelled")); return; }
        target.addEventListener(event, success, { once: true });
        target.addEventListener("error", failure, { once: true });
        signal.addEventListener("abort", failure, { once: true });
        if (operation) { try { operation(); } catch { failure(); } }
      });
      const decoded = () => {
        if (!signal.aborted && !receivedFrame) {
          receivedFrame = true;
          resumePosterRef.current = null;
          setResumePoster(null);
          notify("live");
        }
      };
      video.addEventListener("loadeddata", decoded);
      try {
        await waitEvent(source, "sourceopen");
        const response = await openDesktopVideo(botId, signal, cursor);
        if (response.status === 410) { notify("stopped"); return; }
        if (!response.ok || !response.body) throw new Error("Desktop video unavailable");
        buffer = source.addSourceBuffer(codec);
        const reader = response.body.getReader();
        // Await each append before reading another chunk: the browser, app,
        // manager and encoder all apply bounded backpressure end to end.
        while (!signal.aborted) {
          const { value, done } = await reader.read();
          if (done) throw new Error("Desktop video ended");
          if (!value?.byteLength) continue;
          if (value.byteLength > 4 * 1024 * 1024) throw new Error("Desktop video chunk too large");
          window.clearTimeout(watchdog);
          watchdog = window.setTimeout(() => controller.abort(), 10000);
          if (!receivedFrame && Date.now() > decodeDeadline) throw new Error("Desktop video did not decode");
          if (video.currentTime !== playbackTime) {
            playbackTime = video.currentTime;
            playbackChangedAt = Date.now();
          } else if (receivedFrame && Date.now() - playbackChangedAt > 6000) throw new Error("Desktop video stalled");
          if (source.readyState !== "open") throw new Error("Desktop video closed");
          if (buffer.buffered.length && video.currentTime > 15 && buffer.buffered.start(0) < video.currentTime - 10) {
            const currentBuffer = buffer;
            await waitEvent(buffer, "updateend", () => currentBuffer.remove(0, video.currentTime - 5));
          }
          const currentBuffer = buffer;
          await waitEvent(buffer, "updateend", () => currentBuffer.appendBuffer(value));
          if (buffer.buffered.length) {
            const range = buffer.buffered.length - 1;
            const end = buffer.buffered.end(range);
            const adjustment = desktopPlaybackAdjustment(video.currentTime, buffer.buffered.start(range), end, video.playbackRate);
            if (adjustment.time !== null) video.currentTime = adjustment.time;
            if (video.playbackRate !== adjustment.rate) video.playbackRate = adjustment.rate;
            // play() may wait for more buffered frames. Never await it in
            // the producer loop or a browser's startup threshold can deadlock.
            if (video.paused && !playPending && end - video.currentTime >= 0.3) {
              playPending = true;
              void video.play().catch(() => controller.abort()).finally(() => { playPending = false; });
            }
          }
        }
      } catch {
        if (!disposed && session === controller && !document.hidden) {
          video.pause();
          notify("fallback");
        }
      } finally {
        window.clearTimeout(watchdog);
        video.removeEventListener("loadeddata", decoded);
        controller.abort();
        if (session === controller) {
          // Keep the last decoded frame/poster visible until the parent swaps
          // to its screenshot fallback; never flash a per-frame loading UI.
          session = null;
        }
      }
    };
    const visibility = () => {
      if (document.hidden) { freezeFrame(); detach(); notify("connecting"); }
      else void start();
    };
    document.addEventListener("visibilitychange", visibility);
    void start();
    return () => {
      disposed = true;
      document.removeEventListener("visibilitychange", visibility);
      freezeFrame();
      detach();
    };
  }, [botId, enabled, cursor]);

  const visiblePoster = enabled && resumePoster?.botId === botId ? resumePoster.src : poster;
  return <video ref={videoRef} className={className} poster={visiblePoster} onClick={onClick} muted playsInline disablePictureInPicture aria-label="Bot 当前桌面实时视频" style={{ width: "100%", aspectRatio: "8 / 5", objectFit: "contain" }} />;
}
