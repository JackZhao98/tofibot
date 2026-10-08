import { i18n } from "./i18n";

/** Compact presentation never derives labels from factual bodies or prompts. */
export function conciseMetadata(value: string | undefined, fallback: string, limit: number) {
  const clean = value?.trim().replace(/\s+/gu, " ") || fallback;
  const characters = Array.from(clean);
  return characters.length > limit ? characters.slice(0, limit - 1).join("") + "…" : clean;
}

export function memoryDisplay(item?: { title?: string; description?: string }) {
  return {
    title: conciseMetadata(item?.title, i18n.t("work:memory.title"), 120),
    description: conciseMetadata(item?.description, i18n.t("work:memory.description"), 280),
  };
}

export function scheduleDisplay(item?: { title?: string; description?: string }) {
  return {
    title: conciseMetadata(item?.title, i18n.t("schedules:display.title"), 120),
    description: conciseMetadata(item?.description, i18n.t("schedules:display.description"), 280),
  };
}
