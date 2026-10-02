# Avatar assets

Bot avatars use [Gaze by DiceBear](https://www.dicebear.com/styles/gaze/), licensed under [CC0 1.0](https://creativecommons.org/publicdomain/zero/1.0/).

The application bundles `@dicebear/core` and only the Gaze definition from `@dicebear/styles`. Avatars are generated locally from stable Bot IDs; they do not call the public DiceBear API. The SVG's built-in animation is enabled at the slow speed and respects `prefers-reduced-motion`.

Dependencies are pinned in `package-lock.json`; their license files remain in the published packages.
