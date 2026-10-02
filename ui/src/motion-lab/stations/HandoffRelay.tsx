import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { TofiIcon } from "../../icons";
import type { AvatarConfig } from "../../lib/tofi-avatar/index.js";
import { CatStage, type CatHandle } from "../lib/CatStage";
import { BOTS } from "../lib/crew";
import { closedSpline, cumulativeStops, type Point } from "../lib/choreo";
import { prefersReducedMotion, settled, useSeenOnce, wait } from "../lib/hooks";
import "./handoff-relay.css";

type Stop = Readonly<{ id: string; name: string; verb: string; cat?: Partial<AvatarConfig>; wide: Point; narrow: Point }>;
type NodeState = "idle" | "active" | "done";

// Positions are fractions of the stage; the loop returns to you for the report.
const STOPS: readonly Stop[] = [
  { id: "you", name: "你", verb: "发起", wide: { x: 0.12, y: 0.5 }, narrow: { x: 0.24, y: 0.16 } },
  { id: "research", name: BOTS.research.name, verb: "拆分派发", cat: BOTS.research.cat, wide: { x: 0.43, y: 0.2 }, narrow: { x: 0.78, y: 0.3 } },
  { id: "writer", name: BOTS.writer.name, verb: "执行", cat: BOTS.writer.cat, wide: { x: 0.86, y: 0.42 }, narrow: { x: 0.72, y: 0.7 } },
  { id: "ops", name: BOTS.ops.name, verb: "核查", cat: BOTS.ops.cat, wide: { x: 0.5, y: 0.8 }, narrow: { x: 0.24, y: 0.82 } },
];
const CHAIN = ["发起", "拆分派发", "执行", "核查", "回报"];
const PX_PER_MS = 0.42;
const TRAVEL_EASE = "cubic-bezier(.45,0,.2,1)";

type Geometry = { width: number; height: number; points: Point[]; path: string; stops: number[]; total: number };

function measure(width: number, height: number): Geometry {
  const narrow = width < 560;
  const points = STOPS.map((stop) => {
    const fraction = narrow ? stop.narrow : stop.wide;
    return { x: fraction.x * width, y: fraction.y * height };
  });
  const { path, segments } = closedSpline(points);
  const probe = document.createElementNS("http://www.w3.org/2000/svg", "path");
  const lengths = segments.map((segment) => { probe.setAttribute("d", segment); return probe.getTotalLength(); });
  return { width, height, points, path, stops: cumulativeStops(lengths), total: lengths.reduce((sum, length) => sum + length, 0) };
}

