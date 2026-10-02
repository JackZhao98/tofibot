import { useEffect, useRef } from "react";
import { Picker } from "emoji-mart";
import data from "@emoji-mart/data";

/** Standard Emoji Mart UI; bundled native data keeps searches local/offline. */
export default function ReactionEmojiPicker({ onSelect }: { onSelect: (emoji: string) => void }) {
  const host = useRef<HTMLDivElement>(null);
  const select = useRef(onSelect);
  select.current = onSelect;
  useEffect(() => {
    const theme = () => document.documentElement.dataset.theme === "dark" ? "dark" : "light";
    const picker = new Picker({ data, theme: theme(), locale: "en", set: "native", autoFocus: true,
      dynamicWidth: true, perLine: 8, maxFrequentRows: 1, previewPosition: "none", skinTonePosition: "search",
      onEmojiSelect: (emoji: { native: string }) => select.current(emoji.native) });
    const element = picker as unknown as HTMLElement;
    host.current?.append(element);
    const observer = new MutationObserver(() => picker.update({ theme: theme() }));
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    return () => { observer.disconnect(); element.remove(); };
  }, []);
  return <div ref={host} className="reaction-emoji-picker" />;
}
