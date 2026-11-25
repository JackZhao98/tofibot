import type { ComponentProps, ReactNode } from "react";
import { resolveBrandIcon } from "./brandIcons";
import { BrandMark } from "./ds/BrandMark/BrandMark";
import { Card } from "./ds/Card/Card";
import { cx } from "./ds/cx";
import { Icon } from "./ds/Icon/Icon";
import "./ds/shared.css";

/** A hairline panel inside a step: the sign-in wait, the key form, the server form. */
export function Panel({ className, children, ...rest }: ComponentProps<typeof Card>) {
  return <Card {...rest} variant="quiet" className={cx("onb-panel", className)}>{children}</Card>;
}

/** The line that says the sheet is waiting on something outside it: the kit's breathing dot, still under reduced motion. */
export function Waiting({ role, children }: { role?: "status"; children: ReactNode }) {
  return <p className="onb-waiting" role={role}><span className="tb-breath" aria-hidden="true" />{children}</p>;
}

/** A directory service's official mark on the kit tile. Only a service the registry does not know gets the generic glyph. */
export function Mark({ name, size = 36 }: { name: string; size?: number }) {
  const brand = resolveBrandIcon(name);
  if (!brand) return <span className="onb-mark" data-mark="generic"><span className="tb-brandmark" style={{ width: size, height: size }} aria-hidden="true"><Icon name="mcp" size={Math.round(size * 0.56)} /></span></span>;
  return <span className="onb-mark" data-mark={brand.key}><BrandMark src={brand.src} size={size} tile={brand.tile === "dark" ? "dark" : undefined} fill={0.62} /></span>;
}
