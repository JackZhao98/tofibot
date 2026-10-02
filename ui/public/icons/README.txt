Tofi Rounded Icons v2.1.0

144 named original icons / 12 categories / 288 SVG files.
24 × 24 viewBox; 1.8 px outline; round caps and joins; currentColor.

svg/name.svg                 Outline
svg/name-filled.svg          Filled / emphasized
tofi-sprite.svg              Both variants, ids tofi-name / tofi-name-filled
tofi-contact-sheet.svg       All 144 outline + filled pairs in a named grid
previews/                    Six named paired grids (SVG; PNG when rendered)
manifest.json                Labels, categories, tags, nodes and filledNodes
react/                       Typed React component, catalog, motion runtime and guide
motion.js                    Standalone browser motion controller (Web Animations API)

Filled silhouettes have transparent cutouts, never white overpainting.
Open symbols (arrows, plus, links, etc.) use a heavier line in the filled variant.
Inline SVG inherits CSS color; <img> SVG does not inherit its parent's color.
Sprite: <svg width="24" height="24"><use href="/icons/tofi-sprite.svg#tofi-bot-filled"/></svg>
React: <TofiIcon name="bot" variant="filled" size={24} animated />
Every icon has a one-shot semantic motion; hover, keyboard focus and click trigger it.
Respects prefers-reduced-motion; static SVG assets remain static.
Keep aria-label on icon-only buttons.

All SVG paths are original Tofi artwork, not exported Apple or Material assets.
Organic A/C studies are separate optional theme explorations.
