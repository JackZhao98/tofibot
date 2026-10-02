import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { CHAPTERS } from "./Chapter";

/** Chapter chips in the bar; the pill slides to whichever chapter is on screen. */
export function ChapterNav() {
  const [active, setActive] = useState<string | null>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const pillRef = useRef<HTMLSpanElement>(null);

  useEffect(() => {
    const sections = CHAPTERS.map(({ id }) => document.getElementById(id)).filter((node): node is HTMLElement => Boolean(node));
    if (!sections.length || typeof IntersectionObserver !== "function") return;
    const visible = new Map<string, number>();
    const observer = new IntersectionObserver((entries) => {
      entries.forEach((entry) => visible.set(entry.target.id, entry.isIntersecting ? entry.intersectionRatio : 0));
      const best = CHAPTERS.find(({ id }) => (visible.get(id) ?? 0) > 0);
      setActive(best?.id ?? null);
    }, { rootMargin: "-45% 0px -50% 0px", threshold: [0, 0.01] });
    sections.forEach((section) => observer.observe(section));
    return () => observer.disconnect();
  }, []);

  useLayoutEffect(() => {
    const list = listRef.current;
    const pill = pillRef.current;
    const link = active ? list?.querySelector<HTMLAnchorElement>(`a[href="#${active}"]`) : null;
    if (!list || !pill) return;
    if (!link) { pill.style.opacity = "0"; return; }
    pill.style.opacity = "1";
    pill.style.width = `${link.offsetWidth}px`;
    pill.style.transform = `translateX(${link.offsetLeft}px)`;
    link.scrollIntoView({ block: "nearest", inline: "nearest" });
  }, [active]);

  return (
    <div className="chapter-nav" ref={listRef}>
      <span className="chapter-pill" ref={pillRef} aria-hidden="true" />
      {CHAPTERS.map(({ id, label }) => (
        <a key={id} href={`#${id}`} aria-current={active === id ? "true" : undefined}>{label}</a>
      ))}
    </div>
  );
}
