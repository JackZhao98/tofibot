import {useCallback, useEffect, useRef, useState, type RefObject} from "react";
import {adminOrgApi, AI_CHOICES, aiChoiceOf, aiValueFor, modesFor, orgError, SELF_SERVE, valueFor, type AdminGroup, type AiChoice, type PolicyValue} from "./adminOrgApi";
import type {AdminAccount} from "./adminApi";
import {useTranslation} from "./i18n";
import {TofiIcon} from "./icons";
import {arrowOpens, OverlayMenu} from "./overlays/OverlayHost";
import {Segmented} from "./settings/components";
import {Select} from "./Select";
import type {CapabilityId} from "./types";

/** The group list, read once and again on `reload`. The built-in groups come first, as the server orders them. */
export function useGroups(enabled = true) {
  const [groups, setGroups] = useState<AdminGroup[] | null>(null);
  const [error, setError] = useState("");
  const [version, setVersion] = useState(0);
  useEffect(() => {
    if (!enabled) return;
    let alive = true;
    adminOrgApi.groups().then(list => { if (alive) { setGroups(list); setError(""); } }).catch(cause => { if (alive) setError(orgError(cause)); });
    return () => { alive = false; };
  }, [enabled, version]);
  const reload = useCallback(() => setVersion(value => value + 1), []);
  const replace = useCallback((group: AdminGroup) => setGroups(list => list && list.map(item => item.id === group.id ? group : item)), []);
  return {groups, error, reload, replace, setGroups};
}

type GroupRef = {id: string; name: string};

/** Which group each account is in, from the `group` the accounts list carries (the self-serve group when it has no membership row). */
export function groupsByAccount(accounts: AdminAccount[] | null): Record<string, GroupRef> {
  const out: Record<string, GroupRef> = {};
  for (const account of accounts ?? []) if (account.group) out[account.id] = account.group;
  return out;
}

/** A button that names the current group and opens a menu of every group; the choice is applied by `onPick`. */
export function GroupMenu({groups, value, onPick, disabled = false, label, text, className = ""}: {groups: AdminGroup[]; value?: string; onPick: (id: string) => void; disabled?: boolean; label: string; /** Replaces the current group's name on the button (a bulk move has no current group). */ text?: string; className?: string}) {
  const {t} = useTranslation("settings");
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const current = groups.find(group => group.id === value);
  return <>
    <button ref={trigger} type="button" className={`st-button group-menu-trigger ${className}`.trim()} disabled={disabled || groups.length === 0} aria-haspopup="menu" aria-expanded={open} aria-label={label} data-group={value}
      onClick={() => setOpen(next => !next)} onKeyDown={arrowOpens(setOpen)}>
      <span>{text ?? (current ? current.name : t("admin.groups.unknown"))}</span><TofiIcon name="chevron-down" size={14} aria-hidden="true"/>
    </button>
    {open && <OverlayMenu open onOpenChange={setOpen} className="mcp-menu group-menu" label={label} trigger={trigger as RefObject<HTMLElement | null>} anchor={trigger as RefObject<HTMLElement | null>} align="end" sideOffset={6}>
      {groups.map(group => <button key={group.id} type="button" data-group-option={group.id} aria-current={group.id === value ? "true" : undefined} onClick={() => { setOpen(false); if (group.id !== value) onPick(group.id); }}>
        {group.id === value ? <TofiIcon name="check" size={16} aria-hidden="true"/> : <span className="group-menu-gap" aria-hidden="true"/>}{group.name}
      </button>)}
    </OverlayMenu>}
  </>;
}

/** The words for one capability value: "Organization keys", "Provided · VM". */
export function usePolicyText() {
  const {t} = useTranslation("settings");
  return useCallback((capability: CapabilityId, value: PolicyValue | undefined) => {
    const v = value ?? SELF_SERVE[capability];
    if (capability === "ai_models") return t(`policy.value.ai_models.${aiChoiceOf(v)}` as "policy.value.ai_models.own");
    const base = t(`policy.value.${capability}.${v.mode}` as "policy.value.computer.host");
    return capability === "computer" && v.mode === "provided" && v.source ? `${base} · ${t(`policy.source.${v.source as "vm"}`)}` : base;
  }, [t]);
}

/**
 * One capability's value as a control: a segmented list (a select for the computer's four modes) and, when the computer is
 * provided, the source. Shared by the group editor and the account's overrides, so both set values the same way.
 */
export function PolicyControl({capability, value, onChange, hostHasVm, disabled = false, label}: {capability: CapabilityId; value: PolicyValue | undefined; onChange: (value: PolicyValue) => void; hostHasVm: boolean; disabled?: boolean; label: string}) {
  const {t} = useTranslation("settings");
  const current = value ?? SELF_SERVE[capability];
  const options = modesFor(capability).map(mode => ({value: mode, label: t(`policy.value.${capability}.${mode}` as "policy.value.computer.host")}));
  const pick = (mode: string) => { if (mode !== current.mode) onChange(valueFor(capability, mode, hostHasVm)); };
  if (capability === "ai_models") {
    // Three choices with long names: a select, like the computer's four modes. Stored as a mode plus allow_own.
    const choice = aiChoiceOf(current);
    return <div className="policy-control" data-capability={capability} data-mode={current.mode} data-choice={choice}>
      <Select size="sm" value={choice} onValueChange={next => { if (next !== choice) onChange(aiValueFor(next as AiChoice)); }} disabled={disabled} aria-label={label}
        options={AI_CHOICES.map(item => ({value: item, label: t(`policy.value.ai_models.${item}` as "policy.value.ai_models.own")}))}/>
    </div>;
  }
  return <div className="policy-control" data-capability={capability} data-mode={current.mode}>
    {capability === "computer"
      ? <Select size="sm" value={current.mode} onValueChange={pick} disabled={disabled} aria-label={label} options={options}/>
      : <Segmented<string> label={label} value={current.mode} disabled={disabled} options={options} onChange={pick}/>}
    {capability === "computer" && current.mode === "provided" && <Segmented<string> label={t("policy.source.label")} value={current.source ?? "e2b"} disabled={disabled}
      options={[...(hostHasVm || current.source === "vm" ? [{value: "vm", label: t("policy.source.vm")}] : []), {value: "e2b", label: t("policy.source.e2b")}]}
      onChange={source => { if (source !== current.source) onChange({mode: "provided", source, params: {}}); }}/>}
  </div>;
}
