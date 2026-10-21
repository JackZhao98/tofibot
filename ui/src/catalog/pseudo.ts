// Forced interaction states for the catalog. A page cannot make the browser hover or press an element, so every rule
// that uses :hover, :active or :focus-visible gets a twin in which that pseudo-class is a class instead
// (.catalog-hover, .catalog-active, .catalog-focus). Putting the class on an element shows exactly what the product's
// own CSS draws for that state: nothing is restyled by hand. The originals stay, so the frame still reacts to the mouse.
const CLASS: Record<string, string> = { hover: "catalog-hover", active: "catalog-active", "focus-visible": "catalog-focus" };
const PSEUDO = /:(hover|active|focus-visible)(?![-\w])/g;
const NEGATED = /:not\([^)]*:(hover|active|focus-visible)/;
const done = new WeakSet<CSSStyleSheet>();

/** Split a selector list at its top-level commas (not the ones inside :is(), :where(), :not() or :has()). */
function selectors(list: string): string[] {
  const parts: string[] = [];
  let depth = 0, from = 0;
  for (let index = 0; index < list.length; index += 1) {
    const char = list[index];
    if (char === "(") depth += 1;
    else if (char === ")") depth -= 1;
    else if (char === "," && depth === 0) { parts.push(list.slice(from, index)); from = index + 1; }
  }
  parts.push(list.slice(from));
  return parts;
}

function twin(selectorText: string): string | null {
  // :not(:hover) and friends would turn into "every element without the class": leave those alone.
  const parts = selectors(selectorText).filter(part => part.search(PSEUDO) >= 0 && !NEGATED.test(part));
  if (!parts.length) return null;
  return parts.map(part => part.replace(PSEUDO, (_, name: string) => `.${CLASS[name]}`)).join(",");
}

function rewrite(rules: CSSRuleList, insert: (text: string, index: number) => void) {
  for (let index = rules.length - 1; index >= 0; index -= 1) {
    const rule = rules[index];
    if (rule instanceof CSSStyleRule) {
      const selector = twin(rule.selectorText);
      if (selector) { try { insert(`${selector}{${rule.style.cssText}}`, index + 1); } catch { /* a selector this engine cannot parse */ } }
      if (rule.cssRules?.length) rewrite(rule.cssRules, (text, at) => rule.insertRule(text, at));
    } else if (rule instanceof CSSGroupingRule) {
      rewrite(rule.cssRules, (text, at) => rule.insertRule(text, at));
    }
  }
}

function sweep() {
  for (const sheet of [...document.styleSheets]) {
    if (done.has(sheet)) continue;
    done.add(sheet);
    try { rewrite(sheet.cssRules, (text, at) => sheet.insertRule(text, at)); } catch { /* cross-origin sheet (fonts) */ }
  }
}

/** Twin every rule now and whenever a lazily loaded chunk adds its stylesheet. */
export function installForcedStates() {
  sweep();
  new MutationObserver(sweep).observe(document.head, { childList: true, subtree: true, characterData: true });
}

export type ForcedState = "hover" | "active" | "focus";
/** Put a forced state on every element the selector matches inside `root` (the root itself included). */
export function force(root: HTMLElement, selector: string, state: ForcedState) {
  const targets = [...(root.matches(selector) ? [root] : []), ...root.querySelectorAll<HTMLElement>(selector)];
  for (const target of targets) target.classList.add(`catalog-${state}`);
  // focus-ring.css hides every ring until a navigation key is pressed; a forced focus state is a keyboard focus.
  if (state === "focus") document.documentElement.dataset.input = "keyboard";
  return targets.length;
}
