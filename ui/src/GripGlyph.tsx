/** Six dots in two columns: the handle shape people already read as "move". */
export function GripGlyph({ size = 16 }: { size?: number }) {
  return <svg width={size} height={size} viewBox="0 0 16 16" fill="currentColor" aria-hidden="true" focusable="false">
    {[4, 8, 12].map(y => [6, 10].map(x => <circle key={`${x}-${y}`} cx={x} cy={y} r="1.1" />))}
  </svg>;
}
