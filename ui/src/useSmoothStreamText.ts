import { useEffect, useRef, useState } from "react";

// Presentation only: every frame is a prefix of the text already received.
export function useSmoothStreamText(content: string, active: boolean) {
  const [visible, setVisible] = useState(() => active ? "" : content);
  const visibleRef = useRef(visible);

  useEffect(() => {
    if (!active || window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      visibleRef.current = content;
      setVisible(content);
      return;
    }
    if (!content.startsWith(visibleRef.current)) {
      visibleRef.current = "";
      setVisible("");
    }
    const tail = Array.from(content.slice(visibleRef.current.length));
    let offset = 0;
    let frame = 0;
    let last = 0;
    const reveal = (now: number) => {
      if (now - last < 14) { frame = requestAnimationFrame(reveal); return; }
      last = now;
      // Catch up within roughly one second when the server sends a large block.
      const count = Math.max(1, Math.ceil((tail.length - offset) / 60));
      visibleRef.current += tail.slice(offset, offset + count).join("");
      offset += count;
      setVisible(visibleRef.current);
      if (offset < tail.length) frame = requestAnimationFrame(reveal);
    };
    if (tail.length) frame = requestAnimationFrame(reveal);
    return () => cancelAnimationFrame(frame);
  }, [active, content]);

  return active ? visible : content;
}
