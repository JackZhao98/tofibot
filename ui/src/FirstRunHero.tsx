import { Banner } from "./ds/Banner/Banner";
import { Button } from "./ds/Button/Button";
import { EmptyState } from "./ds/EmptyState/EmptyState";
import { useTranslation } from "./i18n";
import "./layout/first-run.css";

/** First run, before any Bot exists: the one thing to do next. The art is still (owner Q3: only the header cat moves). */
export function FirstRunHero({ onCreate, busy, error, side = false }: { onCreate: () => void; busy: boolean; error: string; /** Rendered inside the sidebar: shown only where the sidebar is the whole first screen (phones). */ side?: boolean }) {
  const { t } = useTranslation("chat");
  return (
    <section className={`first-run${side ? " first-run-side" : ""}`} data-first-run={side ? "side" : "pane"}>
      <EmptyState className="first-run-card" art={<img src="/brand/tofi-logo-calico-awake.svg?v=awake-calico-3" alt="" width="112" height="112" decoding="async" />} title={t("empty.first_bot_title")}
        action={<Button variant="primary" className="first-run-create" busy={busy} disabled={busy} onClick={onCreate}>{busy ? t("create.creating") : t("create.create_bot")}</Button>}>
        {t("empty.first_bot_body")}
      </EmptyState>
      {error && <Banner tone="error">{error}</Banner>}
    </section>
  );
}
