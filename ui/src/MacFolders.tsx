import { Button } from "./ds/Button/Button";
import { IconButton } from "./ds/IconButton/IconButton";
import * as Collapsible from "@radix-ui/react-collapsible";
import { CollapsibleContent } from "./ds/Collapsible/Collapsible";
import {useCallback, useEffect, useRef, useState, type RefObject} from "react";
import {useTranslation} from "./i18n";
import {TofiIcon} from "./icons";
import {arrowOpens, OverlayMenu, pressToggle} from "./overlays/OverlayHost";
import {SettingsCard, SettingsRow, SettingsSection} from "./settings/components";
import type {FoldersResult, FoldersStatus, MacFolder} from "./desktop";
import type {Bot} from "./types";
import "./mac-folders.css";

const KNOWN = ["busy", "not_connected", "server_unsupported", "bot_unknown", "pair_failed", "folder_protected", "not_a_folder", "network_volume", "too_many_folders", "too_many_bots", "failed"] as const;
type ReasonKey = typeof KNOWN[number];
const reasonKey = (code: string | undefined): ReasonKey => (KNOWN as readonly string[]).includes(code ?? "") ? code as ReasonKey : "failed";
const sentence = (text: string) => text.charAt(0).toUpperCase() + text.slice(1);

/** The status is read once and kept current by the app's own events; nothing polls. */
function useFolders() {
  const bridge = typeof window === "undefined" ? undefined : window.tofiDesktop?.folders;
  const [status, setStatus] = useState<FoldersStatus | null>(null);
  useEffect(() => {
    if (!bridge) return;
    let live = true;
    void bridge.status().then(next => { if (live) setStatus(current => current ?? next); }, () => {});
    const off = bridge.onStatus(next => { if (live) setStatus(next); });
    return () => { live = false; off(); };
  }, [bridge]);
  return {bridge, status, setStatus};
}

function FolderMenu({folder, disabled, onReveal, onRemove}: {folder: MacFolder; disabled: boolean; onReveal: () => void; onRemove: (botID?: string) => void}) {
  const {t} = useTranslation("computer");
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const label = t("environments.more", {name: folder.name});
  return <>
    <IconButton ref={trigger} className="env-more" icon="more" label={label} filled={open} disabled={disabled} aria-haspopup="menu" aria-expanded={open}
      {...pressToggle(() => setOpen(value => !value))} onKeyDown={arrowOpens(setOpen)}/>
    {open && <OverlayMenu open onOpenChange={setOpen} className="mcp-menu env-menu" label={label} trigger={trigger as RefObject<HTMLElement | null>} anchor={trigger as RefObject<HTMLElement | null>} align="end" sideOffset={6}>
      <button type="button" data-action="reveal" onClick={() => { setOpen(false); onReveal(); }}>{t("environments.folders.reveal")}</button>
      {folder.bots.length > 1 && folder.bots.map(bot => <button key={bot.id} type="button" data-action="remove-bot" data-bot={bot.id} onClick={() => { setOpen(false); onRemove(bot.id); }}>{t("environments.folders.remove_bot", {name: bot.name})}</button>)}
      <button type="button" className="mcp-menu-danger is-danger" data-action="remove" onClick={() => { setOpen(false); onRemove(); }}>{t("environments.folders.remove")}</button>
    </OverlayMenu>}
  </>;
}

function AddMenu({bots, disabled, onPick}: {bots: Bot[]; disabled: boolean; onPick: (bot: Bot) => void}) {
  const {t} = useTranslation("computer");
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  return <>
    <Button ref={trigger} size="sm" icon="plus" className="env-add" data-action="add" disabled={disabled || bots.length === 0} aria-haspopup="menu" aria-expanded={open} {...pressToggle(() => setOpen(value => !value))} onKeyDown={arrowOpens(setOpen)}>{t("environments.folders.add")}</Button>
    {open && <OverlayMenu open onOpenChange={setOpen} className="mcp-menu env-menu env-add-menu mf-add-menu" label={t("environments.folders.add_for")} trigger={trigger as RefObject<HTMLElement | null>} anchor={trigger as RefObject<HTMLElement | null>} align="end" sideOffset={6}>
      <p className="mf-menu-title">{t("environments.folders.add_for")}</p>
      {bots.map(bot => <button key={bot.id} type="button" data-add-bot={bot.id} onClick={() => { setOpen(false); onPick(bot); }}>{bot.name}</button>)}
    </OverlayMenu>}
  </>;
}

