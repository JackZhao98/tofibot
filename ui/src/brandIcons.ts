/**
 * The one brand registry: which real vendor mark each directory service wears.
 * Assets are vendored under public/brand/services (audited copies and provenance live in
 * docs/integrations/icons/manifest.json); nothing is hotlinked. Marks are shown unmodified on a neutral tile
 * (light in both themes, so black marks such as Notion and GitHub keep contrast; `tile: "dark"` only where the
 * vendor's own color has no contrast on white).
 */
export type BrandIcon = {
  /** Registry key; also the data-brand attribute. */
  key: string;
  /** Catalog ids (integrationCatalog + heldIntegrations) that wear this mark. */
  ids: string[];
  /** Display-name patterns (the callers pass names, not ids). More specific entries come first. */
  match: RegExp;
  /** Public URL of the vendored asset. */
  src: string;
  /** Neutral tile the mark sits on; light unless the brand color needs a dark one. */
  tile?: "dark";
  /** Audit label for the catalog. */
  label: string;
};

const dir = "/brand/services/";

export const brandIcons: BrandIcon[] = [
  { key: "gmail", ids: ["google-gmail"], match: /\bGmail\b/i, src: dir + "google-gmail.png", label: "Gmail" },
  { key: "google-calendar", ids: ["google-calendar"], match: /\bCalendar\b/i, src: dir + "google-calendar.png", label: "Google Calendar" },
  { key: "google-drive", ids: ["google-drive"], match: /\bDrive\b/i, src: dir + "google-drive.png", label: "Google Drive" },
  { key: "google-docs", ids: ["google-docs"], match: /\bDocs\b/i, src: dir + "google-docs.png", label: "Google Docs" },
  { key: "google-sheets", ids: ["google-sheets"], match: /\bSheets\b/i, src: dir + "google-sheets.png", label: "Google Sheets" },
  { key: "google-slides", ids: ["google-slides"], match: /\bSlides\b/i, src: dir + "google-slides.png", label: "Google Slides" },
  { key: "google", ids: [], match: /\bGoogle\b/i, src: dir + "google-g.png", label: "Google account" },
  { key: "github", ids: ["github", "github-readonly"], match: /\bGitHub\b/i, src: dir + "github-monochrome.svg", label: "GitHub" },
  { key: "notion", ids: ["notion", "notion-token"], match: /\bNotion\b/i, src: dir + "notion.svg", label: "Notion" },
  { key: "linear", ids: ["linear", "linear-readonly"], match: /\bLinear\b/i, src: dir + "linear.svg", label: "Linear" },
  { key: "robinhood", ids: ["robinhood"], match: /\bRobinhood\b/i, src: dir + "robinhood.svg", tile: "dark", label: "Robinhood" },
  { key: "context7", ids: ["context7"], match: /\bContext7\b/i, src: dir + "context7.svg", label: "Context7" },
  { key: "microsoft-learn", ids: ["microsoft-learn"], match: /\bMicrosoft\b/i, src: dir + "microsoft-learn.svg", label: "Microsoft Learn" },
  { key: "discord", ids: ["discord"], match: /\bDiscord\b/i, src: dir + "discord-color.svg", label: "Discord" },
  { key: "slack", ids: ["slack"], match: /\bSlack\b/i, src: dir + "slack.png", label: "Slack" },
  // Web search and fetch providers (Settings > Web search & fetch); not directory services, so no catalog ids.
  { key: "tavily", ids: [], match: /\bTavily\b/i, src: dir + "tavily.svg", label: "Tavily" },
  { key: "brave", ids: [], match: /\bBrave\b/i, src: dir + "brave.svg", label: "Brave Search" },
  { key: "searxng", ids: [], match: /\bSearXNG\b/i, src: dir + "searxng.svg", label: "SearXNG" },
  { key: "firecrawl", ids: [], match: /\bFirecrawl\b/i, src: dir + "firecrawl.png", label: "Firecrawl" },
  { key: "brightdata", ids: [], match: /\bBright Data\b/i, src: dir + "brightdata.png", label: "Bright Data" },
  { key: "yahoo-finance", ids: ["yahoo-finance"], match: /\bYahoo\b/i, src: dir + "yahoo-finance.svg", label: "Yahoo Finance" },
];

/** Resolve a catalog id first, then a display name. Unknown (custom) services return undefined. */
export function resolveBrandIcon(name: string, id?: string): BrandIcon | undefined {
  if (id) {
    const byId = brandIcons.find(item => item.ids.includes(id));
    if (byId) return byId;
  }
  return brandIcons.find(item => item.match.test(name));
}
