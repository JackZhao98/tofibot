import { readdir, readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import i18next from "i18next";

const root = dirname(dirname(fileURLToPath(import.meta.url)));

/**
 * A real i18next instance with one language's shipped catalogs (English as the
 * fallback), for harnesses that compile a single component with `tsc` and swap
 * its imports. Replace `import … from "./i18n"` with the returned `i18n` and
 * `useTranslation`. Prefer `openUiModules` when the module can load through Vite.
 */
export async function fixedTranslation(language = "zh-CN") {
  const resources = {};
  for (const lng of new Set([language, "en"])) {
    resources[lng] = {};
    for (const file of await readdir(join(root, "src/locales", lng))) {
      if (file.endsWith(".json")) resources[lng][file.slice(0, -5)] = JSON.parse(await readFile(join(root, "src/locales", lng, file), "utf8"));
    }
  }
  const i18n = i18next.createInstance();
  await i18n.init({ lng: language, fallbackLng: "en", resources, defaultNS: "common", interpolation: { escapeValue: false }, returnEmptyString: false });
  return { i18n, useTranslation: ns => ({ t: i18n.getFixedT(language, ns), i18n }) };
}
