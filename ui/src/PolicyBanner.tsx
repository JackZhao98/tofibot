import {useState} from "react";
import "./policy-member.css";
import {usePolicy} from "./capabilityPolicy";
import {useTranslation} from "./i18n";
import {TofiIcon} from "./icons";
import {Banner} from "./settings/components";
import type {SettingsTab} from "./SettingsShell";
import type {CapabilityId} from "./types";

const PAGE: Record<CapabilityId, SettingsTab> = {ai_models: "models", web_search: "tools", web_fetch: "tools", computer: "computer"};
const KEY = "tofi:policy-banner";

/** The set of managed capabilities, so a new restriction shows the banner again after it was dismissed for an older set. */
const signature = (policy: ReturnType<typeof usePolicy>) => (["ai_models", "computer", "web_search", "web_fetch"] as const).map(id => `${id}:${policy.of(id).mode}`).join("|");

function remembered(sig: string): boolean {
  try { return localStorage.getItem(KEY) === sig; } catch { return false; }
}

/** Settings home: one info banner while the organization provides or restricts something for this account. Dismissible; the choice is kept in this browser. */
export function PolicyBanner({openTab}: {openTab: (tab: SettingsTab) => void}) {
  const {t} = useTranslation("settings");
  const policy = usePolicy();
  const sig = signature(policy);
  const [dismissed, setDismissed] = useState(() => remembered(sig));
  if (!policy.managed || !policy.first || dismissed || remembered(sig)) return null;
  const first = policy.first;
  const dismiss = () => { setDismissed(true); try { localStorage.setItem(KEY, sig); } catch { /* the banner shows again next time */ } };
  return <div className="policy-banner" data-testid="policy-banner">
    <Banner tone="info" title={t("policy.member.banner_title")} action={{label: t("policy.member.banner_action"), onClick: () => openTab(PAGE[first])}}>{t("policy.member.banner_body")}</Banner>
    <button type="button" className="icon-button policy-banner-dismiss" aria-label={t("policy.member.banner_dismiss")} onClick={dismiss}><TofiIcon name="close" size={16} aria-hidden="true"/></button>
  </div>;
}
