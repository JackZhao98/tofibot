# Provider brand marks

Every directory service in `ui/src/integrationCatalog.ts` (available and held) shows the vendor's real mark. The files are vendored, never hotlinked: audited copies and provenance live here (`manifest.json` records name, brand, style, source, sha256, modified, license, retrieval date), and byte-identical runtime copies are served from `ui/public/brand/services/` (checked by `npm run test:brand-icons`). `ui/src/brandIcons.ts` maps each catalog id to its file; the OAuth completion page uses the same files (`internal/app/oauth_completion.go` allow-list, CSP `img-src 'self'`).

## Google

Gmail, Google Calendar, Drive, Docs, Sheets, Slides and the Google "G" account mark are Google's own published assets from `https://www.gstatic.com/images/branding/` (the 2020q4 Workspace product icons at 2x/96dp = 192x192 PNG, and `googleg/2x/googleg_standard_color_128dp.png` = 256x256 PNG for the G). Google publishes no SVG for these at gstatic (every probed SVG path returns 404), so the largest published PNG is used. They are unmodified.

Google and the Google product names and icons are trademarks of Google LLC. They are used here only to identify the Google service the user chooses to connect, unmodified (no recoloring, outline, effects or distortion), at readable size on a neutral tile, with the service name beside them. Their use does not imply endorsement by or affiliation with Google. These files are not licensed under an open license; see https://about.google/brand-resource-center/.

## Simple Icons (CC0)

Notion, Linear and Robinhood come from Simple Icons (https://github.com/simple-icons/simple-icons, commit c71c69e3a27e4436936b831c72643e6513b9f49b), whose package is CC0-1.0. CC0 covers the package, not the brands: Simple Icons' DISCLAIMER (https://github.com/simple-icons/simple-icons/blob/develop/DISCLAIMER.md) says each mark remains the property of its owner and may carry trademark or other rights. Used only to identify the named service; no endorsement is implied.

- Notion: unmodified (brand hex 000000 is the default fill).
- Linear and Robinhood: `modified: true`. The only change is a `fill` attribute on the root `<svg>` set to the brand hex from Simple Icons' `data/simple-icons.json` (Linear 5E6AD2, Robinhood CCFF00); paths are untouched. The manifest records `recolor`, the modification and the upstream sha256.
- Robinhood's brand color has no contrast on white, so it alone sits on a dark tile.

## Official kits and vendor-published assets

- GitHub (`github-monochrome.svg`): GitHub's brand kit, https://brand.github.com/GitHub_Logos.zip, unmodified black Invertocat; the white variant is retained here for audit but is not needed because the tile is light.
- Discord (`discord-color.svg`): https://discord.com/branding, unmodified Blurple symbol. Keep the Discord name beside it, keep clearspace, do not reconfigure.
- Slack (`slack.png`): the hash mark Slack publishes on slack.com (`icon_slack_hash_colored.png`). Simple Icons has no Slack entry.
- Microsoft Learn (`microsoft-learn.svg`): the Microsoft four-square symbol as served by learn.microsoft.com (`/en-us/media/logos/logo-ms-social.svg`). Simple Icons has no Microsoft entry.
- Context7 (`context7.svg`): `public/context7-icon.svg` from the upstash/context7 repository (repository MIT; the mark's trademark stays with Upstash). Simple Icons has no Context7 entry.
- Yahoo Finance (`yahoo-finance.svg`): Yahoo Finance's own favicon SVG (`s.yimg.com/cv/apiv2/finance/YF_Favicon.svg`); it includes its own white square. No other vendor-published vector was found. Simple Icons has no Yahoo Finance entry.

GitHub, Discord, Slack, Microsoft, Yahoo and Upstash own their respective trademarks. TOFI catalog curation does not imply vendor endorsement.

## Web search and fetch providers (2026-10-27)

Brave and SearXNG use the Simple Icons SVGs (CC0-1.0), unmodified. Tavily, Firecrawl and Bright Data use the site icons each vendor publishes on its own website, unmodified. All five are shown only to name the service next to its settings row. Sources and hashes are in `manifest.json`.

## Not brand marks

The TOFI library has 144 original named glyphs (outline/filled pairs, 24x24 viewBox, currentColor). They are the generic fallback for custom or unknown services only (`data-mark="generic"`), and `ui/public/oauth/provider-generic.svg` is the authored neutral pictogram for an unrecognized OAuth provider.

Soft/cute remains a proposed name for TOFI-owned presentation surrounding an unchanged mark; no stylized vendor logo is distributed.
