import { useEffect, useRef } from "react";
import * as Toolbar from "@radix-ui/react-toolbar";
import { useTranslation } from "../i18n";
import type { IconName } from "../ds/Icon/Icon";
import { Button } from "../ds/Button/Button";
import { IconButton } from "../ds/IconButton/IconButton";
import { Badge } from "../ds/Badge/Badge";
import { EmptyState } from "../ds/EmptyState/EmptyState";
import { TextInput } from "../ds/TextInput/TextInput";
import { Tabs } from "../ds/Tabs/Tabs";
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
  const iconButton = (icon: IconName, label: string, action: string, disabled = false) => (
    <Toolbar.Button asChild><IconButton className="bb-icon-button" size="sm" icon={icon} label={label} disabled={disabled} onClick={() => signal(action)} /></Toolbar.Button>
  );
  return (
    <div className="bb-chrome">
      <Toolbar.Root className="bb-toolbar" aria-label={text("empty_title")} data-loading={data.loading || undefined}>
        {iconButton("chevron-left", text("back"), "back", !data.canGoBack)}
        {iconButton("chevron-right", text("forward"), "forward", !data.canGoForward)}
        {iconButton(data.loading ? "close" : "retry", text(data.loading ? "stop" : "reload"), data.loading ? "stop" : "reload", !data.active)}
        <form className="bb-form" role="search" onSubmit={event => {
          event.preventDefault();
          const url = addressURL(input.current?.value || "");
          if (url) { input.current?.blur(); location.href = url; }
        }}>
          <TextInput ref={input} className="bb-address" icon={/^https:/.test(data.url) ? "lock" : "globe"} aria-label={text("address")}
            placeholder={text("address")} spellCheck={false} autoComplete="off" autoCapitalize="off"
            onFocus={event => event.currentTarget.select()} onKeyDown={event => {
              if (event.key === "Escape") { event.currentTarget.value = data.url || ""; event.currentTarget.blur(); }
            }} />
        </form>
        {data.phase === "bot" && data.acting && <span className="bb-status" role="status" title={text("bot")} aria-label={text("bot")}><Badge tone="working"><span className="bb-status-text">{text("bot")}</span></Badge></span>}
        {(data.phase === "human" || data.phase === "stopped") && <Toolbar.Button asChild><Button className="bb-hand-back" variant="primary" size="sm"
          title={`${text("human")} · ${text("hand_back")}`} aria-label={text("hand_back")} onClick={() => signal("return")}>{text("hand_back_short")}</Button></Toolbar.Button>}
        {iconButton("close", text("close"), "close")}
        {data.loading && <span className="bb-progress" aria-hidden="true" />}
      </Toolbar.Root>
      {data.targets.length >= 2 && <Tabs compact className="bb-tabs" label={text("empty_title")} value={data.active ?? ""} onChange={signal}
        tabs={data.targets.map(target => ({ value: target.target_id, label: <span title={target.url}>{target.title || target.url}</span> }))} />}
      {data.active === null && <main className="bb-main"><EmptyState className="bb-empty" icon="globe" title={text("empty_title")}>{text("empty_body")}</EmptyState></main>}
    </div>
  );
}
