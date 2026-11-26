import { useTranslation } from "./i18n";
import { LiveLine } from "./ds/LiveLine/LiveLine";
import "./layout/day-one.css";

/** The new Bot's DM before its greeting: one live line for the setup time. */
export function GreetingSetup() {
  const { t } = useTranslation("chat");
  return <div className="greeting-setup" data-greeting-setup="true"><LiveLine>{t("onboarding.greeting_setup")}</LiveLine></div>;
}
