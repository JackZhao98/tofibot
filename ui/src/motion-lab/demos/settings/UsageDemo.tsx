import { useEffect, useState, type CSSProperties } from "react";
import { TofiIcon } from "../../../icons";
import { useSeenOnce } from "../../lib/hooks";
import "./settings.css";

const STATS = [
  { label: "本月 token", text: "1,284,530" },
  { label: "运行", text: "342", unit: "次" },
  { label: "花费", text: "12.40", prefix: "$" },
] as const;
const DAYS = [["一", 42], ["二", 68], ["三", 55], ["四", 90], ["五", 76], ["六", 30], ["日", 24]] as const;

/** Digits roll up from zero on a reel, right to left, like an odometer settling. */
function Reel({ text, rolled }: { text: string; rolled: boolean }) {
  const chars = text.split("");
  return (
    <span className="usage-number" aria-label={text}>
      {chars.map((char, index) => (/\d/.test(char)
        ? (
          <span key={index} className="usage-col" aria-hidden="true">
            <span className="usage-reel" style={{ "--d": rolled ? Number(char) + 10 : 0, "--delay": `${(chars.length - index) * 45}ms` } as CSSProperties}>
              {"01234567890123456789".split("").map((face, at) => <span key={at}>{face}</span>)}
            </span>
          </span>
        )
        : <span key={index} className="usage-sep" aria-hidden="true">{char}</span>))}
    </span>
  );
}

/** Usage panel: numbers roll up and bars grow, once, when you first look. */
export function UsageDemo() {
  const [rootRef, seen] = useSeenOnce<HTMLDivElement>();
  const [rolled, setRolled] = useState(false);
  const [run, setRun] = useState(0);

  useEffect(() => {
    if (!seen) return;
    setRolled(false);
    const frame = requestAnimationFrame(() => requestAnimationFrame(() => setRolled(true)));
    return () => cancelAnimationFrame(frame);
  }, [seen, run]);

  return (
    <div className={`usage-demo${rolled ? " is-rolled" : ""}`} ref={rootRef}>
      <div className="usage-stats">
        {STATS.map((stat) => (
          <div key={stat.label} className="usage-stat">
            <small>{stat.label}</small>
            <strong>{"prefix" in stat && stat.prefix}<Reel text={stat.text} rolled={rolled} />{"unit" in stat && <em>{stat.unit}</em>}</strong>
          </div>
        ))}
      </div>
      <div className="usage-bars" aria-label="最近 7 天的运行次数">
        {DAYS.map(([day, value], index) => (
          <span key={day} className="usage-bar">
            <i style={{ "--h": `${value}%`, "--delay": `${200 + index * 60}ms` } as CSSProperties} title={`周${day} ${value} 次`} />
            <small>{day}</small>
          </span>
        ))}
      </div>
      <div className="demo-actions">
        <button type="button" className="lab-btn quiet" onClick={() => setRun((value) => value + 1)}><TofiIcon name="retry" size={16} animated />再看一遍</button>
      </div>
    </div>
  );
}
