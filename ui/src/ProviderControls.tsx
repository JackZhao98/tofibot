import {useEffect, useRef, useState, type ReactNode} from "react";
import {useTranslation} from "./i18n";
import {StatusBadge, type BadgeState} from "./settings/components";
import {ConfirmQuestion, useConfirmation} from "./ConfirmAction";
import {Button} from "./ds/Button/Button";
import {BrandMark} from "./ds/BrandMark/BrandMark";
import {resolveBrandIcon} from "./brandIcons";

/** Avoid a busy-label flash when provider verification completes immediately. */
export function useDelayedBusy(busy: boolean) {
 const [visible, setVisible] = useState(false);
 useEffect(() => {
  if (!busy) { setVisible(false); return; }
  const timer = window.setTimeout(() => setVisible(true), 150);
  return () => window.clearTimeout(timer);
 }, [busy]);
 return busy && visible;
}

/** One provider row; credentials and connection details are disclosed on demand. */
export function CardHead({id, title, state, action, status, tag, provider}: {id: string; title: string; state: BadgeState; action?: ReactNode; status?: string; tag?: string; /** The provider id (`openai`, `anthropic`): its official mark sits before the name. Omit where no mark is vendored. */ provider?: string}) {
 const brand = provider ? resolveBrandIcon("", `provider:${provider}`) : undefined;
 return <div className="provider-card-head">{brand && <BrandMark src={brand.src} size={28} fill={brand.fill} tile={brand.tile} className="provider-card-mark"/>}<h3 id={id}>{title}</h3>{tag || status ? <span className="provider-card-marks"><StatusBadge state={state}>{status}</StatusBadge>{tag && <span className="settings-tag">{tag}</span>}</span> : <StatusBadge state={state}/>}{action}</div>;
}

/** Provider confirmations use the same padded secondary buttons as the rest of Settings. */
export function ProviderConfirm({label, question, disabled, onConfirm}: {label: string; question: string; disabled?: boolean; onConfirm: () => Promise<void>}) {
 const {t} = useTranslation("settings");
 return <SwitchMethod label={label} question={question} confirm={t("confirm.action", {action: label})} disabled={disabled} onConfirm={onConfirm}/>;
}

/** A secondary action that asks in a Dialog before it changes the connection. */
export function SwitchMethod({label, question, confirm, disabled, onConfirm}: {label: string; question: string; confirm: string; disabled?: boolean; onConfirm: () => Promise<void>}) {
 const ask = useConfirmation(onConfirm, disabled);
 return <>
  <Button size="sm" type="button" disabled={disabled} onClick={ask.ask}>{label}</Button>
  {ask.asking && <ConfirmQuestion tone="neutral" question={question} confirmLabel={confirm} pending={ask.pending} error={ask.error} disabled={disabled} onConfirm={() => void ask.confirm()} onCancel={ask.cancel} />}
 </>;
}

