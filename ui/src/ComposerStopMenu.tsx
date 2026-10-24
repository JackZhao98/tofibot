import { api, ApiError, request } from "./api";
import { i18n } from "./i18n";
import { errorText } from "./errorText";

const stopErrors = ["no_step_in_flight", "run_not_running"];
const stopErrorText = (cause: unknown) => cause instanceof ApiError && stopErrors.includes(cause.code ?? "") ? i18n.t(`chat:apiError.${cause.code as "no_step_in_flight"}`) : errorText(cause);
import { useTranslation } from "./i18n";
import { TofiIcon as Icon } from "./icons";
import { arrowOpens, OverlayMenu } from "./overlays/OverlayHost";
import type { Bot, Run } from "./types";
import { useRef, useState, type RefObject } from "react";

/**
 * The composer's Stop menu (lazy chunk: it opens on a click, the shell budget keeps it out of the entry).
 * "Stop current step" ends the tool call in flight and the run goes on (POST /api/runs/{id}/interrupt-step; 409
 * `no_step_in_flight` while the Bot only thinks); "Stop all" cancels every working run and its delegated tasks.
 * With several Bots working in a group the step item is per Bot; "Stop all" always covers all of them.
 */
function StopMenu({ runs, bots, trigger, onClose, onStopped, onError }: {
  runs: Run[]; bots: Bot[]; trigger: RefObject<HTMLElement | null>;
  onClose: () => void; onStopped: (runs: Run[]) => void; onError: (message: string) => void;
}) {
  const { t } = useTranslation("chat");
  const nameOf = (run: Run) => bots.find(bot => bot.id === run.bot_id)?.name ?? "Bot";
  async function stopStep(id: string) {
    try { await request(`/api/runs/${encodeURIComponent(id)}/interrupt-step`, { method: "POST" }); onError(""); } catch (cause) { onError(stopErrorText(cause)); }
  }
  async function stopAll() {
    const results = await Promise.allSettled(runs.map(run => api.cancelRun(run.id)));
    onStopped(results.flatMap(result => result.status === "fulfilled" ? [result.value] : []));
    const failure = results.find((result): result is PromiseRejectedResult => result.status === "rejected");
    onError(failure ? stopErrorText(failure.reason) : "");
  }
  return <OverlayMenu open onOpenChange={open => { if (!open) onClose(); }} className="composer-add-menu composer-stop-menu" label={t("composer.stop_menu")} trigger={trigger} side="top" sideOffset={12}>
    {runs.map(run => <button type="button" key={run.id} onClick={() => { onClose(); void stopStep(run.id); }}><Icon className="composer-add-icon" name="stop" size={16} />{runs.length > 1 ? t("composer.stop_step_named", { name: nameOf(run) }) : t("composer.stop_step")}</button>)}
    <button type="button" onClick={() => { onClose(); void stopAll(); }}><Icon className="composer-add-icon" name="stop" size={16} variant="filled" />{t("composer.stop_all")}</button>
  </OverlayMenu>;
}

/**
 * The composer's Stop control: the trigger and its menu (the whole control is the lazy chunk, warmed at idle, so the
 * entry stays inside the shell budget). Shown while a Bot works; the trigger keeps naming the Bot it stops.
 */
export function ComposerStop({ activeRun, runs, bots, disabled, onStopped, onError }: {
  activeRun: Run; runs: Run[]; bots: Bot[]; disabled: boolean; onStopped: (runs: Run[]) => void; onError: (message: string) => void;
}) {
  const { t } = useTranslation("chat");
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement | null>(null);
  const working = runs.filter(run => run.status === "running" || run.status === "queued" || run.status === "waiting");
  return <>
    <button ref={trigger} type="button" className="composer-stop" aria-label={t("composer.stop_label", { name: bots.find(bot => bot.id === activeRun.bot_id)?.name ?? "Bot" })} aria-haspopup="menu" aria-expanded={open} disabled={disabled} onClick={() => setOpen(value => !value)} onKeyDown={arrowOpens(setOpen)}><Icon name="stop" size={16} variant="filled" />{t("composer.stop")}</button>
    {open && <StopMenu runs={working} bots={bots} trigger={trigger} onClose={() => setOpen(false)} onStopped={onStopped} onError={onError} />}
  </>;
}
