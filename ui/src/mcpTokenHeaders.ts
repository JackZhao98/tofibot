import type { IntegrationPreset } from "./integrationCatalog";

/** Uses the existing private settings submission; never persists a UI draft. */
export function mcpTokenHeaders(headersJSON: string, token: string, preset?: Pick<IntegrationPreset, "tokenHeader" | "tokenPrefix">): Record<string, string> {
  const headers: unknown = JSON.parse(headersJSON);
  if (!headers || typeof headers !== "object" || Array.isArray(headers) || Object.values(headers).some(value => typeof value !== "string")) {
    throw new Error("请求头需要是名称与文本值的 JSON 对象");
  }
  const result = { ...headers } as Record<string, string>;
  if (token.trim()) {
    const name = preset?.tokenHeader ?? "Authorization";
    const prefix = preset?.tokenPrefix ?? (name.toLowerCase() === "authorization" ? "Bearer " : "");
    result[name] = `${prefix}${token.trim()}`;
  }
  return result;
}
