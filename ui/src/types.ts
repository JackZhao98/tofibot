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

export interface RunHealth {
  days: number;
  since: string;
  runs: number;
  by_status: Record<string, number>;
  failures: { code: string; count: number }[];
  tool_calls: number;
  tool_timeouts: number;
  model_calls: number;
}

export interface UsagePeriod {
  bot_id: string; input_tokens: number; output_tokens: number; requests: number;
  equivalent_usd: number; unpriced_requests: number;
}

export interface UsageCall {
  id: number; bot_id: string; bot_name: string; conversation_name: string; model: string;
  input_tokens: number; output_tokens: number; equivalent_usd: number; price_known: boolean;
  trigger_content: string; occurred_at: string;
  /** "review" = MCP safety reviewer call; absent or "chat" = the Bot's own model. */
  kind?: "chat" | "review";
}

export interface Bot {
  id: string;
  name: string;
  instructions: string;
  /** "default" follows the workspace's global model; any other id is a pin. */
  model: string;
  /** "default": the global effort when following, else the pinned model's default. Empty is legacy "medium". */
  reasoning_effort?: string;
  /** What the Bot executes with now, resolved by the server. */
  effective_model?: string;
  effective_reasoning_effort?: string;
  dm_conversation_id: string;
  created_at: string;
  archived?: boolean;
  /** The account's Primary Assistant: owns the owner's general chores. Exactly one active Bot has it. */
  is_primary?: boolean;
}

export type FollowupMode = "off" | "shadow" | "on";
export type FollowupVerdict = "useful" | "annoying" | "handled";
export interface FollowupAccount {
  max_nudges_per_day: number;
  quiet_start: string;
  quiet_end: string;
  weekend_min_importance: number;
  digest_weekday: string;
  digest_time: string;
  daily_budget_usd: number;
}
export interface FollowupBotSettings { bot_id: string; mode: FollowupMode; source_chat: boolean; source_email: boolean }
export interface FollowupLoop {
  id: string;
  bot_id: string;
  bot_name: string;
  source: "chat" | "email";
  kind: "awaiting_other" | "owner_promise" | "owner_reminder";
  topic: string;
  counterparty_name?: string;
  category: string;
  importance: number;
  band: "nudge" | "digest";
  expected_by: string;
  suggested_next?: string;
  quote?: string;
  resolution_phrases: string[];
  state: string;
  verdict?: FollowupVerdict | "";
  created_at: string;
}
export interface FollowupOverview {
  account: FollowupAccount;
  primary_bot_id: string;
  delivery_available: boolean;
  budget_paused: boolean;
  model_configured: boolean;
  day: string;
  bots: (FollowupBotSettings & { name: string; is_primary: boolean; last_sweep_at?: string })[];
  stats: { sweeps: number; loops_created: number; rejected_invalid: number; rejected_band: number; merged: number; spent_usd: number; would_nudge: number; would_digest: number; useful: number; annoying: number; handled: number };
  loops: FollowupLoop[];
}

