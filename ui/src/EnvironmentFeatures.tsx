import type {EnvironmentFeature} from "./environments";
import {useTranslation} from "./i18n";
import {StatusBadge} from "./settings/components";

const GROUPS = [["connected", "ok"], ["disconnected", "need"], ["off", "asleep"]] as const;

/** An environment's features on one line, grouped by status: "Connected: Built-in browser · Off: Desktop control, Files".
 * A Mac names its features after the Tofi app's parts; the Bot computer has a plain browser and desktop. */
export function EnvironmentFeatures({features, kind}: {features: EnvironmentFeature[]; kind: "mac" | "bot"}) {
  const {t} = useTranslation("computer");
  const groups = GROUPS.map(([status, badge]) => ({status, badge, items: features.filter(feature => feature.status === status)})).filter(group => group.items.length);
  if (!groups.length) return null;
  return <span className="env-line env-features">{groups.map(group => <StatusBadge key={group.status} state={group.badge}>
    <span className="env-feature-group" data-status={group.status}>{t(`environments.feature_status.${group.status}`)}{t("environments.feature_colon")}{group.items.map((feature, index) =>
      <span key={feature.key} data-feature={feature.key} data-status={feature.status}>{index ? t("environments.feature_sep") : ""}{t(kind === "mac" ? `environments.feature.${feature.key}` : `environments.feature_bot.${feature.key}`)}</span>)}</span>
  </StatusBadge>)}</span>;
}