/** A ticket travels the team loop; each Bot wakes, works it, and passes it on. */
export function HandoffRelay() {
  const [rootRef, seen] = useSeenOnce<HTMLDivElement>();
  const stageRef = useRef<HTMLDivElement>(null);
  const ticketRef = useRef<HTMLDivElement>(null);
  const progressRef = useRef<SVGPathElement>(null);
  const cats = useRef<(CatHandle | null)[]>([]);
  const [geometry, setGeometry] = useState<Geometry | null>(null);
  const [nodes, setNodes] = useState<readonly NodeState[]>(STOPS.map(() => "idle"));
  const [chain, setChain] = useState(-1);
  const [running, setRunning] = useState(false);
  const [run, setRun] = useState(0);
  const geometryRef = useRef<Geometry | null>(null);

  useLayoutEffect(() => {
    const stage = stageRef.current;
    if (!stage) return;
    const observer = new ResizeObserver(() => {
      const next = measure(stage.clientWidth, stage.clientHeight);
      geometryRef.current = next;
      setGeometry(next);
    });
    observer.observe(stage);
    return () => observer.disconnect();
  }, []);

  const mark = (index: number, state: NodeState) => setNodes((current) => current.map((value, at) => (at === index ? state : value)));

  useEffect(() => {
    if (!seen || !geometryRef.current) return;
    const controller = new AbortController();
    const { signal } = controller;
    const ticket = ticketRef.current;
    const progress = progressRef.current;
    const play = async () => {
      setNodes(STOPS.map(() => "idle"));
      setChain(0);
      if (!ticket || !progress) return;
      if (prefersReducedMotion()) {
        setNodes(STOPS.map(() => "done"));
        setChain(CHAIN.length - 1);
        return;
      }
      setRunning(true);
      ticket.getAnimations().forEach((animation) => animation.cancel());
      progress.getAnimations().forEach((animation) => animation.cancel());
      mark(0, "active");
      if (!await wait(700, signal)) return;
      mark(0, "done");
      for (let index = 0; index < STOPS.length; index += 1) {
        const geo = geometryRef.current!;
        const from = geo.stops[index];
        const to = geo.stops[index + 1];
        const duration = Math.max(700, Math.min(1500, ((to - from) * geo.total) / PX_PER_MS));
        const options = { duration, easing: TRAVEL_EASE, fill: "forwards" as const };
        const travel = ticket.animate({ offsetDistance: [`${from * 100}%`, `${to * 100}%`] }, options);
        progress.animate({ strokeDashoffset: [String(1 - from), String(1 - to)] }, options);
        ticket.querySelector(".ticket-card")?.animate({ rotate: ["0deg", index % 2 ? "7deg" : "-7deg", "0deg"] }, { duration, easing: "ease-in-out" });
        signal.addEventListener("abort", () => travel.cancel(), { once: true });
        await settled(travel);
        if (signal.aborted) return;
        const arrived = (index + 1) % STOPS.length;
        setChain(index + 1);
        if (arrived === 0) break;
        mark(arrived, "active");
        // The full work clip is long; hand the ticket on after a beat and let the cat keep typing.
        const working = cats.current[arrived]?.play("work") ?? Promise.resolve(false);
        if (!await Promise.race([working.then(() => true), wait(1600, signal)])) return;
        mark(arrived, "done");
      }
      cats.current.forEach((cat) => { void cat?.play("happy"); });
      setRunning(false);
    };
    void play();
    return () => { controller.abort(); setRunning(false); };
    // Re-run on replay only; geometry changes are read live through the ref.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [seen, run, geometry !== null]);

  const ticketStyle = useMemo(() => (geometry ? { offsetPath: `path("${geometry.path}")` } as CSSProperties : undefined), [geometry]);
  return (
    <div className="handoff-relay" ref={rootRef}>
      <div className="stage dotted relay-stage" ref={stageRef}>
        {geometry && (
          <svg className="relay-track" viewBox={`0 0 ${geometry.width} ${geometry.height}`} aria-hidden="true">
            <path className="relay-base" d={geometry.path} />
            <path className="relay-progress" d={geometry.path} pathLength={1} ref={progressRef} />
          </svg>
        )}
        {geometry && STOPS.map((stop, index) => (
          <div
            key={stop.id}
            className={`relay-node is-${nodes[index]} ${stop.cat ? "is-bot" : "is-human"}`}
            style={{ left: geometry.points[index].x, top: geometry.points[index].y }}
          >
            <span className="relay-face">
              {stop.cat ? <CatStage config={stop.cat} ref={(handle) => { cats.current[index] = handle; }} /> : <span className="relay-you">你</span>}
            </span>
            <span className="relay-name">{stop.name}</span>
            <span className="relay-verb">
              {nodes[index] === "active" && <span className="breath-dot" aria-hidden="true" />}
              {nodes[index] === "done" && <TofiIcon name="check" size={12} variant="filled" />}
              {stop.verb}
            </span>
          </div>
        ))}
        <div className="ticket" ref={ticketRef} style={ticketStyle} hidden={!geometry} aria-hidden="true">
          <div className="ticket-card">
            <span className="ticket-label">TICKET</span>
            <span className="ticket-title">周报素材</span>
          </div>
        </div>
      </div>
      <ol className="chain" aria-label="协作链">
        {CHAIN.map((step, index) => (
          <li key={step} className={index < chain ? "is-done" : index === chain ? "is-now" : ""}><span>{step}</span></li>
        ))}
      </ol>
      <div className="stage-controls">
        <button type="button" className="lab-btn" disabled={running} onClick={() => setRun((value) => value + 1)}>
          <TofiIcon name="bot-handoff" size={16} animated />再传一次
        </button>
      </div>
    </div>
  );
}
