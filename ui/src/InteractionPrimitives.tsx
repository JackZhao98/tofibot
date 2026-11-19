// Controls on Radix for lazy chunks (Settings, onboarding, panels). The entry must not import this file (D31, test:dialogs).
import { useState, type ReactNode } from "react";
import * as Collapsible from "@radix-ui/react-collapsible";
import { CollapsibleContent } from "./ds/Collapsible/Collapsible";
import { TofiIcon } from "./icons";
import { useTranslation } from "./i18n";
export function Disclosure({ title, children, defaultOpen = false }: { title: ReactNode; children: ReactNode; defaultOpen?: boolean }) {
  const [open, setOpen] = useState(defaultOpen);
  return <Collapsible.Root className="disclosure" open={open} onOpenChange={setOpen}>
    <Collapsible.Trigger className="disclosure-toggle"><span>{title}</span><TofiIcon className="disclosure-chevron" name="chevron-down" size={16} /></Collapsible.Trigger>
    <CollapsibleContent forceMount inert={!open || undefined} aria-hidden={!open} className="disclosure-region"><div className="disclosure-inner">{children}</div></CollapsibleContent>
  </Collapsible.Root>;
}
