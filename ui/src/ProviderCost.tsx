import {useTranslation} from "./i18n";
import {linkRuntime} from "./settings/deepLinks";

/**
 * One line about what a provider costs. A ChatGPT sign-in runs on the plan's limits; an API key is billed per token by the
 * provider, and what has been spent shows on the Usage page; a self-hosted endpoint depends on the server. No amount is
 * promised here: the price tables only turn real requests into a figure, and the Usage page is where that happens.
 */
export function ProviderCost({kind}: {kind: "plan" | "api" | "endpoint"}) {
 const {t} = useTranslation("settings");
 return <p className="field-note provider-cost" data-cost={kind}>
  {t(`providers.cost.${kind}`)}
  {kind !== "plan" && <> <button type="button" className="text-button" onClick={() => void linkRuntime().then(module => module.openHref("/settings/usage"))}>{t("providers.cost.open_usage")}</button></>}
 </p>;
}
