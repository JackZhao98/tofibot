import {useEffect, useState} from "react";
import {api} from "./api";
import {useTranslation} from "./i18n";
import {TofiIcon} from "./icons";
import {Banner, SettingsCard, SettingsSection, StatusBadge} from "./settings/components";
import type {ModelProviderStatus} from "./types";
import "./provider-settings.css";
import "./policy-member.css";

/**
 * Settings > AI Models while the organization provides the models (user groups, design 5.2): which providers are available, read-only.
 * No key input, no ChatGPT sign-in and no address: a person never sees the organization's credential, only whether it is there.
 */
export function ProvidedModels({refreshToken}: {refreshToken: number}) {
  const {t} = useTranslation("settings");
  const [list, setList] = useState<ModelProviderStatus[] | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    setFailed(false);
    api.listProviders(controller.signal).then(setList).catch(() => { if (!controller.signal.aborted) setFailed(true); });
    return () => controller.abort();
  }, [refreshToken]);
  // A ChatGPT sign-in is never an organization resource, so Codex does not appear while models are provided.
  const providers = (list ?? []).filter(item => item.id !== "codex" && item.source === "organization");
  const available = providers.filter(item => item.configured);
  return <SettingsSection className="model-providers provided-models" title={t("providers.title")}>
    {failed && <p className="error-text" role="alert">{t("providers.load_failed")}</p>}
    <SettingsCard className="provider-list provided-card">
      <article className="detail-content provider-card" data-policy="provided">
        <div className="provider-card-head"><h3><TofiIcon name="lock" size={16} aria-hidden="true"/> {t("policy.member.models.title")}</h3><span className="provider-card-marks"><span className="settings-tag" data-tag="organization">{t("policy.member.managed")}</span></span></div>
        <p className="provider-card-note">{t("policy.member.models.body")}</p>
        {list && providers.length > 0 && <ul className="provided-list">
          {providers.map(item => <li key={item.id} data-provider={item.id} data-configured={item.configured}><strong>{item.label}</strong><StatusBadge state={item.configured ? "ok" : "need"}>{item.configured ? t("policy.member.models.available") : t("policy.member.models.not_provided")}</StatusBadge></li>)}
        </ul>}
        {!list && !failed && <p className="provider-key-line is-muted" role="status">{t("providers.status_loading")}</p>}
      </article>
    </SettingsCard>
    {list && available.length === 0 && <Banner tone="warn" title={t("policy.member.models.waiting_title")}>{t("policy.member.models.waiting_body")}</Banner>}
  </SettingsSection>;
}
