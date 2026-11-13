import { useTranslation } from "../i18n";
import { SettingsCard, SettingsSection } from "./components";
import "./help-and-feedback.css";

const repository = "https://github.com/JackZhao98/tofibot";
const links = [
  ["report_issue", "/issues/new"],
  ["send_feedback", "/issues/new?title=Feedback%3A%20"],
  ["report_security", "/security/advisories/new"],
  ["github", ""],
] as const;

export function HelpAndFeedback() {
  const { t } = useTranslation("auth");
  return <SettingsSection title={t("account.help_feedback")}>
    <SettingsCard className="settings-help-links">
      {links.map(([key, path]) => <a key={key} href={repository + path} target="_blank" rel="noopener noreferrer">{t(`account.${key}`)}</a>)}
    </SettingsCard>
  </SettingsSection>;
}
