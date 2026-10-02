/** The new design kit explicitly retains these two drawings: neither has a
 * counterpart in the 144-icon catalog. All other UI glyphs use TofiIcon. */
export function ComposerGlyph({ name, size = 20 }: { name: "mic" | "screenshot"; size?: number }) {
  return <svg data-composer-glyph={name} width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={name === "mic" ? 2.2 : 2} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
    {name === "mic" ? <><rect x="8.5" y="3" width="7" height="12" rx="3.5" fill="currentColor" fillOpacity=".15" /><path d="M5.5 11.5a6.5 6.5 0 0 0 13 0M12 18v3" /></> : <><path d="M4 8V5.5A1.5 1.5 0 0 1 5.5 4H8M16 4h2.5A1.5 1.5 0 0 1 20 5.5V8M20 16v2.5a1.5 1.5 0 0 1-1.5 1.5H16M8 20H5.5A1.5 1.5 0 0 1 4 18.5V16" /><circle cx="12" cy="12" r="3" /></>}
  </svg>;
}
