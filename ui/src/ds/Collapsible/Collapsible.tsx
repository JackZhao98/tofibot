import * as Collapsible from "@radix-ui/react-collapsible";
import type { ComponentProps } from "react";
import { cx } from "../cx";
import "./Collapsible.css";

/** The content of a Radix Collapsible with the kit's open and close motion. It holds one child element. `collapsible-content` stays as a hook
 *  for the sheets that place a drawer and for the tests; it carries no rule. */
export function CollapsibleContent({ className, ...rest }: ComponentProps<typeof Collapsible.Content>) {
  return <Collapsible.Content {...rest} className={cx("tb-collapsible", "collapsible-content", className)} />;
}
