import {useEffect, useState} from "react";
import {GroupMenu, PolicyControl, useGroups, usePolicyText} from "./AdminGroupBits";
import {adminOrgApi, CAPABILITIES, orgError, SELF_SERVE, type AccountPolicy, type PolicyMap, type PolicyValue} from "./adminOrgApi";
import type {AdminAccount} from "./adminApi";
import {useHostHasComputer} from "./computerCapability";
import {useTranslation} from "./i18n";
import {SettingsCard, SettingsRow, SettingsSection} from "./settings/components";
import type {CapabilityId} from "./types";
import "./admin-org.css";

/**
 * An account's Group and Overrides (design 5.1). Every capability follows the group until a value is set here; that value is an
 * override row, and "Reset to group" removes it. This is where each capability becomes settable on its own.
 */
export function AccountPolicySections({account, busy, run}: {account: AdminAccount; busy: boolean; run: (work: () => Promise<void>) => Promise<boolean>}) {
  const {t} = useTranslation("settings");
  const text = usePolicyText();
  const hostHasVm = useHostHasComputer();
  const {groups, reload} = useGroups();
  const [policy, setPolicy] = useState<AccountPolicy | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let alive = true;
    setPolicy(null); setError("");
    adminOrgApi.accountPolicy(account.id).then(value => { if (alive) setPolicy(value); }).catch(cause => { if (alive) setError(orgError(cause)); });
    return () => { alive = false; };
  }, [account.id]);
  const write = (body: {group_id?: string; overrides?: Partial<PolicyMap>}) => run(async () => { setPolicy(await adminOrgApi.setAccountPolicy(account.id, body)); reload(); });
  const group = groups?.find(item => item.id === policy?.group.id);
  const groupValue = (capability: CapabilityId): PolicyValue => group?.policies[capability] ?? SELF_SERVE[capability];
  const setOverride = (capability: CapabilityId, value: PolicyValue | null) => {
    if (!policy) return;
    const next = {...policy.overrides};
    if (value) next[capability] = value; else delete next[capability];
    void write({overrides: next});
  };
  const disabled = busy || !policy || Boolean(account.deleting);
  return <>
    <SettingsSection title={t("admin.detail.group.title")}>
      <SettingsCard>
        <SettingsRow inline label={t("admin.detail.group.label")} description={error || t("admin.detail.group.help")}
          control={<GroupMenu groups={groups ?? []} value={policy?.group.id} disabled={disabled} label={t("admin.accounts.group_of", {name: account.username})} onPick={id => void write({group_id: id})}/>}/>
      </SettingsCard>
    </SettingsSection>
    <SettingsSection title={t("admin.detail.overrides.title")} description={t("admin.detail.overrides.help")}>
      <SettingsCard className="ag-overrides">
        {CAPABILITIES.map(capability => {
          const override = policy?.overrides[capability];
          const effective = override ?? policy?.effective[capability] ?? groupValue(capability);
          return <div key={capability} className="ag-override-row" data-capability={capability} data-overridden={override ? "true" : "false"}>
            <SettingsRow label={t(`policy.capability.${capability}.name`)}
              description={override ? t("policy.overridden", {value: text(capability, override), group: text(capability, groupValue(capability))}) : t("policy.follows", {value: text(capability, effective)})}
              control={<div className="ag-override-control">
                <PolicyControl capability={capability} value={effective} hostHasVm={hostHasVm} disabled={disabled} label={t(`policy.capability.${capability}.name`)} onChange={value => setOverride(capability, value)}/>
                {override && <button type="button" className="text-button" data-action="reset" disabled={disabled} onClick={() => setOverride(capability, null)}>{t("policy.reset")}</button>}
              </div>}/>
          </div>;
        })}
      </SettingsCard>
    </SettingsSection>
  </>;
}
