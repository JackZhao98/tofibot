import { lazy, Suspense } from "react";
import type { AvatarConfig } from "./lib/tofi-avatar/index.js";
import { TofiIcon } from "./icons";

const GroupAvatar = lazy(() => import("./GroupAvatar"));

/** The group avatar in its own chunk (the entry bundle has no room for it); the people icon stands in until it arrives. */
export function LazyGroupAvatar(props: { ids: string[]; configs?: AvatarConfig[]; iconSize?: number }) {
  return <Suspense fallback={<TofiIcon name="group" size={props.iconSize ?? 19} />}><GroupAvatar {...props} /></Suspense>;
}
