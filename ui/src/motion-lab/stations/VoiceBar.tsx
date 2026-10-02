import { useEffect, useRef, useState } from "react";
import { TofiIcon } from "../../icons";
import { prefersReducedMotion } from "../lib/hooks";

const BARS = 34;

type VoiceBarProps = {
  onCancel: () => void;
  onDone: () => void;
};

function clock(seconds: number): string {
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

/** Recording row: ✕ cancel · red dot timer · live lagoon waveform · ✓ done (takes the send slot). */
export function VoiceBar({ onCancel, onDone }: VoiceBarProps) {
  const barsRef = useRef<HTMLSpanElement>(null);
  const [seconds, setSeconds] = useState(0);

  useEffect(() => {
    const timer = window.setInterval(() => setSeconds((value) => value + 1), 1000);
    return () => window.clearInterval(timer);
  }, []);

  // Layered sines with a slow "syllable" envelope read as speech, not a meter.
  useEffect(() => {
    const bars = Array.from(barsRef.current?.children ?? []) as HTMLElement[];
    if (prefersReducedMotion()) {
      bars.forEach((bar, index) => { bar.style.transform = `scaleY(${0.3 + 0.25 * Math.abs(Math.sin(index * 0.7))})`; });
      return;
    }
    let frame = 0;
    const draw = (now: number) => {
      const t = now / 1000;
      const envelope = 0.35 + 0.65 * Math.abs(Math.sin(t * 2.3) * Math.sin(t * 0.9 + 1));
      bars.forEach((bar, index) => {
        const wave = Math.abs(Math.sin(t * 7 + index * 0.55) * 0.6 + Math.sin(t * 11.3 - index * 0.9) * 0.4);
        const centre = 1 - Math.abs(index - BARS / 2) / (BARS / 2);
        bar.style.transform = `scaleY(${(0.12 + wave * envelope * (0.45 + centre * 0.55)).toFixed(3)})`;
      });
      frame = requestAnimationFrame(draw);
    };
    frame = requestAnimationFrame(draw);
    return () => cancelAnimationFrame(frame);
  }, []);

  return (
    <div className="voice-bar" role="group" aria-label="正在录音">
      <button type="button" className="composer-icon" aria-label="取消录音" onClick={onCancel}><TofiIcon name="close" size={20} /></button>
      <span className="voice-clock"><span className="rec-dot" aria-hidden="true" />{clock(seconds)}</span>
      <span className="voice-wave" ref={barsRef} aria-hidden="true">
        {Array.from({ length: BARS }, (_, index) => <span key={index} />)}
      </span>
      <button type="button" className="send-button is-ready" aria-label="完成录音" onClick={onDone}><TofiIcon name="check" size={24} variant="filled" /></button>
    </div>
  );
}
