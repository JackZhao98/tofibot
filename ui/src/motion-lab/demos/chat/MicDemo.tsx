import { useEffect, useRef, useState } from "react";
import { ComposerGlyph } from "../../../ComposerGlyph";
import { TofiIcon } from "../../../icons";
import { prefersReducedMotion } from "../../lib/hooks";
import "./chat-cards.css";

type Mode = "sample" | "live" | "denied";
const BARS = 40;

type Mic = { stream: MediaStream; context: AudioContext; analyser: AnalyserNode };

/** Voice waveform driven by the real microphone level (analysed locally, never recorded). */
export function MicDemo() {
  const barsRef = useRef<HTMLSpanElement>(null);
  const mic = useRef<Mic | null>(null);
  const [mode, setMode] = useState<Mode>("sample");

  const stop = () => {
    mic.current?.stream.getTracks().forEach((track) => track.stop());
    void mic.current?.context.close().catch(() => undefined);
    mic.current = null;
  };

  const start = async () => {
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      const context = new AudioContext();
      const analyser = context.createAnalyser();
      analyser.fftSize = 128;
      analyser.smoothingTimeConstant = 0.72;
      context.createMediaStreamSource(stream).connect(analyser);
      mic.current = { stream, context, analyser };
      setMode("live");
    } catch {
      setMode("denied");
    }
  };

  useEffect(() => () => stop(), []);

  useEffect(() => {
    const bars = Array.from(barsRef.current?.children ?? []) as HTMLElement[];
    const levels = new Uint8Array(64);
    let frame = 0;
    const draw = (now: number) => {
      const analyser = mic.current?.analyser;
      if (mode === "live" && analyser) analyser.getByteFrequencyData(levels);
      bars.forEach((bar, index) => {
        let level: number;
        if (mode === "live" && analyser) {
          // Speech lives in the low bins; mirror them around the centre.
          const bin = Math.min(levels.length - 1, 1 + Math.abs(index - BARS / 2));
          level = levels[bin] / 255;
        } else {
          const t = now / 1000;
          level = 0.18 + 0.5 * Math.abs(Math.sin(t * 2.2 + index * 0.45) * Math.sin(t * 0.8 + index * 0.13));
        }
        bar.style.transform = `scaleY(${Math.max(0.08, level).toFixed(3)})`;
      });
      frame = requestAnimationFrame(draw);
    };
    if (prefersReducedMotion() && mode !== "live") {
      bars.forEach((bar, index) => { bar.style.transform = `scaleY(${0.2 + 0.2 * Math.abs(Math.sin(index * 0.6))})`; });
      return;
    }
    frame = requestAnimationFrame(draw);
    return () => cancelAnimationFrame(frame);
  }, [mode]);

  const live = mode === "live";
  return (
    <div className="mic-demo">
      <div className={`mic-wave${live ? " is-live" : ""}`}>
        <span className="mic-bars" ref={barsRef} aria-hidden="true">{Array.from({ length: BARS }, (_, index) => <i key={index} />)}</span>
      </div>
      <p className="mic-state" role="status">
        {live ? "正在听你的麦克风音量" : mode === "denied" ? "没拿到麦克风权限，先看模拟波形" : "现在是模拟波形"}
      </p>
      <div className="demo-actions">
        {live
          ? <button type="button" className="lab-btn" onClick={() => { stop(); setMode("sample"); }}><TofiIcon name="stop" size={16} />停止</button>
          : <button type="button" className="lab-btn primary" onClick={() => void start()}><ComposerGlyph name="mic" size={16} />用我的麦克风</button>}
        <span className="mic-note">只在这个页面里分析音量，不录音，不上传。</span>
      </div>
    </div>
  );
}
