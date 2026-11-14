import type {EnvironmentFeature} from "./environments";
import {useTranslation} from "./i18n";
import {StatusBadge} from "./settings/components";

export function EnvironmentFeatures({features}: {features: EnvironmentFeature[]}) {
  const {t} = useTranslation("computer");
  return <span className="env-features">{features.map(feature => <span className="env-feature" key={feature.key} data-feature={feature.key} data-status={feature.status}>{t(`environments.feature.${feature.key}`)} <StatusBadge state={feature.status === "connected" ? "ok" : feature.status === "disconnected" ? "need" : "asleep"}>{t(`environments.feature_status.${feature.status}`)}</StatusBadge></span>)}</span>;
}
