// Three small "screenshots" drawn in SVG with theme tokens, so the zoom stays crisp at any size.

export function ChartArt() {
  const bars = [46, 72, 58, 96, 84, 118, 104];
  return (
    <svg viewBox="0 0 320 200" className="shot-art" aria-hidden="true">
      <rect width="320" height="200" rx="10" fill="var(--surface)" />
      <text x="20" y="30" fontSize="13" fontWeight="600" fill="var(--ink)">本周完成的任务</text>
      <text x="20" y="48" fontSize="10" fill="var(--muted)">按天 · 共 578 次</text>
      {[80, 115, 150].map((y) => <line key={y} x1="20" x2="300" y1={y} y2={y} stroke="var(--line-soft)" strokeDasharray="3 4" />)}
      {bars.map((height, index) => (
        <rect key={index} x={28 + index * 39} y={180 - height} width="24" height={height} rx="5" fill={index === 5 ? "var(--clay)" : "var(--lagoon)"} opacity={index === 5 ? 1 : 0.75} />
      ))}
    </svg>
  );
}

export function TermArt() {
  const lines = [
    ["$ ", "go test ./...", "var(--ink)"],
    ["ok  ", "internal/app  25.8s", "var(--green-text)"],
    ["ok  ", "internal/agent  4.1s", "var(--green-text)"],
    ["$ ", "npm run build", "var(--ink)"],
    ["✓ ", "built in 266ms", "var(--lagoon-text)"],
  ] as const;
  return (
    <svg viewBox="0 0 320 200" className="shot-art" aria-hidden="true">
      <rect width="320" height="200" rx="10" fill="var(--code-block-bg)" />
      {["var(--danger)", "var(--honey)", "var(--green)"].map((color, index) => <circle key={color} cx={20 + index * 16} cy="18" r="5" fill={color} />)}
      {lines.map(([prompt, text, color], index) => (
        <text key={text} x="20" y={52 + index * 26} fontSize="12.5" fontFamily="JetBrains Mono, monospace" fill="var(--muted)">
          {prompt}<tspan fill={color}>{text}</tspan>
        </text>
      ))}
    </svg>
  );
}

export function BoardArt() {
  const columns = [["待办", 3, "var(--surface-2)"], ["进行中", 2, "color-mix(in srgb, var(--lagoon) 35%, var(--surface))"], ["完成", 4, "color-mix(in srgb, var(--green) 30%, var(--surface))"]] as const;
  return (
    <svg viewBox="0 0 320 200" className="shot-art" aria-hidden="true">
      <rect width="320" height="200" rx="10" fill="var(--bg)" />
      {columns.map(([title, count, fill], column) => (
        <g key={title} transform={`translate(${14 + column * 102} 14)`}>
          <text x="4" y="12" fontSize="11" fontWeight="600" fill="var(--ink-soft)">{title} · {count}</text>
          {Array.from({ length: count }, (_, index) => (
            <rect key={index} y={22 + index * 40} width="92" height="32" rx="7" fill={fill} stroke="var(--line-soft)" />
          ))}
        </g>
      ))}
    </svg>
  );
}
