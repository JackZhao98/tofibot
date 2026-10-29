import {request} from "./api";
import {formatNumber} from "./i18n/format";

/** Shapes of GET /api/storage and GET /api/admin/storage (version 1). New fields may appear; these keep their meaning. */
export type StorageClass = {class: string; bytes: number; objects: number; billable: boolean};
export type StorageItem = {id: string; name: string; bytes: number; objects: number};
export type ComputerUsage = {logical_bytes: number; allocated_bytes: number; quota_bytes: number};

export type StorageView = {
  version: number;
  account_id?: string;
  measured_at: string;
  server_bytes: number;
  files_bytes: number;
  database_bytes: number;
  physical_files_bytes: number;
  pending_copy_count: number;
  classes: StorageClass[];
  breakdown_measured_at?: string;
  /** null = unlimited. */
  limit_bytes: number | null;
  remaining_bytes: number | null;
  over_limit: boolean;
  computer: ComputerUsage | null;
  by_bot: StorageItem[];
  by_conversation: StorageItem[];
};

export type AdminStorageAccount = {
  account_id: string;
  source: "live" | "snapshot" | "none";
  measured_at: string;
  server_bytes: number;
  files_bytes: number;
  database_bytes: number;
  physical_files_bytes: number;
  pending_copy_count: number;
  limit_bytes: number | null;
  over_limit: boolean;
  computer: ComputerUsage | null;
};

export type AdminStorage = {
  version: number;
  host: {free_bytes: number | null; total_bytes: number | null; min_free_bytes: number; low: boolean};
  totals: {accounts: number; server_bytes: number; physical_files_bytes: number; computer_logical_bytes: number; computer_allocated_bytes: number; pending_copy_count: number};
  accounts: AdminStorageAccount[];
};

export const storageApi = {
  mine: (signal?: AbortSignal) => request<StorageView>("/api/storage", {signal}),
  admin: () => request<AdminStorage>("/api/admin/storage"),
  /** null removes the limit. */
  setLimit: (accountId: string, limit: number | null) => request<{server_bytes_limit: number | null}>(`/api/admin/accounts/${encodeURIComponent(accountId)}/quota`, {method: "PATCH", body: JSON.stringify({server_bytes_limit: limit})}),
};

const UNITS = ["B", "KiB", "MiB", "GiB", "TiB"] as const;
/** Binary units, one decimal under 100, none above. Unit symbols are the same in every language. */
export function formatBytes(bytes: number): string {
  let value = Math.max(0, bytes);
  let unit = 0;
  while (value >= 1024 && unit < UNITS.length - 1) { value /= 1024; unit++; }
  const digits = unit === 0 || value >= 100 ? 0 : 1;
  return `${formatNumber(value, {maximumFractionDigits: digits, minimumFractionDigits: 0})} ${UNITS[unit]}`;
}
