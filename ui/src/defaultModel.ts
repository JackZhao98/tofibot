import type { ModelOption } from "./types";

/** The Claude model a workspace starts on. Mirrors `anthropicBackgroundModel` on the server (Haiku 5.5, in the provider registry). */
export const DEFAULT_CLAUDE_MODEL = "claude-haiku-5-5";

const isClaude = (model: Pick<ModelOption, "id" | "provider">) => model.provider ? model.provider === "anthropic" : model.id.trim().toLowerCase().startsWith("claude");

/**
 * The model to preselect when none is chosen. With a Claude model in the list it is Haiku 5.5 (a dated id of it counts),
 * else the newest Haiku, else Claude's first entry; without one it is undefined and the picker stays as it is.
 */
export function preferredDefaultModel<T extends Pick<ModelOption, "id" | "provider">>(models: readonly T[]): T | undefined {
  const claude = models.filter(isClaude);
  const match = claude.find(model => model.id === DEFAULT_CLAUDE_MODEL || model.id.startsWith(`${DEFAULT_CLAUDE_MODEL}-`));
  if (match) return match;
  const haiku = claude.filter(model => /haiku/i.test(model.id)).sort((a, b) => b.id.localeCompare(a.id, "en", { numeric: true }));
  return haiku[0] ?? claude[0];
}
