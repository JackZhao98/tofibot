import { useEffect, useRef, useState } from "react";

const reducedMotion = () => window.matchMedia("(prefers-reduced-motion: reduce)").matches;

// Presentation only: every frame is a prefix of the text already received.
// When the stream ends (the final message replaces the draft) with text still
// unrevealed, the rest keeps revealing quickly instead of appearing at once.
export function useSmoothStreamText(content: string, active: boolean) {
  const [visible, setVisible] = useState(() => active ? "" : content);
  const visibleRef = useRef(visible);
  const streamed = useRef(active);
  if (active) streamed.current = true;
  // Decided during render so the frame the stream ends never shows the full text.
  const settling = !active && streamed.current && visibleRef.current.length < content.length && content.startsWith(visibleRef.current) && !reducedMotion();

  useEffect(() => {
    if ((!active && !settling) || reducedMotion()) {
      visibleRef.current = content;
      setVisible(content);
      return;
    }
    if (!content.startsWith(visibleRef.current)) {
      visibleRef.current = "";
      setVisible("");
    }
    const tail = Array.from(content.slice(visibleRef.current.length));
    // Streaming catches up within about a second; the ending within about 0.35s at an even pace.
    const finishStep = Math.max(1, Math.ceil(tail.length / 24));
    let offset = 0;
    let frame = 0;
    let last = 0;
    const reveal = (now: number) => {
      if (now - last < 14) { frame = requestAnimationFrame(reveal); return; }
      last = now;
      const count = settling ? finishStep : Math.max(1, Math.ceil((tail.length - offset) / 60));
      visibleRef.current += tail.slice(offset, offset + count).join("");
      offset += count;
      setVisible(visibleRef.current);
      if (offset < tail.length) frame = requestAnimationFrame(reveal);
    };
    if (tail.length) frame = requestAnimationFrame(reveal);
    return () => cancelAnimationFrame(frame);
  }, [active, content, settling]);

  return active || settling ? visible : content;
}