export interface Conversation {
  /** Server-stored sidebar placement: pin_order is the 1-based pinned slot (0 = not pinned), sort_order the 1-based manual list slot (0 = never placed by hand). */
  pinned?: boolean;
  pin_order?: number;
  sort_order?: number;
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
    failure_code?: string;
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
  kind?: "notice" | "forward_result" | "message_ref" | "user_message" | "scheduled_task" | "ui_card" | "progress" | "segment" | "internal";
  card?: { type: "text" | "mail"; title?: string; body: string; from?: string; to?: string; subject?: string; summary?: string; received_at?: string; source?: string };
  sender_bot_id?: string;
  sender_bot_name?: string;
  run_id?: string;
  /** The sender's retry identity, present on a person's own messages. The server echoes it so an optimistic copy can be matched. */
  client_message_id?: string;
  /** Client only: this message has not been stored by the server (yet). It never comes from the API. */
  local?: { state: "sending" | "failed"; error?: string };
  notice?: {
    type: string;
    /** Why a "resume" notice was posted, e.g. "restart". */
    reason?: string;
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

export type ScheduleKind = "once" | "interval" | "daily" | "weekly";
export const WEEKDAYS = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"] as const;
export type Weekday = (typeof WEEKDAYS)[number];
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
  weekdays?: Weekday[];
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

export type WorkStatus = "todo" | "in_progress" | "blocked" | "review" | "input_required" | "done" | "cancelled" | "failed";
export type WorkChecklistItem = { id: string; text: string; status: "open" | "done" | "skipped" | "blocked"; reason?: string; updated_at?: string };
export type WorkWait = { id: string; kind: "bot_post" | "task_result" | "human_answer" | "command_exit" | "timer"; target?: string; target_name?: string; mode: "any" | "all"; group_id: string; deadline_at: string; note?: string };
export type TaskImpact = { given_tasks: number; held_tasks: number; open_waits: number };
export interface WorkExecution {
  id: string;
  work_item_id: string;
  root_run_id: string;
  status_run_id: string;
  status: RunStatus;
  active: boolean;
  error?: string;
  error_code?: string;
  result?: string;
  result_code?: string;
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
  assigner_kind?: "user" | "bot" | "self";
  assigner_bot_id?: string;
  assigner_bot_name?: string;
  root_task_id?: string;
  parent_task_id?: string;
  origin_run_id?: string;
  assignee_run_id?: string;
  work_conversation_id?: string;
  spec_version?: number;
  result_message_id?: string;
  result_based_on_version?: number;
  result_summary?: string;
  depends_on?: string[];
  stall_count?: number;
  message_count?: number;
  closed_at?: string;
  checklist?: WorkChecklistItem[];
  waiting_on?: WorkWait[];
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
  /** List responses cut long results to a preview; result_bytes is the stored length. */
  result_preview?: boolean;
  result_bytes?: number;
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

/** GET /api/runs/{id}/context: what a run's first model call received, part by part (D53). */
export interface RunContextPart {
  kind: string;
  label?: string;
  role?: string;
  chars: number;
  tokens: number;
  text?: string;
  text_dropped?: boolean;
  redactions?: number;
  on_demand?: boolean;
  message?: { id?: string; kind?: string; sender?: string; created_at?: string; gisted?: boolean; trimmed?: boolean; images?: number; request?: boolean };
  items?: { name: string; chars: number; tokens: number }[];
}

export interface RunContextSnapshot {
  version: number;
  run_id: string;
  conversation_id: string;
  bot_id: string;
  model?: string;
  captured_at: string;
  parts: RunContextPart[];
  history: { considered: number; included: number; skipped: number; skipped_by_summary: number; earlier_unfetched?: boolean };
  totals: { chars: number; tokens: number; on_demand_chars: number; on_demand_tokens: number; dropped_texts: number; by_kind: { kind: string; parts: number; chars: number; tokens: number }[] };
}

export interface Run {
  id: string;
  conversation_id: string;
  bot_id: string;
  status: RunStatus;
  error?: string;
  failure?: { code: "connection_interrupted" | "execution_failed" | "no_final_answer" | "budget_exhausted" | "approval_expired" | "model_unconfigured" | "model_auth_invalid" | "model_quota_exhausted" | "tool_stuck" | "resumed_repeatedly"; source: "runtime"; count?: number; message: string };
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
/** PATCH /api/schedules/{id}: content fields plus optional timing, with the timing it was opened against. */
export type SchedulePatchInput = ContentPatch & {
  kind?: ScheduleKind; run_at?: string; interval_seconds?: number; daily_time?: string; weekdays?: Weekday[]; timezone?: string;
  expected_timing?: { kind?: ScheduleKind; timezone?: string; interval_seconds?: number; daily_time?: string; weekdays?: Weekday[]; next_at_utc?: string };
};

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

/** The server watchdog's view of the shared computer (GET /api/computers/firecracker/info). */
export type ComputerHealth = { state: "healthy" | "suspect" | "restarting" | "unresponsive" | "unknown"; failures?: number; restarts?: number; restarted_at?: string; next_restart_at?: string };
