import { useTranslation } from "./i18n";
import { GREETING_SETUP_MS } from "./onboardingGreeting";
import "./greeting-setup.css";

/** The new Bot's DM before its greeting: a mock progress bar that fills over the setup time. Decorative; no real progress. */
export function GreetingSetup() {
  const { t } = useTranslation("chat");
  return (
    <div className="greeting-setup" role="status" data-greeting-setup="true">
      <p className="greeting-setup-text">{t("onboarding.greeting_setup")}</p>
      <div className="greeting-setup-bar" aria-hidden="true"><i style={{ animationDuration: `${GREETING_SETUP_MS}ms` }} /></div>
    </div>
  );
}
