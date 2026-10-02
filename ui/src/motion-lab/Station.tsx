import type { ReactNode } from "react";

type StationProps = {
  id: string;
  /** Where this moment lives in the product. */
  place: string;
  title: string;
  children: ReactNode;
  lede: ReactNode;
};

export function Station({ id, place, title, lede, children }: StationProps) {
  return (
    <section className="station" id={id} aria-labelledby={`${id}-title`}>
      <div className="station-copy">
        <p className="station-place">{place}</p>
        <h3 id={`${id}-title`}>{title}</h3>
        <div className="station-lede">{lede}</div>
      </div>
      <div className="station-stage">{children}</div>
    </section>
  );
}
