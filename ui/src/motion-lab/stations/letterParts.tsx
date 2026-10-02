import type { Ref } from "react";
import { CatStage } from "../lib/CatStage";
import { BOTS } from "../lib/crew";

export const LETTER = {
  to: "团队 <team@tofi.dev>",
  subject: "本周周报 · 9 月 28 日",
  lines: [
    "大家好，",
    "这周三件事落地：表单跨轮保留、重启后恢复等待、执行历史可查。",
    "下周重点是外部任务的恢复，周三前给出方案。",
    "—— 写作搭子 代发",
  ],
} as const;

/** The cat stamp: perforated edge, live cat inside. Cloned (as a still frame) when the letter folds. */
export function CatStamp({ className }: { className?: string }) {
  return (
    <span className={`cat-stamp ${className ?? ""}`}>
      <CatStage config={BOTS.mail.cat} />
    </span>
  );
}

/** Paper only (stripe, header, ruled body). This node is what the fold clones. */
export function LetterPaper({ ref }: { ref?: Ref<HTMLDivElement> }) {
  return (
    <div className="letter-paper" ref={ref}>
      <div className="airmail" aria-hidden="true" />
      <div className="letter-head">
        <dl>
          <div><dt>收件人</dt><dd>{LETTER.to}</dd></div>
          <div><dt>主题</dt><dd>{LETTER.subject}</dd></div>
        </dl>
        <CatStamp />
      </div>
      <div className="letter-body">
        {LETTER.lines.map((line) => <p key={line}>{line}</p>)}
      </div>
    </div>
  );
}

/** Round postmark with a cancellation wave, drawn once in SVG. */
export function Postmark({ ref }: { ref?: Ref<SVGSVGElement> }) {
  return (
    <svg className="postmark" ref={ref} viewBox="0 0 150 80" aria-hidden="true">
      <defs>
        <path id="postmark-ring" d="M40 16a24 24 0 1 1 0 48a24 24 0 1 1 0-48" />
      </defs>
      <circle cx="40" cy="40" r="31" fill="none" stroke="currentColor" strokeWidth="2.4" />
      <circle cx="40" cy="40" r="17" fill="none" stroke="currentColor" strokeWidth="1.4" />
      <text className="pm-ring" fontSize="7.4" fontWeight="600" letterSpacing="1.4" fill="currentColor">
        <textPath href="#postmark-ring">TOFI POST · TOFI POST ·</textPath>
      </text>
      <text className="pm-date" x="40" y="43.5" textAnchor="middle" fontSize="9" fontWeight="700" fill="currentColor">9.28</text>
      {[26, 36, 46, 56].map((y) => (
        <path key={y} d={`M76 ${y}q9 -6 18 0t18 0t18 0t18 0`} fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" />
      ))}
    </svg>
  );
}

type EnvelopeRefs = {
  envelope: Ref<HTMLDivElement>;
  inner: Ref<HTMLDivElement>;
  flap: Ref<HTMLDivElement>;
  stamp: Ref<HTMLSpanElement>;
  mark: Ref<SVGSVGElement>;
};

/** Back, the folded letter, front pocket, flap; stamp and postmark sit on top. */
export function Envelope({ refs }: { refs: EnvelopeRefs }) {
  return (
    <div className="envelope" ref={refs.envelope} aria-hidden="true">
      <div className="env-flap" ref={refs.flap}>
        <svg viewBox="0 0 100 58" preserveAspectRatio="none"><path d="M1.5 1.5 L50 56 L98.5 1.5 Z" /></svg>
      </div>
      <div className="env-back" />
      <div className="env-letter" ref={refs.inner} />
      <div className="env-front">
        <svg viewBox="0 0 100 60" preserveAspectRatio="none"><path d="M1.5 1.5 L50 34 L98.5 1.5 L98.5 58.5 L1.5 58.5 Z" /></svg>
      </div>
      <span className="env-stamp" ref={refs.stamp}><CatStamp /></span>
      <span className="env-mark"><Postmark ref={refs.mark} /></span>
      <span className="env-address">团队 · team@tofi.dev</span>
    </div>
  );
}
