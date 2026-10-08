// Safe defaults are shared by the UI and its finite synthetic contract checks.
export const ENVIRONMENT_CATEGORY = "vault_environment";
export const portabilityLabels: Record<string, string> = { bot_config: "Bot 配置与当前浏览器头像", conversations: "会话", chats: "聊天正文", memories: "记忆", schedules: "定时任务", attachments: "附件文件", attachment_bindings: "附件关联", missing_attachments: "未包含的附件", settings: "时区与模型设置", vault_environment: "保险库环境凭据（敏感，恢复后不启用）" };
export const exportDefaultCategories = ["bot_config", "chats", "memories", "schedules", "attachments"];
export function importDefaultCategories(included: string[]): string[] { return included.filter(c => c !== "settings" && c !== ENVIRONMENT_CATEGORY); }
export function checkPortableInput(source: string, encrypted: boolean): void {
  let parsed: { format?: string; version?: number; included?: unknown; vault_environment?: unknown };
  try { parsed = JSON.parse(source); } catch { throw new Error("无法读取 JSON 数据包。"); }
  if (!parsed || !["tofi.bundle", "tofi.bot"].includes(parsed.format ?? "") || !(parsed.version === 1 || (parsed.format === "tofi.bundle" && [2, 3].includes(parsed.version ?? 0))) || !Array.isArray(parsed.included)) throw new Error("文件格式或版本不受支持。");
  if (!encrypted && (parsed.version === 3 || parsed.included.includes(ENVIRONMENT_CATEGORY) || (Array.isArray(parsed.vault_environment) && parsed.vault_environment.length > 0))) throw new Error("环境凭据只能从加密数据包导入。请使用原始加密文件。");
}
