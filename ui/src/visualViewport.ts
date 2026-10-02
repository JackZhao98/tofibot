export type VisualViewportSample = {
  layoutHeight: number;
  visualHeight: number;
  offsetTop: number;
  scale: number;
  wasKeyboardOpen: boolean;
};

export type VisualViewportMetrics =
  | { ignored: true; keyboardOpen: boolean; height?: undefined }
  | { ignored: false; keyboardOpen: boolean; height?: number };

/**
 * Turn one visual viewport observation into the height used by the app shell.
 * A keyboard-panned viewport may have a non-zero offsetTop; its visible bottom
 * is the stable edge to which the flex column should be anchored.
 */
export function visualViewportMetrics(sample: VisualViewportSample): VisualViewportMetrics {
  if (sample.scale !== 1 || !Number.isFinite(sample.layoutHeight) || !Number.isFinite(sample.visualHeight) || !Number.isFinite(sample.offsetTop)) {
    return { ignored: true, keyboardOpen: sample.wasKeyboardOpen };
  }

  const layoutHeight = Math.max(0, sample.layoutHeight);
  const visibleBottom = Math.max(0, Math.min(layoutHeight, sample.offsetTop + sample.visualHeight));
  const bottomGap = layoutHeight - visibleBottom;
  // Opening uses the bottom gap to avoid mistaking browser chrome for a
  // keyboard. Once open, retain the state while iPadOS pans the page and the
  // visible bottom temporarily reaches the layout bottom. The larger
  // visual-height gap separates that case from a genuinely closed keyboard.
  const keyboardOpen = sample.wasKeyboardOpen
    ? bottomGap > 8 || sample.visualHeight < layoutHeight - 80
    : bottomGap > 40;
  return {
    ignored: false,
    keyboardOpen,
    height: keyboardOpen ? Math.round(visibleBottom) : undefined,
  };
}

export type ViewportEventTarget = Pick<EventTarget, "addEventListener" | "removeEventListener">;

/** Subscribe to every event that can move or resize the visual viewport. */
export function observeVisualViewport(visual: ViewportEventTarget, windowTarget: ViewportEventTarget, listener: EventListener): () => void {
  visual.addEventListener("resize", listener);
  visual.addEventListener("scroll", listener);
  windowTarget.addEventListener("resize", listener);
  return () => {
    visual.removeEventListener("resize", listener);
    visual.removeEventListener("scroll", listener);
    windowTarget.removeEventListener("resize", listener);
  };
}
