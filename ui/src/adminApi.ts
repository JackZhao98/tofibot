import {ApiError, request} from "./api";
import {i18n} from "./i18n";

export type AdminAccount = {
  id: string;
  username: string;
  email: string;
  role: "admin" | "user";
  disabled: boolean;
  must_change_password: boolean;
  deleting?: boolean;
  delete_step?: string;
  delete_error?: string;
};

export type DiskInfo = {account_id: string; quota_bytes: number; logical_bytes: number; state: string; pending_quota?: boolean};

export type Capacity = {
  total_bytes?: number;
  available_bytes: number;
  allocated_bytes?: number;
  promised_bytes?: number;
  admission_remaining_bytes: number;
  warning: boolean;
  accounts: DiskInfo[];
};

export type DeletedExport = {
  id: string;
  username: string;
  email?: string;
  created_at: number;
  expires_at: number;
  size: number;
  link_path: string;
  passphrase?: string;
  passphrase_pending: boolean;
  link_unavailable?: boolean;
  passphrase_unavailable?: boolean;
  attachments_included?: boolean;
};

export const GIB = 2 ** 30;
/** Disk sizes the Worker accepts are whole GiB from 8 to 1024; the menu offers these steps. */
export const DISK_SIZES = [8, 16, 32, 64, 128, 256, 512, 1024] as const;
export const DELETE_STEPS = ["export", "computer", "data", "sessions", "record"] as const;
export type DeleteStep = (typeof DELETE_STEPS)[number];
export const LEGACY_OWNER_ID = "legacy-owner";

export const adminApi = {
  accounts: () => request<AdminAccount[]>("/api/admin/accounts"),
  capacity: () => request<Capacity>("/api/admin/capacity"),
  exports: () => request<DeletedExport[]>("/api/admin/deleted-exports"),
  create: (body: {username: string; email: string; password: string}) => request<AdminAccount>("/api/admin/accounts", {method: "POST", body: JSON.stringify(body)}),
  patch: (id: string, body: object) => request<AdminAccount>(`/api/admin/accounts/${encodeURIComponent(id)}`, {method: "PATCH", body: JSON.stringify(body)}),
  quota: (id: string, gib: number) => request<{quota_bytes: number}>(`/api/admin/accounts/${encodeURIComponent(id)}/quota`, {method: "PATCH", body: JSON.stringify({quota_gib: gib})}),
  remove: (id: string, confirm: string) => request<{id: string; deleted: boolean; export?: DeletedExport}>(`/api/admin/accounts/${encodeURIComponent(id)}`, {method: "DELETE", body: JSON.stringify({confirm_username: confirm})}),
  passphrase: (id: string) => request<{passphrase: string}>(`/api/admin/deleted-exports/${encodeURIComponent(id)}/passphrase`),
  acknowledge: (id: string) => request<unknown>(`/api/admin/deleted-exports/${encodeURIComponent(id)}/ack`, {method: "POST"}),
  removeExport: (id: string) => request<unknown>(`/api/admin/deleted-exports/${encodeURIComponent(id)}`, {method: "DELETE"}),
};

const errorCodes = ["self_lockout", "last_admin", "account_deleting", "account_not_deactivated", "legacy_account_not_deletable", "confirmation_mismatch", "delete_in_progress", "account_delete_failed", "account_change_rejected", "quota_not_applied", "quota_unverified", "computer_transition_pending", "weak_password", "password_contains_identity", "common_password", "invalid_account", "forbidden", "export_in_use", "passphrase_unavailable"] as const;

/** Words for a failed admin call, from the server's stable code. Resolved at call time, never stored. */
export function adminError(cause: unknown): string {
  if (cause instanceof ApiError && cause.code && (errorCodes as readonly string[]).includes(cause.code)) return i18n.t(`settings:admin.error.${cause.code}` as "settings:admin.error.generic");
  return i18n.t("settings:admin.error.generic");
}

/** A random one-time password that satisfies the account policy (no look-alike characters). */
export function generatePassword(length = 20): string {
  const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789";
  const bytes = new Uint32Array(length);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, value => alphabet[value % alphabet.length]).join("");
}

export function absoluteLink(path: string): string {
  return new URL(path, window.location.origin).href;
}

export function initialOf(name: string): string {
  const first = Array.from(name.trim())[0];
  return first ? first.toUpperCase() : "?";
}
