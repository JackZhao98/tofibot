// The one dropdown. Every pick-one-of-N control in the app renders this instead of the browser-native control, so the
// closed field and the open list look and behave the same everywhere (design tokens, light and dark, touch-sized rows).
// Radix Select lives only in the lazy SelectRadix chunk (like the menus in overlays/RadixOverlays.tsx): until it arrives
// the closed field renders as a plain button that shows the current label, so the layout never jumps.
// Options come from the `options` prop, or, so a call site can swap the element and keep its markup, from
// <option> / <optgroup> children (labels must be text). Values are strings; "" is a valid value ("none / default").
import { Children, Fragment, isValidElement, lazy, Suspense, useMemo, type ReactNode } from "react";
import "./select.css";

export type SelectOption = {
  value: string;
  label: string;
  /** A second, muted line under the label in the open list. */
  description?: string;
  disabled?: boolean;
  /** A small chip after the label in the open list (for example "New"). */
  tag?: string;
};
export type SelectGroup = { label: string; options: SelectOption[] };
export type SelectChoice = SelectOption | SelectGroup;

export type SelectProps = {
  value: string;
  onValueChange: (value: string) => void;
  options?: SelectChoice[];
  /** <option> and <optgroup> elements, as a migration convenience. Ignored when `options` is given. */
  children?: ReactNode;
  /** Shown while `value` matches no option. */
  placeholder?: string;
  disabled?: boolean;
  id?: string;
  name?: string;
  className?: string;
  /** "md" matches text inputs (44 px in settings); "sm" is for dense toolbars and rows. */
  size?: "md" | "sm";
  "aria-label"?: string;
  "aria-labelledby"?: string;
  "aria-describedby"?: string;
  "aria-invalid"?: boolean;
};

export const isGroup = (choice: SelectChoice): choice is SelectGroup => "options" in choice;
export const flatOptions = (choices: SelectChoice[]) => choices.flatMap(choice => isGroup(choice) ? choice.options : [choice]);

function textOf(node: ReactNode): string {
  if (node === null || node === undefined || typeof node === "boolean") return "";
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(textOf).join("");
  if (isValidElement<{ children?: ReactNode }>(node)) return textOf(node.props.children);
  return "";
}

type OptionElementProps = { value?: string | number; disabled?: boolean; label?: string; children?: ReactNode };
function parseChildren(children: ReactNode): SelectChoice[] {
  const out: SelectChoice[] = [];
  Children.forEach(children, child => {
    if (!isValidElement<OptionElementProps>(child)) return;
    const props = child.props;
    if (child.type === Fragment) out.push(...parseChildren(props.children));
    else if (child.type === "optgroup") out.push({ label: props.label ?? "", options: flatOptions(parseChildren(props.children)) });
    else if (child.type === "option") {
      const label = textOf(props.children);
      out.push({ value: props.value === undefined ? label : String(props.value), label, disabled: props.disabled });
    }
  });
  return out;
}

const RadixSelect = lazy(() => import("./SelectRadix"));

export function Select(props: SelectProps) {
  const { options, children, value } = props;
  const choices = useMemo(() => options ?? parseChildren(children), [options, children]);
  const size = props.size ?? "md";
  const selected = flatOptions(choices).find(option => option.value === value);
  return <Suspense fallback={<button type="button" id={props.id} disabled aria-label={props["aria-label"]} aria-labelledby={props["aria-labelledby"]}
    className={`ui-select-trigger${props.className ? ` ${props.className}` : ""}`} data-size={size} data-value={props.value} data-placeholder={selected ? undefined : ""}>
    <span className="ui-select-value">{selected?.label ?? props.placeholder ?? ""}</span></button>}>
    <RadixSelect {...props} options={choices} size={size} />
  </Suspense>;
}
