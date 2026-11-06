/**
 * The power symbol (IEC 5009), drawn the way the Tofi icon set draws: 24 grid, 1.8 stroke, round caps and joins. The
 * 144-icon set has no counterpart yet; like SendReceipt it is drawn inline until the set gains one.
 */
export function PowerGlyph({ size = 16 }: { size?: number }) {
  return <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
    <path d="M12 3.6V12" />
    <path d="M17.09 7.71A7.2 7.2 0 1 1 6.91 7.71" />
  </svg>;
}
