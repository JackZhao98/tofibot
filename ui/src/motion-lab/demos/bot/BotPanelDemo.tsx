import { useRef, useState, type CSSProperties, type MouseEvent } from "react";
import { flushSync } from "react-dom";
import { TofiIcon } from "../../../icons";
import { catalog, normalizeConfig, paletteOptions, type AvatarConfig, type Palette, type Shape } from "../../../lib/tofi-avatar/index.js";
import { CatStage, type CatHandle } from "../../lib/CatStage";
import { BOTS } from "../../lib/crew";
import { SPRINGS, springEasing } from "../../lib/spring";
import { prefersReducedMotion, settled } from "../../lib/hooks";
import "./bot.css";

type LabBot = Readonly<{ id: string; name: string; role: string; model: string }>;
type Paint = Readonly<{ svg: string; x: number; y: number }>;
type ViewTransitionDocument = Document & { startViewTransition?: (update: () => void) => { finished: Promise<void> } };

const LIST: readonly LabBot[] = [
  { id: "research", name: BOTS.research.name, role: "查资料、读文档，给出处", model: "codex-gpt-5.6-luna · medium" },
  { id: "writer", name: BOTS.writer.name, role: "起草、润色、改写语气", model: "codex-gpt-5.6-terra · low" },
  { id: "ops", name: BOTS.ops.name, role: "盯服务、看告警、先止血", model: "codex-gpt-5.6-luna · high" },
];
const START: Readonly<Record<string, AvatarConfig>> = {
  research: normalizeConfig(BOTS.research.cat),
  writer: normalizeConfig(BOTS.writer.cat),
  ops: normalizeConfig(BOTS.ops.cat),
};
const SWATCHES = paletteOptions("solid").slice(0, 8);
const POP = springEasing(SPRINGS.toy);
const CAT_NAME = "lab-bot-cat";

function isSpecial(pattern: AvatarConfig["pattern"]): boolean {
  return catalog.patterns.find((item) => item.id === pattern)?.mode === "special";
}

