import { api, ApiError, request } from "./api";
import { i18n } from "./i18n";
import { errorText } from "./errorText";

const stopErrors = ["no_step_in_flight", "run_not_running"];
const stopErrorText = (cause: unknown) => cause instanceof ApiError && stopErrors.includes(cause.code ?? "") ? i18n.t(`chat:apiError.${cause.code as "no_step_in_flight"}`) : errorText(cause);
import { useTranslation } from "./i18n";
import { TofiIcon as Icon } from "./icons";
import { OverlayMenu } from "./overlays/OverlayHost";
import type { Bot, Run } from "./types";
import type { RefObject } from "react";

/**
 * The two-item Stop menu (lazy chunk: it opens on a click, the shell budget keeps it out of the entry). The Stop
 * button itself lives in the entry and stops everything by itself when this chunk is not there.
 * "Stop current step" ends the tool call in flight and the run goes on (POST /api/runs/{id}/interrupt-step; 409
 * `no_step_in_flight` while the Bot only thinks); "Stop all" cancels every working run and its delegated tasks.
 * With several Bots working in a group the step item is per Bot; "Stop all" always covers all of them.
 */
export function ComposerStopMenu({ runs, stepRunIds, bots, trigger, onClose, onStopped, onError }: {
  runs: Run[]; stepRunIds: string[]; bots: Bot[]; trigger: RefObject<HTMLElement | null>;
  onClose: () => void; onStopped: (runs: Run[]) => void; onError: (message: string) => void;
}) {
  const { t } = useTranslation("chat");
  // A step can be interrupted only while a tool call of a running run is in flight.
  const steps = runs.filter(run => run.status === "running" && stepRunIds.includes(run.id));
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
    {steps.map(run => <button type="button" key={run.id} onClick={() => { onClose(); void stopStep(run.id); }}><Icon className="composer-add-icon" name="stop" size={16} />{steps.length > 1 ? t("composer.stop_step_named", { name: nameOf(run) }) : t("composer.stop_step")}</button>)}
    <button type="button" onClick={() => { onClose(); void stopAll(); }}><Icon className="composer-add-icon" name="stop" size={16} variant="filled" />{t("composer.stop_all")}</button>
  </OverlayMenu>;
}
