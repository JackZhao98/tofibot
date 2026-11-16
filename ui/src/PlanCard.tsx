import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import * as Accordion from "@radix-ui/react-accordion";
import * as Collapsible from "@radix-ui/react-collapsible";
import { AnimatePresence, MotionConfig, motion } from "motion/react";
import { BotAvatar } from "./BotAvatar";
import { TofiIcon } from "./icons";
import { LiveMark } from "./LiveMark";
import { EASE, MOTION } from "./motion";
import { useTranslation } from "./i18n";
import { dockEntries, familyPlan, finishedEntry, planEndedIncomplete, planHistory, type DockEntry, type DockStep, type StepState } from "./planModel";
import type { Bot, Run, WorkItem } from "./types";
import "./plan-card.css";

/**
 * The pinned step list (owner 2026-11-15): while a Bot works on a request with several steps, a card above the message
 * box lists its steps, marks the one in progress and the ones done, and changes as the Bot calls update_plan. It sits in
 * the composer's top slot, so the composer's measured height pads the message pane and the card never covers a message.
 * Radix Accordion carries the open state and keyboard behaviour; Motion moves steps and cards with transforms only, so
 * the composer height changes once per change instead of every frame.
 */

/** A card whose every step is done stays this long before it leaves (twice the longest motion token). */
const PLAN_LINGER_MS = MOTION.flight * 2;
/** More Bots than this fold into one "+N" line. */
const MAX_CARDS = 3;

const curve = (value: string) => (value.match(/-?[\d.]+/g) ?? []).map(Number) as [number, number, number, number];
const enter = { duration: MOTION.base / 1000, ease: curve(EASE.out) };
const leave = { duration: MOTION.panelOut / 1000, ease: curve(EASE.in) };

function StepMark({ state, live }: { state: StepState; live: boolean }) {
  if (state === "in_progress") return live ? <span className="plan-mark is-live"><LiveMark /></span> : <span className="plan-mark is-paused" />;
  if (state === "done") return <span className="plan-mark is-done"><TofiIcon name="check" size={14} aria-hidden="true" /></span>;
  if (state === "blocked" || state === "stopped") return <span className="plan-mark is-problem"><TofiIcon name={state === "stopped" ? "stop" : "alert"} size={12} aria-hidden="true" /></span>;
  if (state === "skipped") return <span className="plan-mark is-skipped"><TofiIcon name="minus" size={12} aria-hidden="true" /></span>;
  return <span className="plan-mark is-pending" />;
}

/** The ordered steps with their state words for screen readers. Static: used in history. */
export function PlanSteps({ steps, live = false, animate = false }: { steps: readonly DockStep[]; live?: boolean; animate?: boolean }) {
  const { t } = useTranslation("tasks");
  const items = steps.map(step => {
    const body = <><StepMark state={step.state} live={live} /><span className="plan-step-text">{step.text}</span><span className="sr-only">{t(`plan.state.${step.state}`)}</span></>;
    return animate
      ? <motion.li key={step.id} data-state={step.state} layout="position" initial={{ opacity: 0, y: 4 }} animate={{ opacity: 1, y: 0, transition: enter }} exit={{ opacity: 0, transition: leave }} transition={enter}>{body}</motion.li>
      : <li key={step.id} data-state={step.state}>{body}</li>;
  });
  return <ol className="plan-steps">{animate ? <AnimatePresence initial={false}>{items}</AnimatePresence> : items}</ol>;
}

