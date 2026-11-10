import { BotAvatar } from "./BotAvatar";
import { useTranslation } from "./i18n";
import { ONBOARDING_STEPS, type OnboardingStep } from "./onboardingFlow";
import "./onboarding.css";

/** Sidebar chip above the account block: setup was skipped, this brings it back at the step it will resume on. */
export function FinishSetupChip({ step, onClick }: { step: OnboardingStep; onClick: () => void }) {
  const { t } = useTranslation("chat");
  const progress = t("onboarding.progress", { step, total: ONBOARDING_STEPS });
  return (
    <button type="button" className="finish-chip" data-finish-setup="true" aria-label={t("onboarding.chip_aria", { step, total: ONBOARDING_STEPS })} onClick={onClick}>
      <span className="finish-chip-ring" style={{ "--done": (step - 1) / ONBOARDING_STEPS } as React.CSSProperties} aria-hidden="true" />
      <span className="finish-chip-label">{t("onboarding.chip_label")}</span>
      <span className="finish-chip-count">{progress}</span>
    </button>
  );
}

/** Above the composer when no model exists: why the box is disabled, and one button that reopens setup. */
export function ModelBanner({ botId, name, onConnect }: { botId?: string; name: string; onConnect: () => void }) {
  const { t } = useTranslation("chat");
  return (
    <div className="model-banner" role="status" data-model-banner="true">
      {botId && <span className="model-banner-cat" aria-hidden="true"><BotAvatar id={botId} mini /></span>}
      <div className="model-banner-copy">
        <strong>{t("onboarding.banner_title")}</strong>
        <span>{t("onboarding.banner_body", { name })}</span>
      </div>
      <button type="button" className="primary-button" onClick={onConnect}>{t("onboarding.banner_action")}</button>
    </div>
  );
}