/**
 * Settings > Environments, Mac app only: the folders the person lets a Bot work in. The Mac app shows the native folder panel and the
 * native confirmation; the page only asks, and every failure is a named reason from the code, never the app's own text.
 */
export function MacFolders({bots}: {bots: Bot[]}) {
  const {t} = useTranslation("computer");
  const {bridge, status, setStatus} = useFolders();
  const [busy, setBusy] = useState(false);
  const running = useRef(false);
  const [error, setError] = useState("");
  const [refused, setRefused] = useState<{path: string; code: string}[]>([]);
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);

  const reason = useCallback((code: string | undefined) => t(`environments.folders.reason.${reasonKey(code)}` as "environments.folders.reason.failed"), [t]);
  async function act(work: () => Promise<FoldersResult | void>) {
    if (running.current) return;
    running.current = true; setBusy(true); setError(""); setRefused([]);
    try {
      const result = await work();
      if (!alive.current || !result) return;
      setStatus(result.status);
      setRefused(result.refused ?? []);
      if (!result.ok && result.code !== "cancelled" && !(result.refused?.length && !result.code)) setError(sentence(reason(result.code)));
    } catch { if (alive.current) setError(sentence(reason("failed"))); }
    finally { running.current = false; if (alive.current) setBusy(false); }
  }

  if (!bridge || !status?.supported) return null;
  const stateLine = (folder: MacFolder) => folder.state === "ok" ? null : t(`environments.folders.state.${folder.state}` as "environments.folders.state.missing");
  return <SettingsSection className="env-section mf-section" id="mac-folders">
    <div className="env-head"><div className="mf-head-copy"><h4 className="mf-title">{t("environments.folders.title")}</h4><p>{t("environments.folders.description")}</p></div>
      <AddMenu bots={bots} disabled={busy} onPick={bot => void act(() => bridge.add(bot.id))}/></div>
    <SettingsCard className="env-list mf-list">
      {status.folders.map(folder => {
        const line = stateLine(folder);
        const label = <span className="env-name"><TofiIcon name="folder" size={18} aria-hidden="true"/><span className="env-name-text">{folder.name}</span></span>;
        const description = <>
          <code className="env-line mf-path" dir="auto">{folder.path}</code>
          {folder.bots.length > 0 && <span className="env-line mf-bots">{folder.bots.map(bot => bot.name).join(", ")}</span>}
          {line && <span className="env-line env-reason" data-folder-state={folder.state}>{line}</span>}
        </>;
        return <div key={folder.id} className="env-row mf-row" data-folder={folder.id} data-state={folder.state}>
          <SettingsRow label={label} description={description} control={<FolderMenu folder={folder} disabled={busy}
            onReveal={() => void act(() => bridge.reveal(folder.id))} onRemove={botID => void act(() => bridge.remove(folder.id, botID))}/>}/>
        </div>;
      })}
      {status.folders.length === 0 && <p className="settings-card-note" role="status" data-folders-empty="">{t("environments.folders.empty")}</p>}
    </SettingsCard>
    {error && <p className="mf-note is-error" role="alert" data-folders-error="">{error}</p>}
    {refused.map(item => <p key={item.path} className="mf-note is-error" role="alert" data-folders-refused="">{t("environments.folders.refused", {path: item.path, reason: reason(item.code)})}</p>)}
    {!status.exec.available && <p className="mf-note" role="status" data-folders-exec="">{t("environments.folders.exec_unavailable")}</p>}
    {status.tools.length > 0 && <Collapsible.Root className="st-fold mf-tools" data-folders-tools="">
      <Collapsible.Trigger className="mf-tools-trigger"><span>{t("environments.folders.tools")}</span><TofiIcon className="disclosure-chevron" name="chevron-down" size={14} aria-hidden="true"/></Collapsible.Trigger>
      <CollapsibleContent><div>
        <p className="mf-note">{t("environments.folders.tools_note")}</p>
        <ul className="mf-tools-list">{status.tools.map(tool => <li key={tool}><code dir="auto">{tool}</code></li>)}</ul>
      </div></CollapsibleContent>
    </Collapsible.Root>}
  </SettingsSection>;
}
