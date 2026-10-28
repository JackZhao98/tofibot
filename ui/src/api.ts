import type { SearchResults, TaskImpact, FollowupAccount, FollowupBotSettings, FollowupOverview, FollowupVerdict, AgentUsageTotal, Attachment, Bot, ComputerHealth, Config, ContentPatch, ContextUsage, Conversation, EventEnvelope, Memory, MemoryInput, Message, ModelCatalog, ModelProviderStatus, Run, RunContextSnapshot, RunHealth, Schedule, ScheduleKind, SchedulePatchInput, StreamDraft, ToolActivity, ToolActivityDetailPage, ToolActivityRunSummary, UsageCall, UsagePeriod, WebToolsView, WorkspaceEventEnvelope, WorkItem, Weekday } from "./types";
import { i18n } from "./i18n";
import { codeText } from "./i18n/codeText";
import { invalidateModelCatalog } from "./modelCatalogStore";
import { setProxyChallenge } from "./proxyChallenge";

export class ApiError extends Error {
  constructor(public status: number, message: string, public code?: string, public data?: Record<string, unknown>) {
    super(message);
    this.name = "ApiError";
  }
}

const READ_TIMEOUT_MS = 15_000;
/** Last Codex connection state this tab saw, to notice a sign-in finished elsewhere. */
let lastCodexConnected: boolean | null = null;

export async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body && !(init.body instanceof FormData) && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  // A read that never answers must fail so polls can recover; writes keep their own pace.
  const signal = init.signal ?? (!init.method || init.method === "GET" ? AbortSignal.timeout(READ_TIMEOUT_MS) : undefined);
  const send = () => fetch(path, { ...init, headers, credentials: "same-origin", signal });
  let response = await send();
  // A proxy challenge (Cloudflare) is not a TOFI answer: renew the clearance once, retry once, else one notice.
  if (response.status === 403 || response.status === 503) {
    // If the recovery chunk cannot load, the response is handled as it came.
    const settled = await import("./proxyChallengeRecover").then((m) => m.settle(path, response, send), () => response);
    if (!settled) throw new ApiError(response.status, codeText("proxy_challenge")!, "proxy_challenge");
    response = settled;
  }
  setProxyChallenge(false);
  if (!response.ok) {
    if (response.status === 401) window.dispatchEvent(new Event("tofi:unauthorized"));
    const body = (await response.json().catch(() => null)) as { error?: { message?: string; code?: string } & Record<string, unknown> } | null;
    // A catalogued code is worded in the UI's language; the server's English message stays in `data.message`.
    const message = codeText(body?.error?.code) ?? body?.error?.message ?? i18n.t("common:apiError.request_failed", { status: response.status });
    throw new ApiError(response.status, message, body?.error?.code, body?.error);
  }
  if (response.status === 204) return undefined as T;
  return (await response.json()) as T;
}

const json = (body: unknown): RequestInit => ({ method: "POST", body: JSON.stringify(body) });

