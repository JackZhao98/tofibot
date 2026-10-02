# Tofi Rounded Icons · v2.1

144 named original icons in 12 categories, each with outline and filled variants: 288 SVG files. All drawings use a 24 × 24 viewBox, round caps and joins, and `currentColor`. The default outline width is 1.8 px. Filled shapes have actual transparent cutouts. Open action symbols such as arrows use heavier strokes for their filled/emphasized variant.

The approved rounded system direction is applied throughout. Bot has two short side antennas and vertical pill eyes; the filled memory icon retains all four internal creases. Organic A/C studies remain separate optional theme explorations in `design/icon-studies/organic`.

## React

```tsx
import { TofiIcon } from './icons';

<button aria-label="创建 Bot">
  <TofiIcon name="bot-add" size={20} animated />
</button>
<TofiIcon name="memory" variant="filled" title="长期记忆" />
<TofiIcon name="chat" size={24} color="#537766" />
```

`name` is typed. `variant` defaults to `outline`; existing names remain compatible. Decorative icons are hidden from assistive technology. Use a button label for controls or `title` for a meaningful standalone icon. Other SVG props pass through. Recommended sizes: 16, 20, 24 and 32 px. Intricate compound symbols are clearest at 20–24 px.


## Motion

Every named icon has an explicit motion assignment, shared by outline and filled variants. There are 31 short motion profiles (440–1000 ms): directional nudges, spring opening, sending, bell rocking, breathing, connection, confirmation and more. Bot antennas and outline eyes, typing dots, grid tiles and slider knobs also have individual part animations.

Add `animated` to opt in. The component listens to the closest button/link, or the SVG itself, for hover, keyboard focus and click. It plays once, returns to its resting shape, and cancels an earlier animation before replaying. Disabled controls do not animate. The runtime follows live `prefers-reduced-motion` changes and cleans up on unmount. It has no dependencies, recurring timers or idle animation loop.

```tsx
<button aria-label="发送">
  <TofiIcon name="send" animated />
</button>
<TofiIcon name="bot" variant="filled" animated />
```

The gallery has a motion toggle and a replay button in each detail view, including touch input. React code copies include `animated` when motion is enabled. Individual SVGs and the sprite remain static assets; motion is provided by the optional runtime, so icons remain useful in image tags and design tools.

For plain HTML, load `catalog.js` and `motion.js`, inline an SVG from the collection, and bind it:

```js
const controller = TofiMotion.create(TOFI_ICONS);
const cleanup = controller.attach(document.querySelector('svg[data-tofi-icon="bot"]'), 'bot');
// controller.play(svg, 'bot');  // replay
// cleanup();                  // detach one icon
// controller.destroy();       // detach everything
```

`motion.mjs` is the shared runtime for React and the generated standalone browser script. `ui/scripts/icon-motion.mjs` owns motion profiles and all 144 assignments. Per-part wrappers preserve existing SVG transforms and filled cutouts. `previews/tofi-motion.gif` is a sampled animation preview; regenerate it with `node ui/scripts/render-icon-motion.mjs` after editing motion profiles.

## Assets

- `ui/public/icons/svg/{name}.svg`: outline.
- `ui/public/icons/svg/{name}-filled.svg`: filled.
- `tofi-sprite.svg`: IDs `tofi-{name}` and `tofi-{name}-filled`.
- `manifest.json`: names, Chinese labels, categories, search tags, `nodes`, `filledNodes`, motion assignments and profiles.
- `tofi-contact-sheet.svg` / `.png`: all 144 named pairs on a grid.
- `previews/tofi-rounded-01` through `06`: six smaller SVG/PNG grids, 24 names each.
- `tofi-icons.zip`: all SVGs, rendered PNGs, manifest, sprite and React component.
- `tofi-icons.html`: a portable, self-contained gallery with embedded downloads; works without a server or internet connection. The initial named grid is prerendered even with JavaScript disabled.
- `/icons/`: hosted gallery with search, categories, outline/filled selection, size, color, stroke, light/dark, SVG/React copy and downloads.

Inline SVG and the React component inherit CSS `color`; an SVG in `<img>` does not inherit the parent color. External sprite references need a same-origin server.

```html
<svg width="24" height="24" aria-hidden="true">
  <use href="/icons/tofi-sprite.svg#tofi-bot-filled" />
</svg>
```

## Categories

Navigation, Actions, Conversations, Bots, Workspace, Files, Scheduling, Memory, Devices, Extensions, Status, Preferences. Each category has 12 named icons, with Chinese labels in the manifest and galleries.

## Authoring

`ui/scripts/rounded-icon-shapes.mjs` is the geometry source. `icon-metadata.json` owns stable names and labels. Generated files should be regenerated together:

```sh
node ui/scripts/render-icon-motion.mjs
node ui/scripts/generate-icons.mjs --png
node ui/scripts/export-icons-html.mjs
```

The optional `--png` rendering uses `rsvg-convert` (librsvg). Without it the generator exports SVG, JSON and ZIP and includes any existing PNGs; rerender PNGs after changing geometry. The scripts otherwise use only Node built-ins. No test files were added or run for this collection.

All paths are original Tofi artwork, not exports of Apple SF Symbols or Material assets.