function CardHeader({ entry, open, group, botName }: { entry: DockEntry; open: boolean; group: boolean; botName: string }) {
  const { t } = useTranslation("tasks");
  const line = open ? entry.title || t("plan.title") : entry.current?.text || entry.title || t("plan.title");
  const count = t("plan.count", { done: entry.done, total: entry.total });
  const state = entry.state === "done" ? t("plan.done") : entry.state === "waiting" ? t("plan.waiting") : "";
  // One live mark in the card: on the step in progress, or here while the steps are folded away.
  const mark = entry.state === "done" ? <span className="plan-mark is-done"><TofiIcon name="check-circle" size={16} aria-hidden="true" /></span>
    : entry.state === "waiting" ? <span className="plan-mark is-waiting"><TofiIcon name="clock" size={14} aria-hidden="true" /></span>
      : !open ? <span className="plan-mark is-live"><LiveMark /></span> : <span className="plan-mark is-list"><TofiIcon name="checklist" size={14} aria-hidden="true" /></span>;
  return <Accordion.Header className="plan-card-head">
    <Accordion.Trigger className="plan-card-trigger" aria-label={[group ? botName : "", line, state, count].filter(Boolean).join(" · ")}>
      {mark}
      {group && <span className="plan-card-who"><BotAvatar id={entry.botId} mini /><span className="plan-card-name">{botName}</span></span>}
      <span className="plan-card-line">{line}</span>
      <span className="plan-card-count">{state ? `${state} · ${count}` : count}</span>
      <TofiIcon className="plan-card-chevron" name="chevron-down" size={14} aria-hidden="true" />
    </Accordion.Trigger>
  </Accordion.Header>;
}

/**
 * One card per Bot with a list, newest work last. A single card opens by default; with several, each is one line until
 * the person opens one (Accordion type single). The person's choice holds for the conversation.
 */
export function PlanDock({ runs, tasks, botById, group }: { runs: readonly Run[]; tasks: readonly WorkItem[]; botById: ReadonlyMap<string, Pick<Bot, "name">>; group: boolean }) {
  const { t } = useTranslation("tasks");
  const live = useMemo(() => dockEntries(runs, tasks), [runs, tasks]);
  const [lingering, setLingering] = useState<DockEntry[]>([]);
  const previous = useRef<DockEntry[]>([]);
  const [held, setHeld] = useState(false);
  // A card that just left waits a moment for its final state: the run's done event and the list's last update arrive
  // as separate events, in either order.
  const recentlyGone = useRef(new Map<string, { entry: DockEntry; at: number }>());
  useEffect(() => {
    const now = Date.now();
    for (const old of previous.current) if (!live.some(entry => entry.key === old.key)) recentlyGone.current.set(old.key, { entry: old, at: now });
    previous.current = live;
    const finished: DockEntry[] = [];
    for (const [key, { entry, at }] of recentlyGone.current) {
      const done = live.some(item => item.key === key) ? undefined : finishedEntry(entry, runs);
      if (done) finished.push(done);
      if (done || now - at > PLAN_LINGER_MS || live.some(item => item.key === key)) recentlyGone.current.delete(key);
    }
    setLingering(current => {
      const kept = current.filter(entry => !live.some(item => item.key === entry.key) && !finished.some(item => item.key === entry.key));
      const next = [...kept, ...finished];
      return next.length === current.length && next.every((entry, index) => entry === current[index]) ? current : next;
    });
  }, [live, runs]);
  // A finished card leaves after a moment, never while the pointer or focus is on the dock.
  useEffect(() => {
    if (!lingering.length || held) return;
    const timer = window.setTimeout(() => setLingering([]), PLAN_LINGER_MS);
    return () => window.clearTimeout(timer);
  }, [lingering, held]);
  const all = [...live, ...lingering];
  const shown = all.slice(0, MAX_CARDS);
  const hidden = all.slice(MAX_CARDS);
  const [choice, setChoice] = useState<string | null>(null);
  const value = choice !== null && (choice === "" || shown.some(entry => entry.key === choice)) ? choice : shown.length === 1 ? shown[0].key : "";
  const name = (id: string) => botById.get(id)?.name ?? "Bot";
  // The DM cat perches on the top edge of the dock instead of on the field, where it would sit on the last steps:
  // the perch rule (plan-card.css) reads how far the field starts below the dock's top.
  const dock = useRef<HTMLElement>(null);
  const showing = shown.length > 0;
  // The section unmounts under the pointer or focus without a leave or blur event: start the next card unheld.
  useEffect(() => { if (!showing) setHeld(false); }, [showing]);
  useLayoutEffect(() => {
    const section = dock.current, form = section?.closest<HTMLElement>("form.composer"), row = form?.querySelector<HTMLElement>(".composer-row");
    if (!section || !form || !row) return;
    const measure = () => form.style.setProperty("--plan-dock-offset", `${Math.max(0, Math.round(row.getBoundingClientRect().top - section.getBoundingClientRect().top))}px`);
    const observer = new ResizeObserver(measure);
    observer.observe(section); observer.observe(form); measure();
    return () => { observer.disconnect(); form.style.removeProperty("--plan-dock-offset"); };
  }, [showing]);
  if (!showing) return null;
  return <MotionConfig reducedMotion="user">
    <section ref={dock} className="plan-dock" data-group={group || undefined} aria-label={t("plan.dock_label")}
      onPointerEnter={() => setHeld(true)} onPointerLeave={() => setHeld(false)}
      onFocus={() => setHeld(true)} onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setHeld(false); }}>
      <Accordion.Root type="single" collapsible value={value} onValueChange={setChoice} className="plan-dock-cards">
        <AnimatePresence initial={false}>
          {shown.map(entry => <motion.div key={entry.key} className="plan-card-frame" layout="position" initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0, transition: enter }} exit={{ opacity: 0, y: 8, transition: leave }} transition={enter}>
            <Accordion.Item value={entry.key} className="plan-card" data-plan-state={entry.state} data-source={entry.source} data-run-id={entry.runId}>
              <CardHeader entry={entry} open={value === entry.key} group={group} botName={name(entry.botId)} />
              <Accordion.Content className="plan-card-body">
                <PlanSteps steps={entry.steps} live={entry.state === "working"} animate />
              </Accordion.Content>
            </Accordion.Item>
          </motion.div>)}
        </AnimatePresence>
      </Accordion.Root>
      {hidden.length > 0 && <p className="plan-dock-more">{t("plan.more", { count: hidden.length, names: hidden.map(entry => name(entry.botId)).join(", ") })}</p>}
    </section>
  </MotionConfig>;
}

