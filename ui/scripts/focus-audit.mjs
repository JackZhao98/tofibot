// Shared focus-ring audit for the browser tests (settings-polish, chat-bubbles, file-preview, onboarding).
//
// Rule: a focus ring appears ONLY for keyboard navigation.
//   - after a mouse click / touch tap (or a dialog opened that way) no element draws a focus ring: no outline, no
//     ring-shaped box-shadow. Text-entry controls (input/textarea/select) keep their caret frame and are exempt.
//   - after Tab the focused element (or the wrapper / visual box it stands for) DOES draw a visible ring.

/** Runs inside the page. Describes the ring drawn for the focused element. */
export function focusRingProbe() {
  const el = document.activeElement;
  if (!el || el === document.body || el === document.documentElement) return {none: true};
  // Radix parks invisible tab sentinels at the page edges while one of its layers (Select, menu, dialog) is open or still leaving.
  if (el.hasAttribute("data-radix-focus-guard")) return {none: true};
  const name = node => node.tagName.toLowerCase() + (node.id ? "#" + node.id : "") + [...node.classList].slice(0, 2).map(c => "." + c).join("");
  const outlineOf = node => {
    const s = getComputedStyle(node);
    const clear = /rgba\(\s*\d+,\s*\d+,\s*\d+,\s*0\s*\)|transparent/.test(s.outlineColor);
    return s.outlineStyle !== "none" && parseFloat(s.outlineWidth) > 0 && !clear ? `outline ${s.outlineWidth} ${s.outlineStyle} ${s.outlineColor}` : null;
  };
  const shadowOf = node => {
    const s = getComputedStyle(node).boxShadow;
    if (!s || s === "none") return null;
    // ring-shaped: zero offsets, zero blur, positive spread ("rgb(..) 0px 0px 0px 3px"); drop shadows have offsets
    return /\)\s+0px 0px 0px [1-9]\d*(?:\.\d+)?px/.test(s) ? `box-shadow ${s.slice(0, 90)}` : null;
  };
  const field = el.tagName === "IFRAME" || el.matches("input:not([type=checkbox]):not([type=radio]):not([type=button]):not([type=submit]):not([type=file]), textarea, select, [contenteditable=true]");
  const related = [el, ...[...el.querySelectorAll(".message-content")].slice(0, 2)];
  for (let node = el.parentElement, depth = 0; node && node !== document.body && depth < 4; node = node.parentElement, depth++) if (node.matches(":focus-within")) related.push(node);
  const label = el.closest("label, .sheet-radio, .sheet-check, .check-row"); if (label) related.push(label);
  const hits = related.map(node => ({node: name(node), ring: outlineOf(node) || shadowOf(node)})).filter(item => item.ring);
  return {none: false, field, focused: name(el), html: el.outerHTML.slice(0, 160), ring: hits[0] ?? null};
}

/** Runs inside the page. Centers of up to `limit` visible interactive targets, spread across the page. */
export function pointerTargets(limit) {
  const all = [...document.querySelectorAll("button, a[href], summary, [role=tab], [role=menuitem], [role=radio], [tabindex]:not([tabindex='-1']), .message-block, label")];
  const seen = [];
  for (const el of all) {
    if (el.disabled || el.closest("[inert], [hidden]")) continue;
    const r = el.getBoundingClientRect();
    if (r.width < 6 || r.height < 6 || r.bottom < 0 || r.top > innerHeight || r.right < 0 || r.left > innerWidth) continue;
    const s = getComputedStyle(el);
    if (s.visibility === "hidden" || s.display === "none" || parseFloat(s.opacity) < 0.05 || s.pointerEvents === "none") continue;
    const x = Math.min(innerWidth - 1, Math.max(1, r.left + Math.min(r.width / 2, 40))), y = r.top + r.height / 2;
    if (y < 0 || y > innerHeight) continue;
    const top = document.elementFromPoint(x, y);
    if (!top || !(el === top || el.contains(top))) continue;
    if (el.matches("input[type=file]") || el.closest("a[download]")) continue;
    seen.push({x, y, label: el.tagName.toLowerCase() + (el.className && typeof el.className === "string" ? "." + el.className.split(/\s+/)[0] : "")});
  }
  if (seen.length <= limit) return seen;
  const stride = seen.length / limit;
  return Array.from({length: limit}, (_, i) => seen[Math.floor(i * stride)]);
}

/**
 * Pointer sweep then keyboard check. `touch` uses page.touchscreen.tap (context must have hasTouch).
 * Returns violations [{kind:"focus-ring"|"focus-ring-missing", selector, detail}].
 */
export async function auditFocus(page, {touch = false, limit = 14, scope = ""} = {}) {
  const found = [];
  const tag = text => scope ? `${scope}: ${text}` : text;
  const check = async stage => {
    const probe = await page.evaluate(focusRingProbe);
    if (probe.none || probe.field) return probe;
    if (probe.ring) found.push({kind: "focus-ring", selector: probe.focused, detail: tag(`${stage}: ${probe.ring.node} draws ${probe.ring.ring}`)});
    return probe;
  };
  const targets = await page.evaluate(pointerTargets, limit);
  for (const [index, target] of targets.entries()) {
    try {
      // blur first so every sample starts from a pointer-only history
      await page.evaluate(() => { if (document.activeElement instanceof HTMLElement) document.activeElement.blur(); });
      if (touch) await page.touchscreen.tap(target.x, target.y); else await page.mouse.click(target.x, target.y);
      await page.waitForTimeout(160);
      await check(`${touch ? "tap" : "click"} #${index} ${target.label}`);
      // a dialog this opened must also be ring-free after the pointer closes it
      await page.keyboard.press("Escape");
      await page.waitForTimeout(120);
      await check(`after Escape (${target.label})`);
    } catch (error) { found.push({kind: "scenario-error", selector: target.label, detail: tag(`focus sweep: ${error.message.split("\n")[0]}`)}); break; }
  }
  // keyboard: Tab must produce a ring on what it focuses
  try {
    await page.evaluate(() => { if (document.activeElement instanceof HTMLElement) document.activeElement.blur(); window.scrollTo(0, 0); });
    let tabbed = 0;
    for (let i = 0; i < 6; i++) {
      await page.keyboard.press("Tab");
      await page.waitForTimeout(60);
      const probe = await page.evaluate(focusRingProbe);
      if (probe.none) continue;
      tabbed++;
      if (!probe.ring && !probe.field) found.push({kind: "focus-ring-missing", selector: probe.focused, detail: tag("Tab focused an element that draws no ring" + (process.env.FOCUS_DEBUG ? " " + probe.html : ""))});
    }
    if (!tabbed) {
      const state = await page.evaluate(() => ({ active: document.activeElement?.tagName, focusable: [...document.querySelectorAll("button, a[href], input, select, textarea, summary, [tabindex]:not([tabindex='-1'])")].filter(el => !el.disabled && el.getClientRects().length).length, dialogs: document.querySelectorAll("dialog[open]").length, hasFocus: document.hasFocus() }));
      if (state.focusable) found.push({kind: "focus-ring-missing", selector: "(page)", detail: tag(`Tab never reached an element (${JSON.stringify(state)})`)});
    }
  } catch (error) { found.push({kind: "scenario-error", selector: "(tab)", detail: tag(`tab sweep: ${error.message.split("\n")[0]}`)}); }
  return found;
}