export const api = {
  search: (q: string, kind: "all" | "chats" | "messages", limit: number, signal?: AbortSignal) => request<SearchResults>(`/api/search?q=${encodeURIComponent(q)}&kind=${kind}&limit=${limit}`, { signal }),
  serverInfo: (signal?: AbortSignal) => request<{ service: string; protocol_version: number; instance_id: string }>("/api/server-info", { signal }),
  config: () => request<Config>("/api/config"),
  usage: () => request<{ contexts: ContextUsage[]; agents: AgentUsageTotal[]; periods: Record<"24h" | "7d" | "30d", UsagePeriod[]>; review_periods?: Record<"24h" | "7d" | "30d", UsagePeriod[]>; calls: UsageCall[]; price_source: string; price_as_of: string; note: string }>("/api/usage"),
  usageRuns: (days = 7, signal?: AbortSignal) => request<RunHealth>(`/api/usage/runs?days=${days}`, { signal }),
  dictationSettings: () => request<{ model: string; configured: boolean; auth_source: "api_key" | "codex" | ""; models: { id: string; name: string }[] }>("/api/dictation-settings"),
  saveDictationSettings: (model: string) => request<{ model: string }>("/api/dictation-settings", { method: "PUT", body: JSON.stringify({ model }) }),
  transcribeDictation: async (blob: Blob, signal?: AbortSignal) => {
    const body = new FormData();
    const extension = blob.type.includes("mp4") || blob.type.includes("m4a") ? "mp4" : "webm";
    body.append("file", blob, `dictation.${extension}`);
    return request<{ text: string; model: string }>("/api/dictate", { method: "POST", body, signal });
  },
  codexStatus: async () => {
    const result = await request<{ connected: boolean; expires_at?: number; pending?: boolean; needs_reconnect?: boolean }>("/api/auth/codex");
    // Sign-in can finish in another tab; a status that differs from the last one seen means the model list changed with it.
    if (lastCodexConnected !== null && lastCodexConnected !== result.connected) invalidateModelCatalog();
    lastCodexConnected = result.connected;
    return result;
  },
  codexVerify: () => request<{ connected: boolean; expires_at?: number; pending?: boolean; needs_reconnect?: boolean; check: "ok" | "rejected" | "unverified" | "not_connected" }>("/api/auth/codex/verify", { method: "POST" }),
  codexConnect: () => request<{ session_id: string; verification_url: string; user_code: string; expires_at: number; interval: number }>("/api/auth/codex/connect", { method: "POST" }),
  codexPoll: (id: string) => request<{ connected: boolean; pending: boolean; expires_at?: number }>(`/api/auth/codex/connect/${encodeURIComponent(id)}/poll`, { method: "POST" }).then((result) => {
    if (result.connected) { lastCodexConnected = true; invalidateModelCatalog(); }
    return result;
  }),
  codexDisconnect: () => request<void>("/api/auth/codex", { method: "DELETE" }).then((result) => { lastCodexConnected = false; invalidateModelCatalog(); return result; }),
  listProviders: (signal?: AbortSignal) => request<{ providers: ModelProviderStatus[] | null }>("/api/providers", { signal }).then((result) => result.providers ?? []),
  setProviderKey: (id: "openai" | "anthropic", key: string, workspaceId?: string) => request<ModelProviderStatus>(`/api/providers/${encodeURIComponent(id)}/key`, { method: "PUT", body: JSON.stringify(workspaceId ? { key, workspace_id: workspaceId } : { key }) }).then((result) => { invalidateModelCatalog(); return result; }),
  deleteProviderKey: (id: "openai" | "anthropic") => request<unknown>(`/api/providers/${encodeURIComponent(id)}/key`, { method: "DELETE" }).then((result) => { invalidateModelCatalog(); return result; }),
  webTools: (signal?: AbortSignal) => request<WebToolsView>("/api/web-tools", { signal }),
  setWebToolOrder: (chain: "search" | "fetch", order: string[]) => request<WebToolsView>(`/api/web-tools/${chain}/order`, { method: "PUT", body: JSON.stringify({ order }) }),
  setWebToolEnabled: (chain: "search" | "fetch", id: string, enabled: boolean) => request<WebToolsView>(`/api/web-tools/${chain}/${encodeURIComponent(id)}`, { method: "PUT", body: JSON.stringify({ enabled }) }),
  setWebToolLimits: (limits: { search?: number; fetch?: number }) => request<WebToolsView>("/api/web-tools/limits", { method: "PUT", body: JSON.stringify(limits) }),
  saveWebProvider: (id: string, body: { key?: string; zone?: string; base_url?: string }) => request<WebToolsView>(`/api/web-tools/providers/${encodeURIComponent(id)}`, { method: "PUT", body: JSON.stringify(body) }),
  removeWebProvider: (id: string) => request<WebToolsView>(`/api/web-tools/providers/${encodeURIComponent(id)}`, { method: "DELETE" }),
  testWebProvider: (id: string) => request<WebToolsView>(`/api/web-tools/providers/${encodeURIComponent(id)}/test`, { method: "POST" }),
  models: (signal?: AbortSignal) => request<ModelCatalog>("/api/models", { signal }),
  bots: async (includeArchived = false) => {
    const result = await request<{ bots: Bot[] | null }>(`/api/bots${includeArchived ? "?include_archived=true" : ""}`);
    return { bots: result.bots ?? [] };
  },
  createBot: (input: Pick<Bot, "name" | "instructions" | "model" | "reasoning_effort">) => request<Bot>("/api/bots", json(input)),
  createNewBot: (client_creation_id: string) => request<Bot>("/api/bots", json({ onboarding: true, client_creation_id, locale: i18n.resolvedLanguage ?? i18n.language })),
  onboardingState: () => request<unknown>("/api/onboarding"),
  putOnboarding: (update: { step?: number; completed?: boolean; skipped?: boolean }) => request<unknown>("/api/onboarding", { method: "PUT", body: JSON.stringify(update) }),
  firstBot: () => request<{ bot: Bot; created: boolean }>("/api/onboarding/first-bot", json({ locale: i18n.resolvedLanguage ?? i18n.language })),
  updateBot: (id: string, input: Partial<Pick<Bot, "name" | "instructions" | "model" | "reasoning_effort">>) => request<Bot>(`/api/bots/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify(input) }),
  conversations: async (includeArchived = false) => {
    const result = await request<{ conversations: Conversation[] | null }>(`/api/conversations${includeArchived ? "?include_archived=true" : ""}`);
    return { conversations: result.conversations ?? [] };
  },
  updateConversation: (id: string, input: { name?: string; bot_ids?: string[]; expected_bot_ids?: string[]; expected_name?: string }) =>
    request<Conversation>(`/api/conversations/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify(input) }),
  setConversationArchived: (id: string, archived: boolean) => request<Conversation>(`/api/conversations/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify({ archived }) }),
  /** Hiding the Primary Assistant needs `newPrimaryId`; without it the server answers 409 primary_reassign_required. */
  setBotArchived: (id: string, archived: boolean, newPrimaryId?: string) => request<Bot>(`/api/bots/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify(newPrimaryId ? { archived, new_primary_id: newPrimaryId } : { archived }) }),
  /** Deleting the Primary Assistant needs `newPrimaryId` while other Bots exist (409 primary_reassign_required otherwise). */
  deleteBot: (id: string, newPrimaryId?: string) => request<{ deleted: boolean; bot_id: string; conversation_id: string }>(`/api/bots/${encodeURIComponent(id)}${newPrimaryId ? `?new_primary_id=${encodeURIComponent(newPrimaryId)}` : ""}`, { method: "DELETE" }),
  primaryAssistant: () => request<{ bot_id: string }>("/api/primary-assistant"),
  setPrimaryAssistant: (botId: string) => request<{ bot_id: string }>("/api/primary-assistant", { method: "PUT", body: JSON.stringify({ bot_id: botId }) }),
  followups: (signal?: AbortSignal) => request<FollowupOverview>("/api/followups", { signal }),
  setFollowupBot: (botId: string, input: Partial<Pick<FollowupBotSettings, "mode" | "source_chat">>) => request<FollowupBotSettings>(`/api/followups/bots/${encodeURIComponent(botId)}`, { method: "PUT", body: JSON.stringify(input) }),
  setFollowupAccount: (input: Partial<FollowupAccount>) => request<FollowupAccount>("/api/followups/account", { method: "PUT", body: JSON.stringify(input) }),
  followupFeedback: (loopId: string, verdict: FollowupVerdict) => request<{ loop_id: string; verdict: FollowupVerdict }>(`/api/followups/loops/${encodeURIComponent(loopId)}/feedback`, json({ verdict })),
  deleteGroup: (id: string) => request<{ deleted: boolean; conversation_id: string }>(`/api/conversations/${encodeURIComponent(id)}`, { method: "DELETE" }),
  setSidebarLayout: (layout: { pinned: string[]; order: string[] }) => request<{ layout: { id: string; pinned: boolean; pin_order: number; sort_order: number }[] }>("/api/conversations/order", { method: "PUT", body: JSON.stringify(layout) }),
  markRead: (id: string, seq: number) => request<{read_seq: number}>(`/api/conversations/${encodeURIComponent(id)}/read`, json({seq})),
  markUnread: (id: string, seq: number) => request<{read_seq: number}>(`/api/conversations/${encodeURIComponent(id)}/read`, json({seq, unread: true})),
  createGroup: (input: { name: string; bot_ids: string[] }) => request<Conversation>("/api/groups", json(input)),
  messages: (id: string, beforeSeq?: number) => {
    const query = new URLSearchParams({ limit: "50" });
    if (beforeSeq !== undefined) query.set("before_seq", String(beforeSeq));
    return request<{ messages: Message[] | null; drafts?: StreamDraft[] | null; has_more: boolean; event_cursor?: number }>(`/api/conversations/${encodeURIComponent(id)}/messages?${query}`).then((result) => ({ ...result, messages: result.messages ?? [], drafts: result.drafts ?? [] }));
  },
  sendMessage: (id: string, input: { content: string; client_message_id: string; attachment_ids?: string[] }) => request<{ message: Message; run?: Run | null; runs?: Run[]; queued?: { bot_id: string; run_id: string; inbox_id: string }[] }>(`/api/conversations/${encodeURIComponent(id)}/messages`, json(input)),
  setMessageReaction: (conversationId: string, messageId: string, emoji: string, present: boolean) => request<{ conversation_id: string; message_id: string; reactions: NonNullable<Message["reactions"]> }>(`/api/conversations/${encodeURIComponent(conversationId)}/messages/${encodeURIComponent(messageId)}/reactions`, { method: "PUT", body: JSON.stringify({ emoji, present }) }),
  uploadAttachment: async (id: string, file: File) => { const body = new FormData(); body.append("file", file); return request<Attachment>(`/api/conversations/${encodeURIComponent(id)}/attachments`, { method: "POST", body }); },
  // The newest page of runs plus every active one; `before` (an ISO time) asks for the page of runs created earlier, to go with an older page of messages.
  runs: (id: string, before?: string) => request<{ runs: Run[] | null; has_more?: boolean }>(`/api/conversations/${encodeURIComponent(id)}/runs${before ? `?before=${encodeURIComponent(before)}` : ""}`).then((result) => ({ runs: result.runs ?? [], has_more: Boolean(result.has_more) })),
  cancelRun: (id: string) => request<Run>(`/api/runs/${encodeURIComponent(id)}/cancel`, { method: "POST" }),
  retryRun: (id: string) => request<Run>(`/api/runs/${encodeURIComponent(id)}/retry`, { method: "POST" }),
  memories: (id: string) => request<{ memories: Memory[] | null }>(`/api/conversations/${encodeURIComponent(id)}/memories`).then((result) => ({ memories: result.memories ?? [] })),
  createMemory: (id: string, input: MemoryInput) => request<Memory>(`/api/conversations/${encodeURIComponent(id)}/memories`, json(input)),
  updateMemory: (id: string, input: ContentPatch) => request<Memory>(`/api/memories/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify(input) }),
  deleteMemory: (id: string) => request<void>(`/api/memories/${encodeURIComponent(id)}`, { method: "DELETE" }),
  schedules: (id: string) => request<{ schedules: Schedule[] }>(`/api/conversations/${encodeURIComponent(id)}/schedules`),
  scheduleAgenda: (id: string, scope: "bots" | "conversations", history = false) => request<{ schedules: Schedule[] }>(`/api/${scope}/${encodeURIComponent(id)}/schedules?${scope === "bots" ? `history=${history}` : `view=${history ? "history" : "upcoming"}`}`),
  workItems: (id: string, scope: "bots" | "conversations", history = false) => request<{ work_items: WorkItem[] }>(`/api/${scope}/${encodeURIComponent(id)}/work-items?history=${history}`),
  botTaskImpact: (botId: string, groupId?: string, signal?: AbortSignal) => request<TaskImpact>(`/api/bots/${encodeURIComponent(botId)}/task-impact${groupId ? `?group_id=${encodeURIComponent(groupId)}` : ""}`, { signal }),
  taskCards: (id: string) => request<{ tasks: WorkItem[] }>(`/api/conversations/${encodeURIComponent(id)}/task-cards`),
  createWorkItem: (conversation: string, input: Pick<WorkItem, "kind" | "title" | "bot_id"> & Partial<Pick<WorkItem, "description" | "parent_goal_id">>) => request<WorkItem>(`/api/conversations/${encodeURIComponent(conversation)}/work-items`, json(input)),
  updateWorkItem: (id: string, input: Partial<Pick<WorkItem, "title" | "description" | "status" | "bot_id">>) => request<WorkItem>(`/api/work-items/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify(input) }),
  executeWorkItem: (id: string, request_id: string) => request<{ work_item: WorkItem; run: Run; duplicate: boolean }>(`/api/work-items/${encodeURIComponent(id)}/execute`, json({ request_id })),
  createSchedule: (id: string, input: { bot_id?: string; title: string; description: string; content: string; kind: ScheduleKind; run_at?: string; interval_seconds?: number; daily_time?: string; weekdays?: Weekday[]; timezone?: string }) => request<Schedule>(`/api/conversations/${encodeURIComponent(id)}/schedules`, json(input)),
  updateSchedule: (id: string, input: SchedulePatchInput) => request<Schedule>(`/api/schedules/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify(input) }),
  pauseSchedule: (id: string) => request<Schedule>(`/api/schedules/${encodeURIComponent(id)}/pause`, json({})),
  resumeSchedule: (id: string) => request<Schedule>(`/api/schedules/${encodeURIComponent(id)}/resume`, json({})),
  deleteSchedule: (id: string) => request<void>(`/api/schedules/${encodeURIComponent(id)}`, { method: "DELETE" }),
  toolActivities: (id: string) => request<{ activities: ToolActivity[] }>(`/api/conversations/${encodeURIComponent(id)}/tools`),
  toolActivitySummaries: (id: string, runIds: string[]) => request<{ summaries: ToolActivityRunSummary[] }>(`/api/conversations/${encodeURIComponent(id)}/tools?${new URLSearchParams({ run_ids: runIds.join(",") })}`),
  toolActivityResult: (id: string, runId: string, callId: string) => request<{ activity: ToolActivity }>(`/api/conversations/${encodeURIComponent(id)}/tools?${new URLSearchParams({ run_id: runId, call_id: callId })}`),
  toolActivityDetails: (id: string, runId: string, offset = 0) => request<ToolActivityDetailPage>(`/api/conversations/${encodeURIComponent(id)}/tools?${new URLSearchParams({ run_id: runId, offset: String(offset) })}`),
  runContext: (runId: string, signal?: AbortSignal) => request<RunContextSnapshot>(`/api/runs/${encodeURIComponent(runId)}/context`, { signal }),
  computerInfo: () => request<{ kind: string; state: string; phase?: string; workspace_root?: string; browser?: string; error?: string; health?: ComputerHealth }>("/api/computers/firecracker/info"),
  computerRetry: () => request<{ accepted: boolean }>("/api/computers/firecracker/retry", { method: "POST" }),
  computerWake: () => request<{ accepted: boolean; state?: string }>("/api/computers/firecracker/wake", { method: "POST" }),
  computerAction: (input: { bot_id: string; run_id?: string; action: string; args?: Record<string, unknown> }) => request<{ ok: boolean; result?: unknown; error?: string }>("/api/computers/firecracker/actions", { method: "POST", body: JSON.stringify(input) }),
};

export function openConversationEvents(
  id: string,
  after: number,
  onEvent: (type: string, data: EventEnvelope, eventId: number) => void,
  onError: () => void,
  workspace?: { after: number; onEvent: (data: WorkspaceEventEnvelope, eventId: number) => void; onReset: () => void },
  /** Called for every frame, including the server's `ping`, so a silent stream can be told from a quiet one. */
  onActivity?: () => void,
): EventSource {
  const query = new URLSearchParams({ after: String(after) });
  // Cursor 0 means this client has seen nothing yet: start at the live head instead of replaying the whole log.
  if (workspace) query.set("workspace_after", workspace.after > 0 ? String(workspace.after) : "head");
  const source = new EventSource(`/api/conversations/${encodeURIComponent(id)}/events?${query}`);
  const types = ["message", "reaction", "run", "delta", "draft_reset", "thinking", "retrying", "memory", "memory_deleted", "schedule", "work_item", "tool", "bot", "inbox"] as const;
  for (const type of types) {
    source.addEventListener(type, (event) => {
      onActivity?.();
      const message = event as MessageEvent<string>;
      const eventId = Number(message.lastEventId);
      onEvent(type, JSON.parse(message.data) as EventEnvelope, Number.isFinite(eventId) ? eventId : after);
    });
  }
  if (workspace) {
    // Multiplexed workspace frames have no SSE id. Their lastEventId inherits
    // the conversation cursor, so only the payload revision identifies them.
    source.addEventListener("workspace", workspaceEventListener(workspace.onEvent, false));
    source.addEventListener("workspace_reset", workspace.onReset);
  }
  source.addEventListener("ping", () => onActivity?.());
  source.onerror = streamError(source, onError);
  return source;
}

export function openWorkspaceEvents(
  after: number,
  onEvent: (data: WorkspaceEventEnvelope, eventId: number) => void,
  onError: () => void,
  onReset?: () => void,
  onActivity?: () => void,
): EventSource {
  const query = new URLSearchParams({ after: after > 0 ? String(after) : "head" });
  const source = new EventSource(`/api/workspace/events?${query}`);
  source.addEventListener("workspace", workspaceEventListener(onEvent, true));
  source.addEventListener("ping", () => onActivity?.());
  if (onReset) source.addEventListener("workspace_reset", onReset);
  source.onerror = streamError(source, onError);
  return source;
}

/** A stream the browser gave up on may have met a proxy challenge, whose status EventSource hides: one cheap request classifies it. */
const streamError = (source: EventSource, onError: () => void) => () => {
  if (source.readyState === EventSource.CLOSED) void request("/api/server-info").catch(() => {});
  onError();
};

function workspaceEventListener(onEvent: (data: WorkspaceEventEnvelope, eventId: number) => void, ownSSECursor: boolean) {
  return (event: Event) => {
    const message = event as MessageEvent<string>;
    let data: Partial<WorkspaceEventEnvelope>;
    try {
      data = JSON.parse(message.data) as Partial<WorkspaceEventEnvelope>;
    } catch {
      return;
    }
    if (!data || typeof data !== "object") return;
    const eventId = data.revision;
    if (typeof eventId !== "number" || !Number.isSafeInteger(eventId) || eventId <= 0 || (ownSSECursor && eventId !== Number(message.lastEventId)) || (data.scope !== "bots" && data.scope !== "groups" && data.scope !== "config")) return;
    onEvent(data as WorkspaceEventEnvelope, eventId);
  };
}
