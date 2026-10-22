/**
 * The headline of a reasoning summary: its bold title, shown only when complete.
 * Stream chunks can begin or end mid-sentence, so plain prose is never sliced
 * into a status line; the caller falls back to a plain "Thinking".
 */
export function thinkingHeadline(text: string): string {
  const bold = [...text.matchAll(/\*\*([^*\n]{2,80})\*\*/g)].at(-1)?.[1]?.replace(/[_`#]/g, "").trim() ?? "";
  if (!bold) return "";
  // The title is shown whole; the line it sits on ellipsizes by width, so no word is ever cut here.
  return bold;
}