/** "Plan · 3 of 5 done", or where the run stopped. */
function useHistoryLine() {
  const { t } = useTranslation("tasks");
  return (steps: readonly DockStep[]) => {
    const stopped = steps.findIndex(step => step.state === "stopped");
    return stopped >= 0 ? t("plan.history_stopped", { step: stopped + 1, total: steps.length }) : t("plan.history", { done: steps.filter(step => step.state === "done").length, total: steps.length });
  };
}

/** The list at the head of a run's opened Activity (the newest attempt of the family that kept one). */
export function PlanActivityHead({ attempts }: { attempts: readonly Run[] }) {
  const line = useHistoryLine();
  const kept = familyPlan(attempts);
  const steps = kept && planHistory(kept.plan, kept.run);
  if (!steps) return null;
  return <div className="plan-activity-head"><p className="plan-activity-title">{line(steps)}</p><PlanSteps steps={steps} /></div>;
}

/** A run that ended before its list was done keeps one quiet line where the card was; it opens into the list. */
export function PlanHistoryRow({ attempts }: { attempts: readonly Run[] }) {
  const line = useHistoryLine();
  const kept = familyPlan(attempts);
  const steps = kept && planEndedIncomplete(kept.plan, kept.run) ? planHistory(kept.plan, kept.run) : undefined;
  if (!steps) return null;
  return <Collapsible.Root className="plan-history">
    <Collapsible.Trigger className="process-line plan-history-trigger">
      <TofiIcon className="process-line-chevron" name="chevron-right" size={14} aria-hidden="true" />
      <span className="process-line-text">{line(steps)}</span>
    </Collapsible.Trigger>
    <Collapsible.Content className="plan-history-body"><PlanSteps steps={steps} /></Collapsible.Content>
  </Collapsible.Root>;
}
