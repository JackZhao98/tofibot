// Shared, framework-independent Web Animations controller. No timers or animation loops.
export function createTofiMotion(catalog) {
  const playing = new Map();
  const bindings = new Set();
  let enabled = true;
  const reduced = typeof matchMedia === 'function' ? matchMedia('(prefers-reduced-motion: reduce)') : null;
  function cancel(svg) {
    const animations = playing.get(svg);
    if (animations) animations.forEach(animation => animation.cancel());
    playing.delete(svg);
    svg.querySelectorAll("[data-tofi-accent]").forEach(node => node.remove());
  }
  function cancelAll() { [...playing.keys()].forEach(cancel); }
  function play(svg, name = svg?.dataset.tofiIcon) {
    if (!svg) return;
    cancel(svg);
    if (!enabled || reduced?.matches || !svg.isConnected || typeof svg.animate !== 'function') return;
    const definition = catalog.icons[name]?.motion;
    const preset = catalog.motions[definition?.preset];
    let body = svg.querySelector('[data-tofi-body]');
    if (preset && !body) {
      // Also accepts the flat standalone SVGs shipped with the collection.
      const primitives = [...svg.children].filter(node => /^(path|circle|rect|ellipse|line|polyline|polygon|g)$/.test(node.localName));
      if (primitives.length) {
        body = svg.ownerDocument.createElementNS('http://www.w3.org/2000/svg', 'g');
        body.setAttribute('data-tofi-body', '');
        primitives.forEach((node, index) => {
          const wrapper = svg.ownerDocument.createElementNS('http://www.w3.org/2000/svg', 'g');
          wrapper.setAttribute('data-tofi-part', String(index));
          wrapper.append(node);
          body.append(wrapper);
        });
        svg.append(body);
      }
    }
    if (!preset || !body) return;
    const animations = [];
    const animate = (element, spec) => {
      element.style.transformBox = 'view-box';
      element.style.transformOrigin = spec.origin || '12px 12px';
      const animation = element.animate(spec.frames, {
        duration: spec.duration, delay: spec.delay || 0,
        easing: 'cubic-bezier(.4,0,.2,1)', fill: 'none', iterations: 1,
      });
      animations.push(animation);
      const release = () => {
        if (playing.get(svg) === animations && animations.every(item => item.playState === 'finished' || item.playState === 'idle')) playing.delete(svg);
      };
      animation.onfinish = release;
      animation.oncancel = release;
    };
    animate(body, preset);
    for (const spec of definition.parts || []) {
      if (spec.variant && spec.variant !== svg.dataset.tofiVariant) continue;
      for (const index of spec.indices) {
        const element = svg.querySelector(`[data-tofi-part="${index}"]`);
        if (element) animate(element, spec);
      }
    }
    for (const spec of definition.accents || []) {
      const group = svg.ownerDocument.createElementNS('http://www.w3.org/2000/svg', 'g');
      group.setAttribute('data-tofi-accent', '');
      group.style.opacity = '0';
      for (const [tag, attributes] of spec.nodes) {
        const node = svg.ownerDocument.createElementNS('http://www.w3.org/2000/svg', tag);
        for (const [key, value] of Object.entries(attributes)) node.setAttribute(key, String(value));
        group.append(node);
      }
      svg.append(group);
      animate(group, spec);
      animations.at(-1).finished.then(() => group.remove(), () => group.remove());
    }
    playing.set(svg, animations);
  }
  function attach(svg, name, host = svg.closest('button, a, [role="button"]') || svg) {
    const previousOverflow = svg.style.overflow;
    svg.style.overflow = 'visible';
    const usable = () => !host.matches(':disabled,[aria-disabled="true"]');
    const enter = event => { if (event.pointerType !== 'touch' && usable()) play(svg, name); };
    const focus = () => { if (host.matches(':focus-visible') && usable()) play(svg, name); };
    const click = () => { if (usable()) play(svg, name); };
    host.addEventListener('pointerenter', enter);
    host.addEventListener('focus', focus);
    host.addEventListener('click', click);
    const cleanup = () => {
      host.removeEventListener('pointerenter', enter);
      host.removeEventListener('focus', focus);
      host.removeEventListener('click', click);
      cancel(svg);
      svg.style.overflow = previousOverflow;
      bindings.delete(cleanup);
    };
    bindings.add(cleanup);
    return cleanup;
  }
  const onPreference = () => { if (reduced?.matches) cancelAll(); };
  reduced?.addEventListener('change', onPreference);
  return {
    play, attach, cancel, cancelAll,
    setEnabled(value) { enabled = Boolean(value); if (!enabled) cancelAll(); },
    destroy() { [...bindings].forEach(cleanup => cleanup()); cancelAll(); reduced?.removeEventListener('change', onPreference); },
  };
}
