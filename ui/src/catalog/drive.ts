// Real DOM events for reaching a state inside a catalog frame: the product's own handlers do the work.
export type Drive = ReturnType<typeof driver>;

/** How long a step waits for something that appears by itself (a lazy chunk the dev server has to transform first). The step goes on
 * the moment it is there, so this only bounds a hang; a slow or busy machine must never be what fails it. */
const PATIENCE_MS = 60_000;

export function driver(root: HTMLElement) {
  const wait = (ms: number) => new Promise<void>(resolve => window.setTimeout(resolve, ms));
  const frames = (count = 2) => new Promise<void>(resolve => { const step = (left: number) => left ? requestAnimationFrame(() => step(left - 1)) : resolve(); step(count); });
  async function find(selector: string, timeout = PATIENCE_MS): Promise<HTMLElement> {
    const until = performance.now() + Math.max(timeout, PATIENCE_MS); // a caller's smaller number is a guess about speed
    for (;;) {
      // A rendered element inside this state, or outside every state (an overlay portals out of it); never one in a
      // neighbouring inline state, and never one that is not laid out (display:none, a folded sender avatar).
      const shown = (element: HTMLElement) => element.getClientRects().length > 0;
      const found = [...root.querySelectorAll<HTMLElement>(selector)].find(shown) ?? [...document.querySelectorAll<HTMLElement>(selector)].find(element => shown(element) && !element.closest(".catalog-state"));
      if (found) return found;
      if (performance.now() > until) throw new Error(`catalog: nothing matches ${selector}`);
      await wait(30);
    }
  }
  /** Waits until nothing matches (a loading state has finished). */
  async function gone(selector: string, timeout = PATIENCE_MS) {
    const until = performance.now() + Math.max(timeout, PATIENCE_MS); // a caller's smaller number is a guess about speed
    while (document.querySelector(selector)) {
      if (performance.now() > until) throw new Error(`catalog: ${selector} is still there`);
      await wait(50);
    }
  }
  const target = async (selector: string | HTMLElement) => typeof selector === "string" ? find(selector) : selector;
  return {
    root, wait, frames, find, gone,
    async click(selector: string | HTMLElement) { const element = await target(selector); element.click(); await frames(); return element; },
    /** A mouse moving onto the element: pointer and mouse events, the way the delegated hover handlers listen for them. */
    async hover(selector: string | HTMLElement) {
      const element = await target(selector);
      const rect = element.getBoundingClientRect();
      const init = { bubbles: true, cancelable: true, composed: true, clientX: rect.left + rect.width / 2, clientY: rect.top + rect.height / 2, pointerType: "mouse", isPrimary: true };
      for (const type of ["pointerover", "pointerenter", "pointermove"]) element.dispatchEvent(new PointerEvent(type, init));
      for (const type of ["mouseover", "mouseenter", "mousemove"]) element.dispatchEvent(new MouseEvent(type, init));
      await frames();
      return element;
    },
    async contextMenu(selector: string | HTMLElement) {
      const element = await target(selector);
      const rect = element.getBoundingClientRect();
      element.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: rect.left + 24, clientY: rect.top + rect.height / 2, button: 2 }));
      await frames();
      return element;
    },
    /** Keyboard focus: a navigation key first, so focusModality and :focus-visible both read "keyboard". */
    async focus(selector: string | HTMLElement) {
      const element = await target(selector);
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true }));
      // No scroll: a scroll closes popovers such as the identity card, and the frame is already where it should be.
      element.focus({ preventScroll: true });
      await frames();
      return element;
    },
    async key(selector: string | HTMLElement, key: string, init: KeyboardEventInit = {}) {
      const element = await target(selector);
      element.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...init }));
      await frames();
      return element;
    },
    /** Types into an input or textarea the way React sees typing: the native value setter, then an input event. */
    async type(selector: string | HTMLElement, text: string) {
      const element = await target(selector) as HTMLInputElement | HTMLTextAreaElement;
      element.focus();
      const setter = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(element), "value")!.set!;
      setter.call(element, text);
      element.setSelectionRange?.(text.length, text.length);
      element.dispatchEvent(new Event("input", { bubbles: true }));
      await frames();
      return element;
    },
    async scrollTo(selector: string | HTMLElement, top: number) {
      const element = await target(selector);
      element.scrollTop = top;
      element.dispatchEvent(new Event("scroll"));
      await frames(3);
      return element;
    },
    emit(type: string, data: unknown) { (window as unknown as { __catalogEmit: (type: string, data: unknown) => void }).__catalogEmit(type, data); },
  };
}

/** Opens Settings from the gear beside the account and waits until it has loaded: while a page is still loading
 * (aria-busy) the workspace ignores Escape, so a keyboard check right away would test the loading state instead. */
export async function openSettings(step: Drive) {
  await step.click(".sidebar-account-settings");
  await step.find(".settings-shell .settings-tabs, .settings-shell");
  await step.gone('.detail-pane [aria-busy="true"]');
  await step.wait(300);
}
