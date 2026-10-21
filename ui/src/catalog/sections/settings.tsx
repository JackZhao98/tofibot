import { useState } from "react";
import { openSettings } from "../drive";
import { Banner, DangerZone, FilePicker, Segmented, SettingsCard, SettingsDetails, SettingsRow, SettingsSection, StatusBadge, type BadgeState } from "../../settings/components";
import { AppearancePicker, ConfirmAction, Disclosure } from "../../InteractionSystem";
import { i18n } from "../../i18n";
import type { CatalogSection } from "../types";

const noop = () => {};
const t = i18n.t.bind(i18n);
const never = () => new Promise<void>(() => {});

function Theme() {
  const [value, setValue] = useState<"system" | "light" | "dark">("system");
  return <AppearancePicker value={value} onChange={setValue} />;
}
function Choice({ disabled = false }: { disabled?: boolean }) {
  const [value, setValue] = useState<"system" | "light" | "dark">("light");
  return <Segmented label={t("settings:appearance.label")} value={value} disabled={disabled} onChange={setValue}
    options={(["system", "light", "dark"] as const).map(key => ({ value: key, label: t(`settings:appearance.${key}`) }))} />;
}

export const settings: CatalogSection = {
  id: "settings", number: "17", title: "Settings rows and components", context: ["settings-page-body"],
  states: [
    { id: "rows", kind: "inline", label: "Section, card and rows", render: () => <SettingsSection title={t("settings:appearance.title")} hint="3" description="A synthetic description under the section title.">
      <SettingsCard>
        <SettingsRow label="Row with a control" description="The control sits on the right; on a phone it drops below." control={<button type="button" className="secondary-button">{t("common:action.retry")}</button>} />
        <SettingsRow inline label="Inline row" description="Switches and segmented controls stay beside the text on a phone." control={<Choice />} />
        <SettingsRow label="Row without a control" description="Read-only information." />
      </SettingsCard>
    </SettingsSection> },
    { id: "badges", kind: "inline", label: "Status badges", render: () => <div style={{ display: "flex", gap: 10, flexWrap: "wrap" }}>
      {(["ok", "need", "bad", "asleep", "testing"] as BadgeState[]).map(state => <StatusBadge key={state} state={state} />)}</div> },
    { id: "banners", kind: "inline", label: "Banners", note: "error announces itself (role=alert); the others are silent", render: () => <div style={{ display: "grid", gap: 10 }}>
      <Banner tone="error" title="Couldn't reach the provider" action={{ label: t("common:action.retry"), onClick: noop }} details={"HTTP 503 from synthetic.example.test\nretry-after: 30"}>Check the key, then try again.</Banner>
      <Banner tone="warn" title="The key expires in 3 days">Replace it before then.</Banner>
      <Banner tone="info" title="Changes apply from the next run" />
    </div> },
    { id: "banner-details", kind: "inline", label: "Banner, details open", drive: step => step.click(".settings-banner-more").then(() => undefined),
      render: () => <Banner tone="error" title="Couldn't reach the provider" details={"HTTP 503 from synthetic.example.test\nretry-after: 30"}>Check the key, then try again.</Banner> },
    { id: "segmented", kind: "inline", label: "Segmented", render: () => <Choice /> },
    { id: "segmented-focus", kind: "inline", label: "Segmented, keyboard focus", force: [{ selector: ".segmented button:nth-child(2)", state: "focus" }], render: () => <Choice /> },
    { id: "segmented-hover", kind: "inline", label: "Segmented, hover", force: [{ selector: ".segmented button:nth-child(3)", state: "hover" }], render: () => <Choice /> },
    { id: "segmented-disabled", kind: "inline", label: "Segmented, disabled", render: () => <Choice disabled /> },
    { id: "appearance", kind: "inline", label: "Appearance picker", render: () => <Theme /> },
    { id: "details", kind: "inline", label: "Details disclosure (closed / open)", render: () => <div style={{ display: "grid", gap: 8 }}>
      <SettingsDetails><p>The long version of the help text.</p></SettingsDetails>
      <Disclosure title={t("settings:disclosure.details")} defaultOpen><p>The long version of the help text.</p></Disclosure>
    </div> },
    { id: "file-picker", kind: "inline", label: "File picker (empty / chosen / disabled)", render: () => <div style={{ display: "grid", gap: 10 }}>
      <FilePicker label="Choose a file" dropHint="or drop it here" onFile={noop} />
      <FilePicker label="Choose a file" fileName="synthetic-export.tofi" onFile={noop} />
      <FilePicker label="Choose a file" dropHint="or drop it here" disabled onFile={noop} />
    </div> },
    { id: "danger", kind: "inline", label: "Danger zone with an in-place confirm", render: () => <DangerZone title="Danger zone">
      <SettingsRow label="Delete synthetic data" description="Removes every synthetic record." control={<ConfirmAction label="Delete" question="Delete everything?" onConfirm={async () => {}} />} />
    </DangerZone> },
    { id: "confirm-open", kind: "inline", label: "In-place confirm, asking", note: "focus moves to Cancel; Escape steps back",
      drive: step => step.click(".confirm-action .danger-link").then(() => undefined),
      render: () => <ConfirmAction label="Delete" question="Delete everything?" onConfirm={async () => {}} /> },
    { id: "confirm-busy", kind: "inline", label: "In-place confirm, working", drive: async step => { await step.click(".confirm-action .danger-link"); await step.click(".inline-confirmation .danger-link"); },
      render: () => <ConfirmAction label="Delete" question="Delete everything?" onConfirm={never} /> },
    { id: "confirm-error", kind: "inline", label: "In-place confirm, failed", drive: async step => { await step.click(".confirm-action .danger-link"); await step.click(".inline-confirmation .danger-link"); await step.find(".inline-confirmation .error-text"); },
      render: () => <ConfirmAction label="Delete" question="Delete everything?" onConfirm={() => Promise.reject(new Error("Synthetic: the server refused"))} /> },
    { id: "shell", kind: "app", label: "Settings window", note: "the real settings shell, opened from the gear beside the account",
      app: { path: "/b/bot_mochi" }, probe: ".settings-shell", keyboard: { close: "Escape" },
      drive: async step => { await openSettings(step); } },
  ],
};
