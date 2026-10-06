import { prefersReducedMotion } from "./motion-lab/lib/hooks";
import { SPRINGS, springEasing } from "./motion-lab/lib/spring";
import "./mail-send-flight.css";
import { postmarkDate, postmarkSVG } from "./postmark";

async function finish(animation: Animation) { try { await animation.finished; } catch { /* element removed */ } }

/** Runs only after an exact draft revision has a confirmed send outcome. */
export async function flyMailDraft(letter: HTMLElement): Promise<void> {
  if (prefersReducedMotion()) return;
  const rect = letter.getBoundingClientRect();
  if (rect.width < 1 || rect.height < 1) return;
  const layer = document.createElement("div");
  layer.className = "workspace mail-flight-layer";
  Object.assign(layer.style, { position:"fixed", inset:"0", height:"100dvh", display:"block", overflow:"hidden", background:"transparent", pointerEvents:"none", zIndex:"120" });
  layer.setAttribute("aria-hidden", "true");
  const packet = document.createElement("div");
  packet.className = "mail-flight-packet";
  Object.assign(packet.style, { left:`${rect.left}px`, top:`${rect.top}px`, width:`${rect.width}px`, height:`${rect.height}px` });
  const third = rect.height / 3;
  const panels: HTMLElement[] = [];
  for (let i = 0; i < 3; i++) {
    const panel = document.createElement("div");
    panel.className = `mail-flight-panel mail-flight-panel-${i}`;
    Object.assign(panel.style, { top:`${third * i}px`, height:`${third}px` });
    const copy = letter.cloneNode(true) as HTMLElement;
    copy.removeAttribute("aria-label");
    Object.assign(copy.style, { position:"absolute", left:"0", top:`${-third * i}px`, width:`${rect.width}px`, height:`${rect.height}px`, margin:"0", boxShadow:"none" });
    panel.append(copy);
    packet.append(panel);
    panels.push(panel);
  }
  const envelope = document.createElement("div");
  envelope.className = "mail-flight-envelope";
  envelope.innerHTML = '<div class="mail-flight-envelope-flap"><svg viewBox="0 0 100 58" preserveAspectRatio="none"><path d="M1.5 1.5 L50 56 L98.5 1.5 Z" /></svg></div><div class="mail-flight-envelope-back"></div><div class="mail-flight-envelope-insert"></div><div class="mail-flight-envelope-pocket"><svg viewBox="0 0 100 60" preserveAspectRatio="none"><path d="M1.5 1.5 L50 34 L98.5 1.5 L98.5 58.5 L1.5 58.5 Z" /></svg></div><div class="mail-flight-envelope-stamp"></div><div class="mail-flight-envelope-postmark">' + postmarkSVG({ ring:"TOFI POST · TOFI POST ·", date:postmarkDate(new Date()), waves:true }) + '</div><span class="mail-flight-envelope-address">TOFI POST</span>';
  const stampSource = letter.querySelector(".mail-draft-stamp .avatar");
  if (stampSource) envelope.querySelector(".mail-flight-envelope-stamp")?.append(stampSource.cloneNode(true));
  const envelopeWidth = Math.min(300, Math.max(220, rect.width * .68));
  const targetX = Math.min(window.innerWidth - envelopeWidth - 24, Math.max(24, rect.left + (rect.width - envelopeWidth) / 2));
  const targetY = Math.max(90, Math.min(window.innerHeight - 220, rect.top + rect.height / 2 - 90));
  Object.assign(envelope.style, { left:`${targetX}px`, top:`${targetY}px`, width:`${envelopeWidth}px` });
  layer.append(packet, envelope);
  document.body.append(layer);
  letter.style.visibility = "hidden";
  try {
    await finish(panels[2].animate({ transform:["rotateX(0deg)","rotateX(180deg)"] }, { duration:460, easing:"cubic-bezier(.62,0,.24,1)", fill:"forwards" }));
    await finish(panels[0].animate({ transform:["rotateX(0deg)","rotateX(-180deg)"] }, { duration:460, easing:"cubic-bezier(.62,0,.24,1)", fill:"forwards" }));
    const packetCenterX = rect.left + rect.width / 2;
    const packetCenterY = rect.top + rect.height / 2;
    const envelopeCenterX = targetX + envelopeWidth / 2;
    const envelopeCenterY = targetY + envelopeWidth * .3;
    envelope.style.visibility = "visible";
    const rise = envelope.animate({ transform:["translateY(160%) rotate(-8deg)","none"], opacity:[0,1] }, { duration:650, easing:springEasing(SPRINGS.toy).easing, fill:"forwards" });
    const insert = packet.animate({ transform:["none",`translate(${envelopeCenterX-packetCenterX}px,${envelopeCenterY-packetCenterY-70}px) scale(.7) rotate(-3deg)`,`translate(${envelopeCenterX-packetCenterX}px,${envelopeCenterY-packetCenterY}px) scale(${envelopeWidth/rect.width * .82})`], opacity:[1,1,0] }, { duration:760, easing:"cubic-bezier(.3,.6,.25,1)", fill:"forwards" });
    await Promise.all([finish(rise),finish(insert)]);
    packet.style.visibility = "hidden";
    const inner = envelope.querySelector<HTMLElement>(".mail-flight-envelope-insert")!;
    inner.style.visibility = "visible";
    await finish(inner.animate({ transform:["none",`translateY(${envelopeWidth * .31}px)`] }, { duration:380, easing:"cubic-bezier(.55,0,.8,.3)", fill:"forwards" }));
    const flap = envelope.querySelector<HTMLElement>(".mail-flight-envelope-flap")!;
    flap.style.zIndex = "4";
    await finish(flap.animate({ transform:["rotateX(180deg)","rotateX(0deg)"] }, { duration:560, easing:springEasing(SPRINGS.morph).easing, fill:"forwards" }));
    flap.style.transform = "rotateX(0deg)";
    const stamp = envelope.querySelector<HTMLElement>(".mail-flight-envelope-stamp")!;
    await finish(stamp.animate({ opacity:[0,1], transform:["translate(-40px,-65px) scale(2.2) rotate(-24deg)","rotate(6deg)"] }, { duration:420, easing:springEasing(SPRINGS.toy).easing, fill:"forwards" }));
    const postmark = envelope.querySelector<HTMLElement>(".mail-flight-envelope-postmark")!;
    await finish(postmark.animate({ opacity:[0,.92], transform:["scale(1.7) rotate(-34deg)","scale(1) rotate(-10deg)"] }, { duration:240, easing:"cubic-bezier(.2,.9,.3,1)", fill:"forwards" }));
    await new Promise(resolve => window.setTimeout(resolve, 560));
    await finish(envelope.animate({ transform:["none","translate(-3%,4%) rotate(-3deg)","translate(160%,-70%) rotate(16deg) scale(.7)"], opacity:[1,1,0] }, { duration:760, easing:"cubic-bezier(.5,0,.75,.1)", fill:"forwards" }));
  } finally {
    letter.style.visibility = "";
    layer.remove();
  }
}
