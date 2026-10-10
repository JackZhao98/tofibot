# Third-party notices

Bot avatars are TOFI's own cat artwork, drawn locally from each Bot's saved appearance.

Web dependencies are pinned in `package-lock.json`; their license files remain in the published packages. Bundled dependency notices are under `public/licenses`.

## Fonts (SIL Open Font License 1.1)

The Web app self-hosts these fonts. They remain under the SIL OFL 1.1, not the `ui/` license; full texts are in `public/licenses/`. The fonts are not sold on their own. No CJK font is shipped (Chinese, Japanese and Korean text uses the system fonts).

| Font | Files in `src/assets/fonts` | License text | Source |
|---|---|---|---|
| Fredoka, Copyright 2016 The Fredoka Project Authors | `fredoka-latin.woff2`, `fredoka-latin-ext.woff2` | `public/licenses/fredoka-OFL.txt` | https://github.com/google/fonts/blob/main/ofl/fredoka/Fredoka%5Bwdth%2Cwght%5D.ttf |
| JetBrains Mono, Copyright 2020 The JetBrains Mono Project Authors | `jetbrains-mono-400.woff2`, `jetbrains-mono-600.woff2` | `public/licenses/jetbrains-mono-OFL.txt` | https://github.com/JetBrains/JetBrainsMono/releases/download/v2.304/JetBrainsMono-2.304.zip |

Fredoka: the upstream variable TTF (sha256 `2ba02e68b152868aef9ba28e24b3648c7d457fe6f25c761f2c2c53fb61a73fc8`) was pinned to `wdth=100`, `wght=500..700`, then subset to Latin and Latin Extended and converted to WOFF2 with fontTools (`varLib.instancer`, `subset`). The result is a modified file; the Font Software is not renamed because the license declares no Reserved Font Name.

JetBrains Mono: the unmodified upstream `fonts/webfonts/JetBrainsMono-Regular.woff2` and `JetBrainsMono-SemiBold.woff2` from release v2.304 (zip sha256 `6f6376c6ed2960ea8a963cd7387ec9d76e3f629125bc33d1fdcd7eb7012f7bbf`).

| Shipped file | sha256 |
|---|---|
| `fredoka-latin.woff2` | `5cd01b79d27e6a6ec7ce5aeb117d1e9492abde15399b7fb50c491739fcbffa8c` |
| `fredoka-latin-ext.woff2` | `69c0a4d60aa154c90f68892bf1fefc9ad309fa1acfcb72392101980a107c00b2` |
| `jetbrains-mono-400.woff2` | `a9cb1cd82332b23a47e3a1239d25d13c86d16c4220695e34b243effa999f45f2` |
| `jetbrains-mono-600.woff2` | `918edad542a1da608fd2ba8daebaff9ac802309103fe760eed465b8b4e47faf1` |
| `public/licenses/fredoka-OFL.txt` | `5c9e7eee5c6b25f4b05b8d53b2e470ea4962f9ced742d044a98f7d95d1375bab` |
| `public/licenses/jetbrains-mono-OFL.txt` | `30f0c136e3c88e422d0791acd97238870f9054a9729bc34cf2ff0d4ed8cac4ad` |
