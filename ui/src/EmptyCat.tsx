import type { ReactNode } from "react";
import "./ds/EmptyState/EmptyState.css";

/*
 * Empty and first-use states (V3 PR 7.2, owner Q3): no live cats outside the header. The art is the sleeping calico, still. The
 * look is the kit's (`tb-empty`, ds/EmptyState); this file keeps the product's call shape so the callers do not change:
 * `look` and `pose` are accepted and ignored. A caller that needs a mark of its own passes the kit EmptyState an icon.
 */

export type CatLook = Record<string, never>;
export type CatPose = "asleep" | "awake" | "curious";
/** Kept so call sites read the same; every state wears the one calico art. */
export const CATS = { mochi: {}, miso: {}, nori: {}, yuzu: {}, azuki: {}, taro: {}, kiwi: {} } as const satisfies Record<string, CatLook>;

type ArtProps = { look?: CatLook; pose?: CatPose; size?: number; className?: string; name?: string };

/** The still calico, decorative. The state's text carries the meaning. */
export function EmptyCat({ className, name }: ArtProps) {
  return <span className={`tb-empty-art${className ? ` ${className}` : ""}`} data-empty-cat={name ?? ""} aria-hidden="true"><img src="/brand/tofi-logo-calico.svg" alt="" width="88" height="88" decoding="async" /></span>;
}

type EmptyStateProps = ArtProps & {
  name: string;
  title?: ReactNode;
  children?: ReactNode;
  role?: "status" | "alert";
};

/** The shared shape of an empty state: the art, a heading, supporting text and actions. */
export function EmptyState({ name, title, children, className, role }: EmptyStateProps) {
  return (
    <div className={`tb-empty${className ? ` ${className}` : ""}`} role={role} data-empty-state={name}>
      <EmptyCat name={name} />
      {title && <h3 className="tb-empty-title">{title}</h3>}
      {children}
    </div>
  );
}
