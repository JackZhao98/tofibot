/** Compact presentation never derives labels from factual bodies or prompts. */
export function conciseMetadata(value: string | undefined, fallback: string, limit: number) {
  const clean = value?.trim().replace(/\s+/gu, " ") || fallback;
  const characters = Array.from(clean);
  return characters.length > limit ? characters.slice(0, limit - 1).join("") + "…" : clean;
}

export function memoryDisplay(item?: { title?: string; description?: string }) {
  return {
    title: conciseMetadata(item?.title, "记忆", 120),
    description: conciseMetadata(item?.description, "已保存的记忆；展开查看完整内容。", 280),
  };
}

export function scheduleDisplay(item?: { title?: string; description?: string }) {
  return {
    title: conciseMetadata(item?.title, "定时任务", 120),
    description: conciseMetadata(item?.description, "按计划执行的任务；在管理中查看完整指令。", 280),
  };
}
