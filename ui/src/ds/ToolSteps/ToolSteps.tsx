import * as Collapsible from "@radix-ui/react-collapsible";
import { CollapsibleContent } from "../Collapsible/Collapsible";
import type { CSSProperties, ReactNode } from "react";
import { useTranslation } from "../../i18n";
import { Icon } from "../Icon/Icon";
import { cx } from "../cx";
import "../ruled.css";
import "./ToolSteps.css";

export type ToolStepData = {
  tool: string;
  /** What the call acted on, as the person would say it (a query, a page, a file), never raw JSON. */
  arg?: ReactNode;
  /** The duration, like `2.1s`. A running call shows a quiet ellipsis. */
  dur?: string;
  /** done (default), run (live), bad (failed: the one row that gets a mark), wait. */
  state?: "done" | "run" | "bad" | "wait";
  /** Output text. Each line sits one level deeper, in mono. */
  result?: string;
  /** A failure's reason in the product's words, one level deeper in danger text. */
  note?: ReactNode;
  /** Calls this one ran (a sub-agent, a batch). Keep to three levels. */
  steps?: ToolStepData[];
  /** Whether the nested calls start open. Default open. */
  open?: boolean;
  avatar?: ReactNode;
};

export type ToolStepsProps = {
  steps: ToolStepData[];
  label?: string;
  /** The indent level of the first row (1 under a ProcessLine head). */
  level?: number;
  className?: string;
};

function Row({ level, kind, children, dur }: { level: number; kind?: string; children?: ReactNode; dur?: ReactNode }) {
  return <div className={cx("tb-tool-row", kind && `is-${kind}`)} style={{ "--lv": level } as CSSProperties}>
    <span className="tb-tool-text">{children}</span>
    <span className="tb-tool-dur">{dur}</span>
  </div>;
}

function Step({ step, level }: { step: ToolStepData; level: number }) {
  const { t } = useTranslation();
  const state = step.state ?? "done";
  const kids = step.steps && step.steps.length ? step.steps : null;
  const name = <>
    {state === "bad" ? <span className="tb-tool-sr">{t("ds.step.failed")}</span> : null}
    {step.avatar ? <span className="tb-tool-av">{step.avatar}</span> : null}
    <span className="tb-tool-name">{step.tool}</span>{step.arg ? <span className="tb-tool-arg">{step.arg}</span> : null}
  </>;
  const dur = step.dur ?? (state === "run" ? "…" : "");
  const extra = <>
    {step.note ? <Row level={level + 1} kind="note">{step.note}</Row> : null}
    {step.result ? step.result.split("\n").map((line, index) => <Row key={index} level={level + 1} kind="result">{line}</Row>) : null}
  </>;
  if (!kids) return <li className={cx("tb-tool", `is-${state}`)}><Row level={level} kind={state} dur={dur}>{name}</Row>{extra}</li>;
  return <li className={cx("tb-tool", `is-${state}`, "has-kids")}>
    <Collapsible.Root defaultOpen={step.open ?? true}>
      <div className={cx("tb-tool-row", `is-${state}`)} style={{ "--lv": level } as CSSProperties}>
        <Collapsible.Trigger className="tb-tool-text tb-tool-fold" aria-label={t("ds.toolSteps")}>{name}<Icon name="chevron-down" size={12} className="tb-tool-chev" /></Collapsible.Trigger>
        <span className="tb-tool-dur">{dur}</span>
      </div>
      {extra}
      <CollapsibleContent forceMount><ToolSteps steps={kids} level={level + 1} /></CollapsibleContent>
    </Collapsible.Root>
  </li>;
}

/** A Bot's tool calls for one reply, written on a ruled scratch pad. Indentation carries the structure: only the title in the head
 *  is bold, the tool is plain mono, the argument is softer, the duration sits on one right edge. There is no status mark except the
 *  cross on a failed call (S18). Sits inside ProcessLine. */
export function ToolSteps({ steps, label, level = 1, className }: ToolStepsProps) {
  return <ol className={cx("tb-tools", level > 1 && "is-nested", className)} aria-label={label}>
    {steps.map((step, index) => <Step key={index} step={step} level={level} />)}
  </ol>;
}
