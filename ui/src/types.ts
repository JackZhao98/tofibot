export type ConversationKind = "dm" | "group";
export type MessageRole = "user" | "assistant" | "tool";
export type RunStatus = "queued" | "running" | "waiting" | "done" | "failed" | "cancelled" | "interrupted";

export interface ContextUsage {
  run_id: string; conversation_id: string; conversation_name: string; bot_id: string; bot_name: string;
  run_status: RunStatus; model: string; window_tokens: number; window_known: boolean; compact_at: number;
  estimated_input: number; last_input: number; total_input: number; total_output: number;
  compact_count: number; updated_at: string;
}

export interface AgentUsageTotal {
  bot_id: string; bot_name: string; input_tokens: number; output_tokens: number; runs: number; compactions: number;
}

export interface UsagePeriod {
  bot_id: string; input_tokens: number; output_tokens: number; requests: number;
  equivalent_usd: number; unpriced_requests: number;
}

export interface UsageCall {
  id: number; bot_id: string; bot_name: string; conversation_name: string; model: string;
  input_tokens: number; output_tokens: number; equivalent_usd: number; price_known: boolean;
  trigger_content: string; occurred_at: string;
}

export interface Bot {
  id: string;
  name: string;
  instructions: string;
  model: string;
  reasoning_effort?: string;
  dm_conversation_id: string;
  created_at: string;
  archived?: boolean;
}

export interface Conversation {
  unread_count?: number;
  task_state?: {
    status: "executing" | "needs_attention" | "waiting" | "failed" | "completed" | "cancelled" | "finishing" | "expired";
    run_id?: string;
    question_id?: string;
    draft_id?: string;
    draft_status?: "pending" | "sending" | "sent" | "declined" | "unknown";
    draft_demo?: boolean;
    result_message_id?: string;
    failure_reason?: string;
    can_retry?: boolean;
  };
  read_seq?: number;
  id: string;
  kind: ConversationKind;
  name: string;
  bot_id?: string;
  bot_ids: string[];
  last_user_message_at?: string;
  working_bot_ids?: string[];
  updated_at: string;
  last_message?: {
    id: string;
    seq: number;
    role: MessageRole;
    kind?: string;
    sender_bot_id?: string;
    content: string;
    created_at: string;
    internal?: boolean;
  };
  archived?: boolean;
  user_visible?: boolean;
}

export interface Message {
  id: string;
  conversation_id: string;
  seq: number;
  role: MessageRole;
  kind?: "notice" | "forward_result" | "message_ref" | "user_message" | "scheduled_task" | "ui_card" | "progress" | "segment";
  card?: { type: "text" | "mail"; title?: string; body: string; from?: string; to?: string; subject?: string; summary?: string; received_at?: string; source?: string };
  sender_bot_id?: string;
  sender_bot_name?: string;
  run_id?: string;
  notice?: {
    type: string;
    from_bot_id: string;
    to_bot_id: string;
    to_bot_ids?: string[];
    target_conversation_id: string;
    target_run_id: string;
    target_run_ids?: string[];
    target_name?: string;
    target_bot_ids?: string[];
  };
  content: string;
  reactions?: Reaction[];
  attachments?: Attachment[];
  created_at: string;
}

export interface Reaction {
  emoji: string;
  actor_type: "user" | "bot";
  actor_id?: string;
  created_at: string;
}

export interface Attachment {
  id: string;
  conversation_id: string;
  message_id?: string;
  name: string;
  mime: string;
  size: number;
  created_at: string;
}

export interface StreamDraft {
  seq?: number;
  run_id: string;
  conversation_id: string;
  bot_id: string;
  message_id: string;
  content: string;
  status: "active" | "done" | "cancelled";
  revision: number;
  created_at: string;
  updated_at: string;
}

export type ScheduleKind = "once" | "interval" | "daily";
export type ScheduleStatus = "active" | "paused" | "completed" | "deleted";

export interface Schedule {
  id: string;
  title?: string;
  description?: string;
  created_by?: "user" | "bot";
  conversation_id: string;
  bot_id: string;
  content: string;
  kind: ScheduleKind;
  timezone: string;
  next_at_utc: string;
  interval_seconds?: number;
  daily_time?: string;
  status: ScheduleStatus;
  last_run_id?: string;
  execution_status?: RunStatus;
  created_at: string;
  updated_at: string;
  conversation_name?: string;
  conversation_kind?: ConversationKind;
  bot_name?: string;
  conversation_archived?: boolean;
  bot_archived?: boolean;
}

