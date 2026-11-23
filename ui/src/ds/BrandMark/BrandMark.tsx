import { cx } from "../cx";
import "./BrandMark.css";

export type BrandMarkProps = {
  /** The official vendor file, from the product's brand registry (`brandIcons.ts`). Never a redraw or a recolour. */
  src: string;
  /** The accessible name when the mark stands alone; omit when the vendor name is printed beside it. */
  name?: string;
  size?: number;
  /** How much of the tile the image fills (0.58 by default). A vendor file drawn with its own clearspace, such as the OpenAI Blossom, needs 1 to read at the same size as the others. */
  fill?: number;
  /** `dark` only when the brand colour has no contrast on the light tile. */
  tile?: "dark";
  className?: string;
};

/** An outside service's official mark on a neutral light tile. Never for Tofi itself (use Brand). */
export function BrandMark({ src, name, size = 34, fill = 0.58, tile, className }: BrandMarkProps) {
  const ink = Math.round(size * fill);
  return <span className={cx("tb-brandmark", tile === "dark" && "tb-brandmark--dark", className)} style={{ width: size, height: size }} {...(name ? { role: "img", "aria-label": name } : { "aria-hidden": true })}>
    <img src={src} alt="" width={ink} height={ink} />
  </span>;
}
