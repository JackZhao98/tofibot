/**
 * The headline of a reasoning summary: its bold title, shown only when complete.
 * Stream chunks can begin or end mid-sentence, so plain prose is never sliced
 * into a status line; the caller falls back to a plain "Thinking".
 */
export function thinkingHeadline(text: string): string {
  const bold = [...text.matchAll(/\*\*([^*\n]{2,80})\*\*/g)].at(-1)?.[1]?.replace(/[_`#]/g, "").trim() ?? "";
  if (!bold) return "";
  if (bold.length <= 40) return bold;
  // Over-long titles are cut at a word boundary from the start, never mid-word.
  const cut = bold.slice(0, 40).replace(/\s+\S*$/, "").replace(/[\s,;:\u2014-]+$/, "");
  return cut.length >= 8 ? `${cut}\u2026` : "";
}