export type WorkStatus = "todo" | "in_progress" | "blocked" | "review" | "done" | "cancelled";
export interface WorkExecution {
  id: string;
  work_item_id: string;
  root_run_id: string;
  status_run_id: string;
  status: RunStatus;
  active: boolean;
  error?: string;
  result?: string;
  result_message_id?: string;
  created_at: string;
  updated_at: string;
}

export interface WorkItem {
  id: string;
  conversation_id: string;
  conversation_name?: string;
  conversation_archived?: boolean;
  bot_archived?: boolean;
  bot_id: string;
  bot_name?: string;
  kind: "goal" | "task";
  title: string;
  description: string;
  status: WorkStatus;
  parent_goal_id?: string;
  created_at: string;
  updated_at: string;
  completed_at?: string;
  execution?: WorkExecution;
}

export type ToolActivityStatus = "queued" | "running" | "completed" | "failed" | "interrupted";

export interface ToolActivity {
  conversation_id: string;
  bot_id: string;
  run_id: string;
  call_id: string;
  name: string;
  arguments: string;
  result: string;
  outcome?: { status: string; code: string; execution_certainty: string; message: string; next_action: string; attempts?: number; retry_limit?: number; repair_limit?: number };
  status: ToolActivityStatus;
  truncated: boolean;
  started_at: string;
  updated_at: string;
}

export interface ToolActivityRunSummary {
  run_id: string;
  bot_id: string;
  tool_count: number;
  completed_count: number;
  failed_count: number;
  interrupted_count: number;
  pending_count: number;
  expired_count?: number;
  skipped_count?: number;
  started_at: string;
  updated_at: string;
}

export interface ToolActivityDetailPage {
  activities: ToolActivity[];
  tool_count: number;
  has_more: boolean;
}

export interface Run {
  id: string;
  conversation_id: string;
  bot_id: string;
  status: RunStatus;
  error?: string;
  failure?: { code: "connection_interrupted" | "execution_failed" | "no_final_answer" | "budget_exhausted" | "approval_expired" | "model_unconfigured" | "model_auth_invalid" | "model_quota_exhausted"; source: "runtime"; message: string };
  stop_reason?: "approval_expired";
  finishing_reason?: "approval_expired";
  parent_run_id?: string;
  model?: string;
  kind?: string;
  origin_conversation_id?: string;
  trigger_message_id?: string;
  queue_seq?: number;
  created_at: string;
  updated_at: string;
}

export interface MemoryInput { title: string; description: string; content: string; }
export type ContentPatch = Partial<MemoryInput> & { expected?: Partial<MemoryInput> };

export interface Memory {
  id: string;
  title?: string;
  description?: string;
  conversation_id: string;
  bot_id?: string;
  content: string;
  revision: number;
  created_at: string;
  updated_at: string;
}

export interface Config {
  /** True when any model provider (Codex, OpenAI or Claude) is configured. */
  model_configured: boolean;
  default_model: string;
  /** First configured provider; kept for compatibility. */
  provider: string;
}

export type ModelProviderID = "codex" | "openai" | "anthropic";

/** One model provider as GET /api/providers reports it. Keys are never returned. */
export interface ModelProviderStatus {
  id: ModelProviderID;
  label: string;
  kind: "oauth" | "api_key";
  configured: boolean;
  /** Codex only. */
  status?: "connected" | "needs_reconnect" | "disconnected";
  /** API-key providers only, e.g. "…abcd". */
  key_hint?: string;
  /** RFC3339, or "" when never verified. */
  verified_at?: string;
  error?: string;
}

export interface ModelOption {
  id: string;
  name: string;
  /** Absent on older servers; inferred from the id then. */
  provider?: ModelProviderID;
  reasoning_efforts: string[];
  default_reasoning: string;
}

export interface ModelCatalog {
  models: ModelOption[];
  source: string;
  warning?: string;
}

export interface EventEnvelope {
  conversation_id: string;
  bot?: Bot;
  [key: string]: unknown;
}

export type WorkspaceEventScope = "bots" | "groups" | "config";

/** Workspace events carry only an invalidation scope and its durable revision. */
export interface WorkspaceEventEnvelope {
  scope: WorkspaceEventScope;
  revision: number;
}
