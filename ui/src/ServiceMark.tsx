import { TofiIcon } from "./icons";
import { resolveBrandIcon } from "./brandIcons";

/**
 * The one mark every directory service wears (onboarding, the Connections list, the directory, the connect dialog):
 * the vendor's real brand image from the registry, unmodified, on a neutral tile. Only a custom or unknown service
 * gets the generic glyph (data-mark="generic"). The image is decorative; the service name is always beside it.
 */
export function ServiceMark({ name, id }: { name: string; id?: string }) {
  const brand = resolveBrandIcon(name, id);
  if (!brand) return <span className="service-mark service-mark-generic" data-mark="generic" aria-hidden="true"><TofiIcon name="mcp" size={22} /></span>;
  return <span className="service-mark service-mark-brand" data-mark={brand.key} data-tile={brand.tile ?? "light"} aria-hidden="true"><img src={brand.src} alt="" draggable={false} decoding="async" /></span>;
}
