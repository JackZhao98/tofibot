import type { ModelOption, ModelProviderID } from "./types";

export const providerOrder: ModelProviderID[] = ["codex", "openai", "anthropic"];
export const providerLabels: Record<ModelProviderID, string> = { codex: "Codex", openai: "OpenAI", anthropic: "Claude" };

/** Mirrors the server routing rule: codex- → Codex, claude* → Anthropic, anything else → OpenAI. */
export function providerForModel(id: string): ModelProviderID {
  const value = id.trim().toLowerCase();
  if (value.startsWith("codex-")) return "codex";
  if (value.startsWith("claude")) return "anthropic";
  return "openai";
}

export function modelProvider(model: Pick<ModelOption, "id" | "provider">): ModelProviderID {
  return model.provider && providerOrder.includes(model.provider) ? model.provider : providerForModel(model.id);
}

/** Codex names carry a "Codex · " prefix; older servers return bare names, so add it once. */
export function modelDisplayName(model: Pick<ModelOption, "id" | "name" | "provider">): string {
  const name = model.name?.trim() || model.id;
  if (modelProvider(model) !== "codex" || /^codex\b/i.test(name)) return name;
  return `Codex · ${name}`;
}

export type ModelGroup = { provider: ModelProviderID; label: string; models: (ModelOption & { label: string })[] };

/** Groups in Codex, OpenAI, Claude order; keeps the server's order inside each group and drops duplicate ids. */
export function groupModels(models: readonly ModelOption[]): ModelGroup[] {
  const seen = new Set<string>();
  const groups = new Map<ModelProviderID, ModelGroup>();
  for (const model of models) {
    if (!model.id || seen.has(model.id)) continue;
    seen.add(model.id);
    const provider = modelProvider(model);
    const group = groups.get(provider) ?? { provider, label: providerLabels[provider], models: [] };
    group.models.push({ ...model, label: modelDisplayName(model) });
    groups.set(provider, group);
  }
  return providerOrder.flatMap((provider) => groups.get(provider) ?? []);
}

/** Efforts the picker offers for a model; empty means the effort control is hidden. */
export function modelEfforts(model: Pick<ModelOption, "reasoning_efforts"> | undefined): string[] {
  return model?.reasoning_efforts?.filter(Boolean) ?? [];
}

/** Keeps the chosen effort when the next model supports it, else falls back to its default. */
export function effortForModel(next: Pick<ModelOption, "reasoning_efforts" | "default_reasoning"> | undefined, effort: string): string {
  const efforts = modelEfforts(next);
  if (effort && efforts.includes(effort)) return effort;
  return next?.default_reasoning && efforts.includes(next.default_reasoning) ? next.default_reasoning : "";
}
