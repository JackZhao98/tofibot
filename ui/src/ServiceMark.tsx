import { TofiIcon, type TofiIconName } from "./icons";

/** Letter monograms for the named catalog services (not brand art): a letter on the service's own colour. */
const monograms: { match: RegExp; letter: string; color: string }[] = [
  { match: /GitHub/, letter: "G", color: "#24292f" },
  { match: /Notion/, letter: "N", color: "#191919" },
  { match: /Linear/, letter: "L", color: "#5e6ad2" },
  { match: /Robinhood/, letter: "R", color: "#1e8f4f" },
  { match: /Context7/, letter: "C", color: "#0f766e" },
  { match: /Microsoft/, letter: "M", color: "#0b6bcb" },
];

/** The one mark every catalog service wears: onboarding, the Connections list and the add form. */
export function ServiceMark({ name }: { name: string }) {
  const mono = monograms.find(item => item.match.test(name));
  if (mono) return <span className="service-mark service-mark-letter" data-mark={mono.letter} style={{ background: mono.color }} aria-hidden="true">{mono.letter}</span>;
  const icon: TofiIconName = /Calendar/.test(name) ? "calendar" : /Drive/.test(name) ? "folder" : /Gmail/.test(name) ? "inbox" : /Docs|Sheets|Slides/.test(name) ? "file-text" : "mcp";
  return <span className={`service-mark${/Google|Gmail/.test(name) ? " service-mark-google" : ""}`} aria-hidden="true"><TofiIcon name={icon} size={22} /></span>;
}
