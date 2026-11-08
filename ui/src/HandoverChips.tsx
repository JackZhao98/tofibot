import {useEffect, useState, type CSSProperties, type KeyboardEvent} from "react";
import {request} from "./api";
import {useTranslation} from "./i18n";
import "./handover-chips.css";

type Offer = {id: string};

/**
 * First-run handovers: up to four proven tasks the server says this install can do now, shown in the first direct
 * message of a Bot whose setup is pending. A click puts the ready sentence in the composer; the person edits and
 * sends it. No model call: the list comes from the embedded trust report, the wording from the locale catalog.
 *
 * It is a quiet label and a row of keys under the Bot's messages, no card: each key is L2 of the notebook style (D51;
 * ink edge, hard shadow, the send key's press that travels onto its own shadow) and takes the width of its label.
 * Enter does not trigger `:active` in most browsers, so the keyboard press is mirrored in `data-pressed`.
 */
export default function HandoverChips({botId, onPick}: {botId: string; /** Receives the ready message; the caller puts it in the composer and focuses it. */ onPick: (message: string) => void}) {
 const {t, i18n} = useTranslation("chat");
 const [offers, setOffers] = useState<Offer[]>([]);

 useEffect(() => {
  const controller = new AbortController();
  request<{handovers: Offer[]}>(`/api/onboarding/handovers?bot_id=${encodeURIComponent(botId)}`, {signal: controller.signal})
   .then(result => { if (!controller.signal.aborted) setOffers(Array.isArray(result.handovers) ? result.handovers : []); })
   .catch(() => { if (!controller.signal.aborted) setOffers([]); });
  return () => controller.abort();
 }, [botId]);

 const press = (event: KeyboardEvent<HTMLButtonElement>) => { if (event.key === "Enter" || event.key === " ") event.currentTarget.setAttribute("data-pressed", ""); };
 const release = (event: {currentTarget: HTMLButtonElement}) => event.currentTarget.removeAttribute("data-pressed");

 // An id the catalog has no wording for is left out rather than shown bare.
 const shown = offers.filter(offer => i18n.exists(`chat:firstrun.items.${offer.id}.label`)).slice(0, 4);
 if (!shown.length) return null;
 return <section className="handover-chips" aria-label={t("firstrun.title")} data-handover-chips="">
  <h2>{t("firstrun.title")}</h2>
  <div className="handover-chips-grid" role="group" aria-label={t("firstrun.title")}>
   {shown.map((offer, index) => <button key={offer.id} type="button" className="handover-chip" data-handover={offer.id} style={{"--i": index} as CSSProperties}
    onClick={() => onPick(t(`firstrun.items.${offer.id}.message` as never))} onKeyDown={press} onKeyUp={release} onBlur={release}>
    {t(`firstrun.items.${offer.id}.label` as never)}
   </button>)}
  </div>
 </section>;
}
