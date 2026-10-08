import { useRef, useState, type MouseEvent } from "react";
import { flushSync } from "react-dom";
import { catalog, paletteOptions, type AvatarConfig } from "./lib/tofi-avatar/index.js";
import { BotAvatar, type AvatarMotion } from "./BotAvatar";
import { randomBotAvatarConfig } from "./avatarStore";
import { isDesktop } from "./desktop";
import { SPRINGS, springEasing } from "./motion-lab/lib/spring";
import { useTranslation } from "./i18n";
import "./bot-avatar-picker-motion.css";

export function BotAvatarPicker({ botId, config, onChange }: { botId: string; config: AvatarConfig; onChange: (config: AvatarConfig) => void }) {
  const { t } = useTranslation("bots");
  const [motion, setMotion] = useState<AvatarMotion>("awake");
  const stageRef = useRef<HTMLDivElement>(null);
  const busy = useRef(false);
  async function changeShape(next: AvatarConfig) {
    if (busy.current || config.shape === next.shape) return;
    const avatar = stageRef.current?.querySelector<HTMLElement>(".avatar");
    if (isDesktop || matchMedia("(prefers-reduced-motion: reduce)").matches || !avatar) { onChange(next); return; }
    busy.current = true;
    const out = avatar.animate({ transform:["none","translateY(-22px) scale(.25) rotate(-14deg)"], opacity:[1,0] }, { duration:220,easing:"cubic-bezier(.5,0,.75,0)",fill:"forwards" });
    try { await out.finished; } catch { /* Continue with the new shape. */ }
    flushSync(() => onChange(next));
    await new Promise<void>(resolve => requestAnimationFrame(() => resolve()));
    const pop = springEasing(SPRINGS.toy);
    const incoming = avatar.animate({ transform:["translateY(14px) scale(.25)","none"], opacity:[0,1] }, { duration:pop.durationMs,easing:pop.easing });
    try { await incoming.finished; } catch { /* Unmount can interrupt the transition. */ }
    out.cancel(); incoming.cancel(); busy.current = false;
  }
  async function changePalette(next: AvatarConfig, event: MouseEvent<HTMLButtonElement>) {
    if (busy.current || config.palette === next.palette) return;
    const stage = stageRef.current;
    const avatar = stage?.querySelector<HTMLElement>(".avatar");
    if (isDesktop || matchMedia("(prefers-reduced-motion: reduce)").matches || !stage || !avatar) { onChange(next); return; }
    busy.current = true;
    const box = avatar.getBoundingClientRect();
    const stageBox = stage.getBoundingClientRect();
    const swatch = event.currentTarget.getBoundingClientRect();
    const x = swatch.left + swatch.width / 2 - box.left;
    const y = swatch.top + swatch.height / 2 - box.top;
    const old = document.createElement("span");
    old.className = "web-avatar-paint";
    Object.assign(old.style, { left:`${box.left-stageBox.left}px`, top:`${box.top-stageBox.top}px`, width:`${box.width}px`, height:`${box.height}px` });
    old.style.setProperty("--x",`${x}px`);
    old.style.setProperty("--y",`${y}px`);
    old.append(avatar.cloneNode(true));
    stage.append(old);
    flushSync(() => onChange(next));
    await new Promise<void>(resolve => requestAnimationFrame(() => resolve()));
    const near = Math.hypot(x-Math.max(0,Math.min(x,box.width)),y-Math.max(0,Math.min(y,box.height)));
    const reach = Math.hypot(Math.max(x,box.width-x),Math.max(y,box.height-y))+12;
    try { await old.animate([{ "--paint-r":`${near}px` }, { "--paint-r":`${reach}px` }] as Keyframe[], { duration:720,easing:"cubic-bezier(.45,.05,.3,1)",fill:"forwards" }).finished; } catch { /* Unmount can interrupt the transition. */ }
    old.remove(); busy.current = false;
  }
  const palettes = paletteOptions(config.pattern);
  const pattern = catalog.patterns.find((item) => item.id === config.pattern);
  return <section className="bot-avatar-picker bot-avatar-v2" aria-label={t("avatar.aria")}>
    <div className="bot-avatar-stage" ref={stageRef}><BotAvatar id={botId} config={config} motion={motion} /></div>
    <div className="bot-avatar-state" role="group" aria-label={t("avatar.state_aria")}>{(["sleeping", "awake", "working"] as const).map(value => <button key={value} type="button" aria-pressed={motion === value} onClick={() => setMotion(value)}>{t(`avatar.state.${value}`)}</button>)}</div>
    <div className="bot-avatar-sizes" aria-label={t("avatar.sizes_aria")}>{([24, 36, 48, 64] as const).map(size => <div key={size}><span style={{width:size,height:size}}><BotAvatar id={botId} config={config} motion="sleeping" /></span><small>{size}</small></div>)}</div>
    <h3>{t("avatar.palette")} <small>{t("avatar.palette_count", { count: palettes.length })}</small></h3><div className="bot-avatar-palettes" role="group" aria-label={t("avatar.palettes_aria")}>{palettes.map(option => <button type="button" key={option.id} aria-label={option.name} aria-pressed={config.palette === option.id} title={option.name} onClick={event => void changePalette({...config,palette:option.id},event)}><span style={{background:option.body,borderColor:option.mark}}/><small>{option.name}</small></button>)}</div>
    <div className="bot-avatar-choice-heading"><h3>{t("avatar.shape")}</h3><button type="button" className="text-button" onClick={() => onChange(randomBotAvatarConfig(botId))}>{t("avatar.random")}</button></div>
    <div className="bot-avatar-shapes" role="group" aria-label={t("avatar.shapes_aria")}>{catalog.shapes.map(shape => <button type="button" key={shape.id} aria-pressed={config.shape === shape.id} onClick={() => void changeShape({...config,shape:shape.id})}><BotAvatar id={botId} config={{...config,shape:shape.id}} motion="sleeping"/><span>{shape.name}</span></button>)}</div>
    <h3>{t("avatar.pattern")}</h3><div className="bot-avatar-patterns" role="group" aria-label={t("avatar.patterns_aria")}>{catalog.patterns.map(item => <button type="button" key={item.id} aria-pressed={config.pattern === item.id} onClick={() => onChange({...config,pattern:item.id,palette:paletteOptions(item.id)[0].id})}>{item.name}</button>)}</div>
    {pattern?.note && <p className="field-note">{palettes.length === 1 ? `${pattern.note} · ${t("avatar.fixed_palette")}` : pattern.note}</p>}
  </section>;
}
