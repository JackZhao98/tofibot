import "./live-line.css";

/**
 * The loading indicator of a working run's status line: a 14px ink ring with a short lagoon stroke that circles it.
 * It sits where the Bot's cat used to, is decorative (the line's text is the status) and is mounted only while the
 * run works, so finishing removes it. Motion: transform only (docs/design/components.md, "Process line").
 */
export function LiveMark() {
  return <span className="live-mark" aria-hidden="true" />;
}
