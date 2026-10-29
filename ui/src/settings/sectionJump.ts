/**
 * Scrolls to a section by its stable id and flashes it once with `data-jump-target` (the search palette's pattern, not a class,
 * because React rewrites className). The page may still be loading when the link arrives, so it looks for up to about a second.
 */
import "./section-jump.css";

const FLASH_MS = 1700;
const MAX_FRAMES = 90;

export function jumpToSection(root: ParentNode | null, section: string): boolean {
  const node = root?.querySelector<HTMLElement>(`[data-section="${section.replace(/["\\]/g, "")}"]`);
  if (!node) return false;
  node.scrollIntoView({ block: "start", inline: "nearest" });
  node.removeAttribute("data-jump-target");
  void node.offsetWidth; // restart the flash when the same section is jumped to twice
  node.setAttribute("data-jump-target", "");
  window.setTimeout(() => node.removeAttribute("data-jump-target"), FLASH_MS);
  return true;
}

/** Retries `jumpToSection` each frame until the section exists; `done` runs when it landed or the search gave up. Returns the cancel function. */
export function jumpWhenReady(find: () => ParentNode | null, section: string, done?: () => void): () => void {
  let frame = 0;
  let id = 0;
  const step = () => {
    if (jumpToSection(find(), section) || ++frame >= MAX_FRAMES) { done?.(); return; }
    id = requestAnimationFrame(step);
  };
  id = requestAnimationFrame(step);
  return () => cancelAnimationFrame(id);
}
