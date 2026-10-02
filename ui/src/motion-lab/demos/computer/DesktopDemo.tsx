import { useEffect, useRef, useState, type PointerEvent } from "react";
import { flushSync } from "react-dom";
import { TofiIcon, type TofiIconName } from "../../../icons";
import { CatStage } from "../../lib/CatStage";
import { BOTS } from "../../lib/crew";
import { arcPoints } from "../../lib/choreo";
import { stepSpring, type SpringConfig, type SpringState } from "../../lib/spring";
import { prefersReducedMotion, settled, useSeenOnce } from "../../lib/hooks";
import "./computer.css";

type Power = "off" | "booting" | "on";
type Driver = "bot" | "you";
type Step = Readonly<{ target: string; act: "tap" | "open" | "type" | "close" }>;

// One short errand, repeated; targets are real elements on the screen.
const ERRAND: readonly Step[] = [
  { target: "files", act: "tap" },
  { target: "browser", act: "open" },
  { target: "search", act: "type" },
  { target: "close", act: "close" },
];
const POINTER: SpringConfig = { stiffness: 110, damping: 16, mass: 1 };
const ICONS: readonly { id: string; icon: TofiIconName; label: string }[] = [
  { id: "files", icon: "folder", label: "文件" },
  { id: "browser", icon: "browser", label: "浏览器" },
  { id: "terminal", icon: "terminal", label: "终端" },
];

