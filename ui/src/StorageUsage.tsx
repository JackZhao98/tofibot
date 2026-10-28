import {useEffect, useState} from "react";
import {useTranslation} from "./i18n";
import {formatDateTime} from "./i18n/format";
import {Banner, SettingsCard, SettingsSection} from "./settings/components";
import {formatBytes, storageApi, type StorageClass, type StorageItem, type StorageView} from "./storageApi";
import "./storage-usage.css";

const FILE_CLASSES = ["uploads", "published", "generated", "outbox", "mirror"] as const;

/** Settings storage card: one bar for what the server keeps, a class legend, the computer disk as a separate line. */
export function StorageUsage() {
  const {t} = useTranslation("storage");
  const [view, setView] = useState<StorageView | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    storageApi.mine(controller.signal).then(next => { if (!Array.isArray(next?.classes)) throw new Error("storage"); setView({...next, by_bot: next.by_bot ?? [], by_conversation: next.by_conversation ?? []}); setFailed(false); }).catch(() => { if (!controller.signal.aborted) setFailed(true); });
    return () => controller.abort();
  }, []);

  if (!view) return failed ? <SettingsSection title={t("title")}><Banner tone="warn" title={t("unavailable")}/></SettingsSection> : null;
  const limit = view.limit_bytes;
  const scale = limit ?? Math.max(view.server_bytes, 1);
  const percent = Math.min(100, Math.round((view.server_bytes / Math.max(scale, 1)) * 100));
  const parts = [...view.classes.filter(item => item.billable && item.bytes > 0)];
  const label = limit === null ? t("used", {used: formatBytes(view.server_bytes)}) : t("used_of", {used: formatBytes(view.server_bytes), limit: formatBytes(limit)});
  return <SettingsSection title={t("title")} description={t("description")}>
    <SettingsCard className="storage-card">
      <div className="storage-main">
        <div className="storage-line"><strong>{t("bar")}</strong><span>{label}{limit === null && <small>{t("no_limit")}</small>}</span></div>
        <div className={`storage-bar${view.over_limit ? " is-full" : ""}`} role="progressbar" aria-label={t("bar")} aria-valuemin={0} aria-valuemax={100} aria-valuenow={limit === null ? undefined : percent} aria-valuetext={label}>
          {parts.map(part => <i key={part.class} className={`storage-seg is-${part.class}`} style={{width: `${Math.max(0.5, (part.bytes / Math.max(scale, 1)) * 100)}%`}}/>)}
        </div>
        {view.over_limit && <p className="storage-full" role="status">{t("full")}</p>}
        <ul className="storage-legend">
          {[...FILE_CLASSES.map(name => view.classes.find(item => item.class === name)), view.classes.find(item => item.class === "db")].filter((item): item is StorageClass => Boolean(item && item.bytes > 0)).map(item =>
            <li key={item.class}><i className={`storage-key is-${item.class}`} aria-hidden="true"/><span>{t(`class.${item.class}` as "class.uploads")}</span><b>{formatBytes(item.bytes)}</b></li>)}
        </ul>
        {view.pending_copy_count > 0 && <p className="storage-note">{t("pending", {count: view.pending_copy_count})}</p>}
      </div>
      {view.computer && <div className="storage-main">
        <div className="storage-line"><strong>{t("computer")}</strong><span>{view.computer.quota_bytes > 0 ? t("computer_used", {used: formatBytes(view.computer.logical_bytes), quota: formatBytes(view.computer.quota_bytes)}) : t("computer_unknown", {used: formatBytes(view.computer.logical_bytes)})}</span></div>
      </div>}
      {(view.by_bot.length > 0 || view.by_conversation.length > 0) && <details className="storage-breakdown">
        <summary>{t("breakdown")}</summary>
        <div className="storage-tables">
          <Table title={t("by_bot")} items={view.by_bot.slice(0, 10)} nameOf={item => item.id ? item.name || item.id : t("you")}/>
          <Table title={t("by_conversation")} items={view.by_conversation.slice(0, 10)} nameOf={item => item.name || t("unnamed")}/>
        </div>
        {view.breakdown_measured_at && <p className="storage-note">{t("measured", {time: formatDateTime(view.breakdown_measured_at)})}</p>}
      </details>}
    </SettingsCard>
  </SettingsSection>;
}

function Table({title, items, nameOf}: {title: string; items: StorageItem[]; nameOf: (item: StorageItem) => string}) {
  if (items.length === 0) return null;
  return <div className="storage-table"><h5>{title}</h5><ul>{items.map(item => <li key={item.id || "you"}><span>{nameOf(item)}</span><b>{formatBytes(item.bytes)}</b></li>)}</ul></div>;
}
