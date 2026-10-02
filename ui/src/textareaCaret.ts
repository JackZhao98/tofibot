/** Locate a textarea character using a hidden mirror with the same wrapping rules. */
export function textareaCaretRect(textarea: HTMLTextAreaElement, position: number): DOMRect {
  const style = getComputedStyle(textarea);
  const mirror = document.createElement("div");
  const copied = ["boxSizing", "width", "padding", "border", "font", "fontFamily", "fontSize", "fontWeight", "fontStyle", "lineHeight", "letterSpacing", "textIndent", "textTransform", "wordSpacing", "tabSize", "overflowWrap", "wordBreak"] as const;
  for (const property of copied) mirror.style[property] = style[property];
  Object.assign(mirror.style, { position: "fixed", left: `${textarea.getBoundingClientRect().left}px`, top: `${textarea.getBoundingClientRect().top - textarea.scrollTop}px`, visibility: "hidden", pointerEvents: "none", whiteSpace: "pre-wrap", overflow: "visible" });
  mirror.textContent = textarea.value.slice(0, position);
  const mark = document.createElement("span");
  mark.textContent = "\u200b";
  mirror.append(mark);
  document.body.append(mirror);
  const rect = mark.getBoundingClientRect();
  mirror.remove();
  return rect;
}
