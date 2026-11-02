import {useEffect, useState} from "react";
import {request} from "./api";
import {useTranslation} from "./i18n";
import "./handover-chips.css";

type Offer = {id: string};

/**
 * First-run handovers: up to four proven tasks the server says this install can do now, shown in the first direct
 * message of a Bot whose setup is pending. A click puts the ready sentence in the composer; the person edits and
 * sends it. No model call: the list comes from the embedded trust report, the wording from the locale catalog.
 */
export default function HandoverChips({botId, onPick}: {botId: string; /** Receives the ready message; the caller puts it in the composer and focuses it. */ onPick: (message: string) => void}) {
 const {t, i18n} = useTranslation("chat");
 const [offers, setOffers] = useState<Offer[]>([]);
 const [evaluated, setEvaluated] = useState(true);

 useEffect(() => {
  const controller = new AbortController();
  request<{handovers: Offer[]; evaluated?: boolean}>(`/api/onboarding/handovers?bot_id=${encodeURIComponent(botId)}`, {signal: controller.signal})
   .then(result => { if (!controller.signal.aborted) { setOffers(Array.isArray(result.handovers) ? result.handovers : []); setEvaluated(result.evaluated !== false); } })
   .catch(() => { if (!controller.signal.aborted) setOffers([]); });
  return () => controller.abort();
 }, [botId]);

 // An id the catalog has no wording for is left out rather than shown bare.
 const shown = offers.filter(offer => i18n.exists(`chat:firstrun.items.${offer.id}.label`)).slice(0, 4);
 if (!shown.length) return null;
 return <section className="handover-chips" aria-label={t("firstrun.title")} data-handover-chips="">
  <h2>{t("firstrun.title")}</h2>
  <div className="handover-chips-grid" role="group" aria-label={t("firstrun.title")}>
   {shown.map(offer => <button key={offer.id} type="button" className="handover-chip" data-handover={offer.id} onClick={() => onPick(t(`firstrun.items.${offer.id}.message` as never))}>
    {t(`firstrun.items.${offer.id}.label` as never)}
   </button>)}
  </div>
  <p className="field-note">{t(evaluated ? "firstrun.hint" : "firstrun.hint_unevaluated")}</p>
 </section>;
}