/** FloatingDesktop: CRT boot with the lagoon glow, a spring-driven Bot pointer, and handing control over. */
export function DesktopDemo() {
  const [rootRef, seen] = useSeenOnce<HTMLDivElement>();
  const screenRef = useRef<HTMLDivElement>(null);
  const pointerRef = useRef<HTMLDivElement>(null);
  const tokenRef = useRef<HTMLSpanElement>(null);
  const botSlot = useRef<HTMLSpanElement>(null);
  const youSlot = useRef<HTMLSpanElement>(null);
  const [power, setPower] = useState<Power>("off");
  const [driver, setDriver] = useState<Driver>("bot");
  const [windowOpen, setWindowOpen] = useState(false);
  const [typing, setTyping] = useState(false);
  const [tapped, setTapped] = useState<string | null>(null);
  const [ripples, setRipples] = useState<readonly { key: number; x: number; y: number }[]>([]);
  const [you, setYou] = useState<{ x: number; y: number } | null>(null);
  const rippleKey = useRef(0);

  const boot = async () => {
    const screen = screenRef.current;
    if (!screen || power !== "off") return;
    setPower("booting");
    if (!prefersReducedMotion()) {
      // Draw the bright line, hold it for a beat, then open it up like a CRT.
      await settled(screen.animate([
        { transform: "scale(0, .006)", filter: "brightness(3)", easing: "cubic-bezier(.2,.8,.2,1)" },
        { transform: "scale(1, .006)", filter: "brightness(3)", offset: 0.3 },
        { transform: "scale(1, .006)", filter: "brightness(2.4)", offset: 0.42, easing: "cubic-bezier(.3,.7,.2,1)" },
        { transform: "scale(1, 1.02)", filter: "brightness(1.4)", offset: 0.84 },
        { transform: "scale(1, 1)", filter: "brightness(1)" },
      ], { duration: 980, easing: "linear" }));
    }
    setPower("on");
  };

  const shutdown = async () => {
    const screen = screenRef.current;
    if (!screen || power !== "on") return;
    setDriver("bot");
    setWindowOpen(false);
    if (!prefersReducedMotion()) {
      await settled(screen.animate([
        { transform: "scale(1, 1)", filter: "brightness(1)" },
        { transform: "scale(1, .006)", filter: "brightness(2.6)", offset: 0.55 },
        { transform: "scale(0, .006)", filter: "brightness(3)" },
      ], { duration: 520, easing: "cubic-bezier(.5,0,.75,.2)", fill: "forwards" }));
      screen.getAnimations().forEach((animation) => animation.cancel());
    }
    setPower("off");
  };

  useEffect(() => { if (seen) void boot(); }, [seen]); // eslint-disable-line react-hooks/exhaustive-deps

  // The Bot pointer: springs toward each errand stop, acts on arrival, moves on.
  useEffect(() => {
    const screen = screenRef.current;
    const pointer = pointerRef.current;
    if (power !== "on" || driver !== "bot" || !screen || !pointer) return;
    let x: SpringState = { position: screen.clientWidth * 0.5, velocity: 0 };
    let y: SpringState = { position: screen.clientHeight * 0.7, velocity: 0 };
    let step = 0;
    const base = () => screen.getBoundingClientRect();
    // Aim at the element's centre; before it exists (window not open yet) aim at the middle.
    const locate = (target: string) => {
      const element = screen.querySelector<HTMLElement>(`[data-target="${target}"]`);
      const box = base();
      if (!element) return { x: box.width / 2, y: box.height / 2 };
      const rect = element.getBoundingClientRect();
      return { x: rect.left - box.left + rect.width / 2, y: rect.top - box.top + rect.height / 2 };
    };
    let waitUntil = performance.now() + 400;
    let last = performance.now();
    let frame = 0;
    const act = (stop: Step, px: number, py: number) => {
      const key = rippleKey.current++;
      setRipples((current) => [...current.slice(-3), { key, x: px, y: py }]);
      if (stop.act === "tap") setTapped("files");
      if (stop.act === "open") { setTapped("browser"); setWindowOpen(true); }
      if (stop.act === "type") setTyping(true);
      if (stop.act === "close") { setWindowOpen(false); setTyping(false); setTapped(null); }
    };
    const loop = (now: number) => {
      const dt = Math.min(0.05, (now - last) / 1000);
      last = now;
      const stop = ERRAND[step % ERRAND.length];
      const { x: tx, y: ty } = locate(stop.target);
      if (now >= waitUntil) {
        x = stepSpring(x, tx, POINTER, dt);
        y = stepSpring(y, ty, POINTER, dt);
        if (Math.hypot(x.position - tx, y.position - ty) < 1.5 && Math.hypot(x.velocity, y.velocity) < 25) {
          act(stop, tx, ty);
          step += 1;
          waitUntil = now + (stop.act === "type" ? 1500 : 650);
        }
      }
      pointer.style.transform = `translate(${x.position.toFixed(1)}px, ${y.position.toFixed(1)}px)`;
      frame = requestAnimationFrame(loop);
    };
    if (prefersReducedMotion()) {
      const rest = locate("browser");
      pointer.style.transform = `translate(${rest.x}px, ${rest.y}px)`;
      return;
    }
    frame = requestAnimationFrame(loop);
    return () => cancelAnimationFrame(frame);
  }, [power, driver]);

  // The mouse is handed over as a token travelling between the two avatars.
  const handOver = async (next: Driver) => {
    const token = tokenRef.current;
    const from = (next === "you" ? botSlot : youSlot).current?.getBoundingClientRect();
    const to = (next === "you" ? youSlot : botSlot).current?.getBoundingClientRect();
    // The token snaps to its new slot on render; the animation replays the trip from the old one.
    flushSync(() => setDriver(next));
    if (!token || !from || !to || prefersReducedMotion()) return;
    const path = arcPoints({ x: from.left - to.left, y: from.top - to.top }, { x: 0, y: 0 }, 26, 10);
    await settled(token.animate(path.map((point) => ({ transform: `translate(${point.x}px, ${point.y}px)` })), { duration: 520, easing: "cubic-bezier(.4,0,.2,1)" }));
  };

  const trackYou = (event: PointerEvent<HTMLDivElement>) => {
    if (driver !== "you") return;
    const rect = event.currentTarget.getBoundingClientRect();
    setYou({ x: event.clientX - rect.left, y: event.clientY - rect.top });
  };

  const on = power === "on";
  return (
    <div className="desktop-demo" ref={rootRef}>
      <div className="stage dotted fd-stage">
        <div className={`fd-machine is-${power} is-${driver}`}>
          <p className="fd-capsule" role="status">
            <span className="fd-capsule-dot" aria-hidden="true" />
            {power === "off" ? "共享电脑已关机" : power === "booting" ? "正在开机…" : driver === "bot" ? `${BOTS.ops.name} 在操作` : "你在控制"}
          </p>
          <div className="fd-screen">
          <div className="fd-display" ref={screenRef} onPointerMove={trackYou} onPointerLeave={() => setYou(null)}>
            {on && (
              <>
                <div className="fd-icons">
                  {ICONS.map((item, index) => (
                    <span key={item.id} data-target={item.id} className={`fd-icon${tapped === item.id ? " is-tapped" : ""}`} style={{ animationDelay: `${120 + index * 90}ms` }}>
                      <i><TofiIcon name={item.icon} size={20} /></i>{item.label}
                    </span>
                  ))}
                </div>
                {windowOpen && (
                  <div className="fd-window">
                    <div className="fd-window-bar"><i /><i /><i /><span>docs.tofi.dev</span><b className="fd-window-close" data-target="close" aria-hidden="true">×</b></div>
                    <div className="fd-window-body">
                      <div className="fd-search" data-target="search">{typing ? <span className="fd-typed">device flow<i /></span> : <span className="fd-placeholder">搜索文档</span>}</div>
                      <span /><span /><span />
                    </div>
                  </div>
                )}
                {ripples.map((ripple) => <span key={ripple.key} className="fd-ripple" style={{ left: ripple.x, top: ripple.y }} onAnimationEnd={() => setRipples((current) => current.filter((item) => item.key !== ripple.key))} />)}
                <div className={`fd-pointer${driver === "bot" ? "" : " is-away"}`} ref={pointerRef} aria-hidden="true">
                  <svg viewBox="0 0 24 24"><path d="M4 3l15 7.5-6.5 1.8L9.8 19z" /></svg>
                  <span>{BOTS.ops.name}</span>
                </div>
                {driver === "you" && you && <span className="fd-you" style={{ transform: `translate(${you.x}px, ${you.y}px)` }} aria-hidden="true" />}
              </>
            )}
          </div>
          {power === "off" && <p className="fd-off">按下 <TofiIcon name="play" size={12} /> 开机</p>}
          </div>
          <div className="fd-controls">
            <button type="button" className="fd-power" aria-label={on ? "关机" : "开机"} onClick={() => void (on ? shutdown() : boot())} disabled={power === "booting"}>
              <TofiIcon name={on ? "stop" : "play"} size={16} />
            </button>
            <span className="fd-hands" aria-hidden="true">
              <span className="fd-hand" ref={botSlot}><CatStage config={BOTS.ops.cat} /></span>
              <span className="fd-track" />
              <span className="fd-hand is-you" ref={youSlot}>你</span>
              <span className={`fd-token is-${driver}`} ref={tokenRef}><TofiIcon name="mouse" size={14} /></span>
            </span>
            <button type="button" className="fd-take" disabled={!on} onClick={() => void handOver(driver === "bot" ? "you" : "bot")}>
              {driver === "bot" ? "接管控制" : "交还控制"}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
