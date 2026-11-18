import { useTranslation } from "../../i18n";
import { Icon } from "../Icon/Icon";
import { cx } from "../cx";
import "../file.css";
import "./AttachmentGrid.css";

export type AttachmentItem = { src: string; alt?: string; width?: number; height?: number; kind?: "image" | "video"; /** "0:42" */ duration?: string };
export type AttachmentGridProps = {
  /** URLs or items. Give width and height for a single picture so its frame is right before it loads. */
  items: (string | AttachmentItem)[];
  onOpen?: (index: number) => void;
  openLabel?: string;
  moreLabel?: string;
  className?: string;
};

/** Pictures and clips in a message, laid out by count: 1 keeps its shape, 2 to 4 share a frame, 5 or more show two over three with +N. */
export function AttachmentGrid({ items, onOpen, openLabel, moreLabel, className }: AttachmentGridProps) {
  const { t } = useTranslation();
  const list: AttachmentItem[] = items.map(item => typeof item === "string" ? { src: item } : item);
  const count = list.length;
  const show = Math.min(count, 5);
  const extra = count - show;
  const layout = count >= 5 ? "n5" : `n${count}`;
  let solo: { aspectRatio: string; width: number } | undefined;
  const first = list[0];
  if (count === 1 && first.width && first.height) {
    const ratio = Math.min(2, Math.max(0.75, first.width / first.height));
    solo = { aspectRatio: String(ratio), width: ratio >= 1 ? 420 : Math.round(360 * ratio) };
  }
  return <div className={cx("tb-attach", `tb-attach--${layout}`, className)}>
    {list.slice(0, show).map((item, i) => {
      const more = i === show - 1 && extra > 0;
      const video = item.kind === "video";
      return <button key={i} type="button" className={cx("tb-attach-cell", video && "is-video")} style={solo}
        aria-label={more ? `${moreLabel ?? t("ds.attach.openAll")} (+${extra})` : `${openLabel ?? t("ds.attach.open")} ${i + 1}${item.alt ? `: ${item.alt}` : ""}`}
        onClick={onOpen ? () => onOpen(i) : undefined}>
        <img src={item.src} alt="" />
        {video && !more ? <span className="tb-fv-play tb-fv-play--center" aria-hidden="true"><Icon name="play" size={18} variant="filled" /></span> : null}
        {video && item.duration && !more ? <span className="tb-fv-chip">{item.duration}</span> : null}
        {more ? <span className="tb-attach-more" aria-hidden="true">+{extra}</span> : null}
      </button>;
    })}
  </div>;
}