/** Sidebar cat → panel cat as one shared element; coat colour spreads from the swatch; shapes hop out and in. */
export function BotPanelDemo() {
  const [openId, setOpenId] = useState<string | null>(null);
  const [configs, setConfigs] = useState<Readonly<Record<string, AvatarConfig>>>(START);
  const [paint, setPaint] = useState<Paint | null>(null);
  const rowCats = useRef<Record<string, HTMLSpanElement | null>>({});
  const bigBox = useRef<HTMLDivElement>(null);
  const bigCat = useRef<CatHandle>(null);
  const paintRef = useRef<HTMLDivElement>(null);
  const busy = useRef(false);

  // The row avatar carries the shared name only for the frame being captured.
  const transition = (update: () => void, rowId: string | null, direction: "open" | "close") => {
    const doc = document as ViewTransitionDocument;
    const row = rowId ? rowCats.current[rowId] : null;
    if (!doc.startViewTransition || prefersReducedMotion() || !row) { update(); return; }
    if (direction === "open") row.style.viewTransitionName = CAT_NAME;
    const running = doc.startViewTransition(() => {
      flushSync(update);
      row.style.viewTransitionName = direction === "close" ? CAT_NAME : "";
    });
    void running.finished.finally(() => { row.style.viewTransitionName = ""; });
  };

  const open = (id: string) => {
    if (id === openId) return;
    transition(() => setOpenId(id), openId ? null : id, "open");
  };
  const close = () => transition(() => setOpenId(null), openId, "close");

  const recolor = async (palette: Palette, event: MouseEvent<HTMLButtonElement>) => {
    const id = openId;
    const cat = bigCat.current?.get();
    const box = bigBox.current?.getBoundingClientRect();
    if (!id || !cat || !box || busy.current || configs[id].palette === palette) return;
    const current = configs[id];
    const next = normalizeConfig({ ...current, pattern: isSpecial(current.pattern) ? "solid" : current.pattern, palette });
    if (prefersReducedMotion()) { setConfigs((all) => ({ ...all, [id]: next })); return; }
    busy.current = true;
    // Freeze the cat, keep its old coat as a still overlay, recolour underneath, then open a hole from the swatch.
    cat.setAutoplay(false);
    cat.setLife(false);
    const swatch = event.currentTarget.getBoundingClientRect();
    const x = swatch.left + swatch.width / 2 - box.left;
    const y = swatch.top + swatch.height / 2 - box.top;
    flushSync(() => { setPaint({ svg: cat.exportSVG(), x, y }); setConfigs((all) => ({ ...all, [id]: next })); });
    // The swatch sits outside the cat: start the circle where it first touches the cat so the whole sweep is visible.
    const near = Math.hypot(x - Math.max(0, Math.min(x, box.width)), y - Math.max(0, Math.min(y, box.height)));
    const reach = Math.hypot(Math.max(x, box.width - x), Math.max(y, box.height - y)) + 12;
    const layer = paintRef.current;
    if (layer) await settled(layer.animate([{ "--paint-r": `${near}px` }, { "--paint-r": `${reach}px` }] as Keyframe[], { duration: 720, easing: "cubic-bezier(.45,.05,.3,1)", fill: "forwards" }));
    setPaint(null);
    cat.setLife(true);
    cat.setAutoplay(true);
    busy.current = false;
  };

  const reshape = async (shape: Shape) => {
    const id = openId;
    const box = bigBox.current;
    if (!id || !box || busy.current || configs[id].shape === shape) return;
    const next = normalizeConfig({ ...configs[id], shape });
    if (prefersReducedMotion()) { setConfigs((all) => ({ ...all, [id]: next })); return; }
    busy.current = true;
    await settled(box.animate({ transform: ["none", "translateY(-22px) scale(.25) rotate(-14deg)"], opacity: [1, 0] }, { duration: 220, easing: "cubic-bezier(.5,0,.75,0)", fill: "forwards" }));
    flushSync(() => setConfigs((all) => ({ ...all, [id]: next })));
    await settled(box.animate({ transform: ["translateY(14px) scale(.25)", "none"], opacity: [0, 1] }, { duration: POP.durationMs, easing: POP.easing }));
    box.getAnimations().forEach((animation) => animation.cancel());
    busy.current = false;
    void bigCat.current?.play("happy");
  };

  const bot = LIST.find((item) => item.id === openId);
  return (
    <div className="bot-panel-demo">
      <div className={`stage bp-stage${bot ? " is-open" : ""}`}>
        <div className="bp-list" role="list">
          {LIST.map((item) => (
            <button key={item.id} type="button" role="listitem" className={`bp-row${item.id === openId ? " is-active" : ""}`} onClick={() => open(item.id)} aria-label={`打开 ${item.name}`}>
              <span className="bp-row-cat" ref={(element) => { rowCats.current[item.id] = element; }}>
                {item.id !== openId && <CatStage config={configs[item.id]} autoplay />}
              </span>
              <span className="bp-row-copy"><strong>{item.name}</strong><small>{item.role}</small></span>
            </button>
          ))}
        </div>
        <div className="bp-panel-slot">
          {!bot && <p className="bp-empty">点任意一只猫。</p>}
          {bot && (
            <section className="bp-panel" key={bot.id} aria-label={`${bot.name} 的面板`}>
              <button type="button" className="bp-close" aria-label="关闭面板" onClick={close}><TofiIcon name="close" size={18} /></button>
              <div className="bp-hero">
                <div className="bp-cat" ref={bigBox} style={{ viewTransitionName: CAT_NAME }}>
                  <CatStage config={configs[bot.id]} autoplay ref={bigCat} />
                  {paint && (
                    <div
                      className="bp-paint"
                      ref={paintRef}
                      style={{ "--x": `${paint.x}px`, "--y": `${paint.y}px` } as CSSProperties}
                      aria-hidden="true"
                      // A snapshot this page just generated from its own renderer; no external markup.
                      dangerouslySetInnerHTML={{ __html: paint.svg }}
                    />
                  )}
                </div>
                <h4>{bot.name}</h4>
                <p>{bot.role}</p>
                <small>{bot.model}</small>
              </div>
              <div className="bp-section">
                <h5>毛色</h5>
                <div className="bp-swatches">
                  {SWATCHES.map((swatch) => (
                    <button
                      key={swatch.id}
                      type="button"
                      className="bp-swatch"
                      aria-label={swatch.name}
                      aria-pressed={configs[bot.id].palette === swatch.id}
                      style={{ background: swatch.body, boxShadow: `inset 0 -7px 0 ${swatch.mark}` }}
                      onClick={(event) => void recolor(swatch.id, event)}
                    />
                  ))}
                </div>
              </div>
              <div className="bp-section">
                <h5>身形</h5>
                <div className="bp-shapes">
                  {catalog.shapes.map((shape) => (
                    <button key={shape.id} type="button" className="bp-shape" aria-pressed={configs[bot.id].shape === shape.id} onClick={() => void reshape(shape.id)}>{shape.name}</button>
                  ))}
                </div>
              </div>
            </section>
          )}
        </div>
      </div>
    </div>
  );
}
