import * as Collapsible from "@radix-ui/react-collapsible";
import type { ComponentProps } from "react";
import { cx } from "../cx";
import "./Collapsible.css";

/** The content of a Radix Collapsible with the kit's open and close motion. It holds one child element. */
export function CollapsibleContent({ className, ...rest }: ComponentProps<typeof Collapsible.Content>) {
  return <Collapsible.Content {...rest} className={cx("tb-collapsible", className)} />;
}
