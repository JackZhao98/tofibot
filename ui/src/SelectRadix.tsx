// The lazy half of Select.tsx: Radix Select (listbox semantics, arrows / Home / End / typeahead, Enter and Space choose,
// Escape closes and returns focus to the field), portaled into #tofi-layers like every other TOFI overlay.
// Radix rejects an empty item value, so "" travels as a private sentinel and is mapped back on change.
import * as RadixSelect from "@radix-ui/react-select";
import { useRef, useState } from "react";
import { Icon } from "./ds/Icon/Icon";
import { layerRoot } from "./layers";
import { flatOptions, isGroup, type SelectOption, type SelectProps } from "./Select";

const EMPTY = "__tofi_empty__";
const encode = (value: string) => value === "" ? EMPTY : value;
const decode = (value: string) => value === EMPTY ? "" : value;
const POPUP_PADDING = 8;

function Item({ option }: { option: SelectOption }) {
  return <RadixSelect.Item className="tb-select-item ui-select-item" value={encode(option.value)} disabled={option.disabled} textValue={option.label} data-value={option.value}>
    <span className="tb-select-item-body ui-select-item-body">
      <RadixSelect.ItemText>{option.label}</RadixSelect.ItemText>
      {option.description && <small className="tb-select-item-note">{option.description}</small>}
    </span>
    {option.tag && <span className="tb-select-item-tag">{option.tag}</span>}
    <RadixSelect.ItemIndicator className="tb-select-item-check"><Icon name="check" size={16} /></RadixSelect.ItemIndicator>
  </RadixSelect.Item>;
}

export default function SelectRadixImpl(props: SelectProps & { options: NonNullable<SelectProps["options"]> }) {
  const { value, onValueChange, options, placeholder, disabled, id, name, className, size = "md" } = props;
  const trigger = useRef<HTMLButtonElement>(null);
  const hasEmpty = flatOptions(options).some(option => option.value === "");
  // Inside a modal (Settings) the list stacks just above it (interaction-system.css: data-layer="dialog").
  const [layer, setLayer] = useState<"dialog" | undefined>();
  return <RadixSelect.Root value={hasEmpty || value !== "" ? encode(value) : ""} onValueChange={next => onValueChange(decode(next))} disabled={disabled} name={name}
    onOpenChange={open => { if (open) setLayer(trigger.current?.closest('[aria-modal="true"]') ? "dialog" : undefined); }}>
    <RadixSelect.Trigger ref={trigger} id={id} className={`tb-select ui-select-trigger${className ? ` ${className}` : ""}`} data-size={size} data-field={props["data-field"]} data-value={value}
      aria-label={props["aria-label"]} aria-labelledby={props["aria-labelledby"]} aria-describedby={props["aria-describedby"]} aria-invalid={props["aria-invalid"]}>
      <span className="tb-select-value ui-select-value"><RadixSelect.Value placeholder={placeholder} /></span>
      <RadixSelect.Icon asChild><Icon name="chevron-down" size={16} /></RadixSelect.Icon>
    </RadixSelect.Trigger>
    <RadixSelect.Portal container={layerRoot()}>
      <RadixSelect.Content className="tofi-pop tb-glass tb-select-list ui-select-content" position="popper" sideOffset={6} collisionPadding={POPUP_PADDING} data-layer={layer}>
        <RadixSelect.Viewport className="tb-select-viewport">
          {options.map((choice, index) => isGroup(choice)
            ? <RadixSelect.Group key={`${choice.label}:${index}`}>
                <RadixSelect.Label className="tb-select-group-label ui-select-group-label">{choice.label}</RadixSelect.Label>
                {choice.options.map(option => <Item key={option.value} option={option} />)}
              </RadixSelect.Group>
            : <Item key={`${choice.value}:${index}`} option={choice} />)}
        </RadixSelect.Viewport>
      </RadixSelect.Content>
    </RadixSelect.Portal>
  </RadixSelect.Root>;
}
