import { useEffect, useRef } from "react";
import * as Toolbar from "@radix-ui/react-toolbar";
import * as Tabs from "@radix-ui/react-tabs";
import { useTranslation } from "../i18n";
import { TofiIcon, type TofiIconName } from "../icons";
import { addressURL, signal, useHostData } from "./hostBridge";

export function BotBrowserChrome() {
  const data = useHostData();
  const { t } = useTranslation("computer");
  const text = (key: keyof typeof import("../locales/en/computer.json").bot_browser_chrome) => t(`bot_browser_chrome.${key}`);
  const input = useRef<HTMLInputElement>(null);
  // Leave an in-progress edit alone when host state changes, including loading updates.
  useEffect(() => {
    if (input.current && document.activeElement !== input.current) {
      input.current.value = data.url && data.url !== "about:blank" ? data.url : "";
    }
  }, [data]);
  const iconButton = (icon: TofiIconName, label: string, action: string, disabled = false) => (
    <Toolbar.Button className="bb-icon-button" type="button" title={label} aria-label={label}
      disabled={disabled} onClick={() => signal(action)}><TofiIcon name={icon} size={16} /></Toolbar.Button>
  );
  return (
    <div className="bb-chrome">
      <Toolbar.Root className="bb-toolbar" aria-label={text("empty_title")} data-loading={data.loading || undefined}>
        {iconButton("chevron-left", text("back"), "back", !data.canGoBack)}
        {iconButton("chevron-right", text("forward"), "forward", !data.canGoForward)}
        {iconButton(data.loading ? "close" : "retry", text(data.loading ? "stop" : "reload"), data.loading ? "stop" : "reload", !data.active)}
        <form className="bb-address" role="search" onSubmit={event => {
          event.preventDefault();
          const url = addressURL(input.current?.value || "");
          if (url) { input.current?.blur(); location.href = url; }
        }}>
          <TofiIcon name={/^https:/.test(data.url) ? "lock" : "globe"} size={14} />
          <input ref={input} className="bb-address-input" type="text" aria-label={text("address")}
            placeholder={text("address")} spellCheck={false} autoComplete="off" autoCapitalize="off"
            onFocus={event => event.currentTarget.select()} onKeyDown={event => {
              if (event.key === "Escape") { event.currentTarget.value = data.url || ""; event.currentTarget.blur(); }
            }} />
        </form>
        {data.phase === "bot" && data.acting && <span className="bb-status" role="status" title={text("bot")} aria-label={text("bot")}>
          <i className="bb-status-dot" /><span className="bb-status-text">{text("bot")}</span>
        </span>}
        {(data.phase === "human" || data.phase === "stopped") && <Toolbar.Button className="bb-hand-back" type="button"
          title={`${text("human")} · ${text("hand_back")}`} aria-label={text("hand_back")} onClick={() => signal("return")}>{text("hand_back_short")}</Toolbar.Button>}
        {iconButton("close", text("close"), "close")}
        {data.loading && <span className="bb-progress" aria-hidden="true" />}
      </Toolbar.Root>
      {data.targets.length >= 2 && <Tabs.Root value={data.active ?? ""} onValueChange={signal}>
        <Tabs.List className="bb-tabs" aria-label={text("empty_title")}>
          {data.targets.map(target => <Tabs.Trigger className="bb-tab" key={target.target_id} value={target.target_id} title={target.url}>
            {target.title || target.url}
          </Tabs.Trigger>)}
        </Tabs.List>
        {data.targets.map(target => <Tabs.Content forceMount key={target.target_id} value={target.target_id} hidden />)}
      </Tabs.Root>}
      {data.active === null && <main className="bb-empty">
        <TofiIcon name="globe" size={40} /><h1>{text("empty_title")}</h1><p>{text("empty_body")}</p>
      </main>}
    </div>
  );
}
