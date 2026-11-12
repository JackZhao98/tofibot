import { validSourceKey } from './botBrowserSource';

/** Moves the existing portal host, so changing windows never remounts its stream. */
export function externalPipHost(host: HTMLElement, key: string, onClose: () => void) {
  if (!window.tofiDesktop?.pip || !validSourceKey(key)) throw new Error('browser_client_unsupported');
  const child = window.open('about:blank', `tofi-pip-v1:${key}`);
  if (!child) throw new Error('browser_client_unsupported');
  const parent = host.parentNode;
  const sibling = host.nextSibling;
  let disposed = false;
  const sync = () => {
    if (disposed || child.closed) return;
    const root = child.document.documentElement;
    for (const attribute of Array.from(root.attributes)) root.removeAttribute(attribute.name);
    for (const attribute of Array.from(document.documentElement.attributes)) root.setAttribute(attribute.name, attribute.value);
    root.dataset.externalPip = '';
    child.document.head.replaceChildren(...Array.from(document.head.querySelectorAll('style, link[rel="stylesheet"]')).map(node => node.cloneNode(true)));
    child.document.body.className = document.body.className;
  };
  sync();
  child.document.body.appendChild(host);
  const observer = new MutationObserver(sync);
  observer.observe(document.head, {childList: true, subtree: true, characterData: true});
  observer.observe(document.documentElement, {attributes: true});
  const closed = () => { if (!disposed) onClose(); };
  child.addEventListener('pagehide', closed);
  return {
    child,
    dispose() {
      disposed = true;
      observer.disconnect();
      child.removeEventListener('pagehide', closed);
      if (parent) parent.insertBefore(host, sibling?.parentNode === parent ? sibling : null);
      else host.remove();
      child.close();
    },
  };
}
