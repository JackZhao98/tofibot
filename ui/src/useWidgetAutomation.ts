import { useCallback, useEffect, useRef, useState } from "react";
import { request } from "./api";
import { autoOpenWidget, setWidgetFolded, spaceFoldTarget, suppressedRunsOf, type Surfaces, type WidgetId, type WidgetsByConversation } from "./surfaces";
import type { Message } from "./types";

type Switches = { artifacts: boolean; computer: boolean };
const media = (query: string) => typeof matchMedia === "function" ? matchMedia(query) : null;

/** Settings › Automatic behavior: whether the Artifacts and the Computer widget open by themselves. Both default on until the server says otherwise. */
function useSwitches(): React.RefObject<Switches> {
  const value = useRef<Switches>({ artifacts: true, computer: true });
  useEffect(() => {
    const abort = new AbortController();
    const load = () => request<{ items: { key: string; value: unknown }[] }>("/api/automation-settings", { signal: abort.signal }).then(body => {
      const on = (key: string) => body.items?.find(item => item.key === key)?.value !== false;
      value.current = { artifacts: on("widget_artifacts_auto"), computer: on("widget_computer_auto") };
    }, () => undefined);
    void load();
    window.addEventListener("tofi:automation-changed", load);
    return () => { abort.abort(); window.removeEventListener("tofi:automation-changed", load); };
  }, []);
  return value;
}

/**
 * The automatic life of the widgets (docs/design/widgets-and-sidebar.md sections 2.1, 3, 6): the Artifacts widget opens when the first
 * file a Bot made arrives in this conversation; the Computer widget opens when a Bot takes the desktop here, folds into its strip 30 s
 * after the Bot lets go, and stays closed for a run the person closed it in; with three or more widgets open on a short window the ones at the bottom of the
 * person's order fold (never the computer), except the ones the person unfolded. Nothing here moves focus or closes a cover.
 */
export function useWidgetAutomation({ activeId, ready, messages, columnMode, hasComputer, surfaces, setSurfaces, botRunId, holderKind, bigComputer, memory }: {
  activeId?: string; ready: boolean; messages: readonly Message[]; columnMode: boolean; hasComputer: boolean; surfaces: Surfaces;
  setSurfaces: (update: (current: Surfaces) => Surfaces) => void;
  /** The run of the Bot that holds the desktop in this conversation. */
  botRunId?: string;
  /** Who holds the desktop now, in any conversation. */
  holderKind?: string;
  bigComputer: boolean;
  /** What each conversation remembers of its widgets (surfaces.ts); the runs the person closed the computer in are kept there. */
  memory: { readonly current: WidgetsByConversation };
}) {
  const switches = useSwitches();
  const [short, setShort] = useState(() => media("(max-height: 759.98px)")?.matches ?? false);
  useEffect(() => {
    const list = media("(max-height: 759.98px)");
    if (!list) return;
    const sync = () => setShort(list.matches);
    list.addEventListener("change", sync);
    return () => list.removeEventListener("change", sync);
  }, []);

  // Artifacts: a Bot's file that arrives after the conversation was loaded (older pages and the first load are not arrivals).
  const seen = useRef(new Map<string, { maxSeq: number; ids: Set<string> }>());
  const filed = useRef(new Set<string>());
  useEffect(() => {
    if (!activeId || !ready) return;
    const mine = messages.filter(message => message.conversation_id === activeId && !message.local);
    const files = mine.filter(message => message.role === "assistant" && message.attachments?.length);
    const maxSeq = mine.reduce((top, message) => Math.max(top, message.seq), 0);
    const record = seen.current.get(activeId);
    if (!record) { seen.current.set(activeId, { maxSeq, ids: new Set(files.map(message => message.id)) }); return; }
    const arrived = files.filter(message => message.seq > record.maxSeq && !record.ids.has(message.id));
    record.maxSeq = Math.max(record.maxSeq, maxSeq);
    for (const message of arrived) record.ids.add(message.id);
    if (arrived.length && columnMode && switches.current.artifacts && !filed.current.has(activeId)) {
      filed.current.add(activeId);
      setSurfaces(state => autoOpenWidget(state, "artifacts"));
    }
  }, [activeId, ready, messages, columnMode, setSurfaces, switches]);

  // Computer: a Bot takes the desktop in this conversation.
  const lastRun = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (!botRunId) { lastRun.current = undefined; return; }
    if (lastRun.current === botRunId) return;
    lastRun.current = botRunId;
    if (!columnMode || !hasComputer || !switches.current.computer || !activeId || suppressedRunsOf(memory.current, activeId).has(botRunId)) return;
    // A folded strip opens again for a new run: the person should see the Bot at work.
    setSurfaces(state => state.widgets.includes("computer") ? setWidgetFolded(state, "computer", false) : autoOpenWidget(state, "computer"));
  }, [botRunId, columnMode, hasComputer, setSurfaces, switches, activeId, memory]);

  // Thirty seconds after the Bot let go of the desktop, the open computer folds into its strip (not while someone else holds it or the big window is up).
  const botRan = useRef(false);
  const computerOpen = surfaces.widgets.includes("computer");
  const computerFolded = surfaces.folded.includes("computer");
  if (!computerOpen) botRan.current = false;
  else if (botRunId) botRan.current = true;
  useEffect(() => {
    if (!computerOpen || computerFolded || !botRan.current || botRunId || holderKind === "human" || bigComputer) return;
    const timer = window.setTimeout(() => { botRan.current = false; setSurfaces(state => setWidgetFolded(state, "computer", true)); }, 30_000);
    return () => window.clearTimeout(timer);
  }, [computerOpen, computerFolded, botRunId, holderKind, bigComputer, setSurfaces]);

  // Space: three or more widgets on a short window. The bottom one of the person's order folds first (the Usage widget, then Artifacts
  // and Members in the default order); the next follows only if the column still overflows. The computer never folds this way.
  const unfolded = useRef(new Map<string, Set<WidgetId>>());
  const latest = useRef(surfaces);
  latest.current = surfaces;
  useEffect(() => {
    if (!columnMode || !short || !activeId || surfaces.widgets.length < 3) return;
    const timer = window.setTimeout(() => {
      const column = document.querySelector<HTMLElement>(".widget-column");
      const overflow = Boolean(column && column.scrollHeight > column.clientHeight + 1);
      const next = spaceFoldTarget(latest.current, { skip: unfolded.current.get(activeId), overflow });
      if (next) setSurfaces(state => state.widgets.length < 3 ? state : setWidgetFolded(state, next, true));
    }, 300);
    return () => window.clearTimeout(timer);
  }, [columnMode, short, activeId, surfaces.widgets, surfaces.folded, surfaces.order, setSurfaces]);

  const noteClose = useCallback((id: WidgetId) => {
    if (id === "computer" && lastRun.current && activeId) suppressedRunsOf(memory.current, activeId).add(lastRun.current);
    if (id === "artifacts" && activeId) filed.current.add(activeId);
  }, [activeId, memory]);
  const noteUnfold = useCallback((id: WidgetId) => {
    if (!activeId) return;
    const set = unfolded.current.get(activeId) ?? new Set<WidgetId>();
    set.add(id);
    unfolded.current.set(activeId, set);
  }, [activeId]);
  return { noteClose, noteUnfold };
}
