import type { ReactNode } from "react";

export type ChapterInfo = Readonly<{ id: string; label: string }>;

/** The product areas, in page order; also drives the bar's chapter nav. */
export const CHAPTERS: readonly ChapterInfo[] = [
  { id: "chat", label: "对话" },
  { id: "sidebar", label: "侧栏" },
  { id: "bot", label: "Bot" },
  { id: "team", label: "团队" },
  { id: "computer", label: "电脑与终端" },
  { id: "settings", label: "设置与连接" },
  { id: "global", label: "全局" },
];

type ChapterProps = { id: string; title: string; intro: string; children: ReactNode };

export function Chapter({ id, title, intro, children }: ChapterProps) {
  return (
    <section className="chapter" id={id} aria-labelledby={`${id}-chapter`}>
      <header className="chapter-head">
        <h2 id={`${id}-chapter`}>{title}</h2>
        <p>{intro}</p>
      </header>
      {children}
    </section>
  );
}

type DemoCardProps = { title: string; note: ReactNode; children: ReactNode; wide?: boolean };

/** A small interaction: the live stage first, then one line on what it shows. */
export function DemoCard({ title, note, children, wide = false }: DemoCardProps) {
  return (
    <article className={`demo-card${wide ? " is-wide" : ""}`}>
      <div className="demo-stage">{children}</div>
      <div className="demo-copy">
        <h3>{title}</h3>
        <p>{note}</p>
      </div>
    </article>
  );
}

export function DemoGrid({ children }: { children: ReactNode }) {
  return <div className="demo-grid">{children}</div>;
}
