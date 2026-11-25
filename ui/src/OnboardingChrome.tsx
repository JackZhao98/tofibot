import { Banner } from "./ds/Banner/Banner";
import { Button } from "./ds/Button/Button";
import { SidebarRow } from "./ds/SidebarRow/SidebarRow";
import { useTranslation } from "./i18n";
import { ONBOARDING_STEPS, type OnboardingStep } from "./onboardingFlow";
import "./onboarding-chrome.css";

/** Sidebar row above the account block: setup was skipped, this brings it back at the step it will resume on. */
export function FinishSetupChip({ step, onClick, compact }: { step: OnboardingStep; onClick: () => void; compact?: boolean }) {
  const { t } = useTranslation("chat");
  const progress = t("onboarding.progress", { step, total: ONBOARDING_STEPS });
  return <SidebarRow as="button" className="finish-chip" data-hint={t("onboarding.chip_label")} compact={compact} data-finish-setup="true" aria-label={t("onboarding.chip_aria", { step, total: ONBOARDING_STEPS })} icon="check-circle" status="info" label={t("onboarding.chip_label")} meta={progress} onClick={onClick} />;
}

/** Above the composer when no model exists: why the box is disabled, and one button that reopens setup. */
export function ModelBanner({ name, onConnect }: { name: string; onConnect: () => void }) {
  const { t } = useTranslation("chat");
  return (
    <div className="model-banner" role="status" data-model-banner="true">
      <Banner tone="warning" title={t("onboarding.banner_title")} action={<Button variant="primary" size="sm" onClick={onConnect}>{t("onboarding.banner_action")}</Button>}>{t("onboarding.banner_body", { name })}</Banner>
    </div>
  );
}
