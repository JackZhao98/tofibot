import { cx } from "../cx";
import "./BrandMark.css";

export type BrandMarkProps = {
  /** The official vendor file, from the product's brand registry (`brandIcons.ts`). Never a redraw or a recolour. */
  src: string;
  /** The accessible name when the mark stands alone; omit when the vendor name is printed beside it. */
  name?: string;
  size?: number;
  /** `dark` only when the brand colour has no contrast on the light tile. */
  tile?: "dark";
  className?: string;
};

/** An outside service's official mark on a neutral light tile. Never for Tofi itself (use Brand). */
export function BrandMark({ src, name, size = 34, tile, className }: BrandMarkProps) {
  const ink = Math.round(size * 0.58);
  return <span className={cx("tb-brandmark", tile === "dark" && "tb-brandmark--dark", className)} style={{ width: size, height: size }} {...(name ? { role: "img", "aria-label": name } : { "aria-hidden": true })}>
    <img src={src} alt="" width={ink} height={ink} />
  </span>;
}
