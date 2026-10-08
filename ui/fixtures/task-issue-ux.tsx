import React from "react";
import { createRoot } from "react-dom/client";
import App from "../src/App";
import "../src/styles.css";
import "../src/interaction-system.css";
import "../src/settings-system.css";
import "../src/desktop-system.css";
import "../src/conversation-workspace.css";
import "../src/v2-foundations.css";
import "../src/v2-app.css";
import "../src/web-tool-steps.css";
import "../src/web-sidebar-list.css";
import "../src/chat-header.css";
import "../src/context-card.css";
const params = new URLSearchParams(location.search);
document.documentElement.lang = params.get("lang") === "en" ? "en" : "zh-CN";
localStorage.setItem("tofi:appearance", params.get("theme") === "dark" ? "dark" : "light");
if (params.get("scale") === "200") {
  document.documentElement.style.setProperty("--type-body", "400 30px/1.6 var(--body)");
  document.documentElement.style.setProperty("--type-row-title", "600 28px/1.4 var(--body)");
  document.documentElement.style.setProperty("--type-meta", "400 26px/1.5 var(--body)");
  document.documentElement.style.setProperty("--type-button", "600 28px/1 var(--body)");
}
if (params.get("copy") === "fail") Object.defineProperty(navigator, "clipboard", {configurable:true,value:{writeText:async()=>{throw new Error("Synthetic clipboard failure");}}});
const fixtureCase = params.get("case") || "incident";
const originalFetch = window.fetch.bind(window);
const calls: {method:string;path:string}[] = [];
(window as any).syntheticCalls = calls;
window.fetch = async (input, init) => {
  const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, location.origin);
  if (url.origin !== location.origin || !url.pathname.startsWith("/api/")) throw new Error("Synthetic fixture forbids external requests");
  url.searchParams.set("fixture_case", fixtureCase);
  calls.push({method:init?.method ?? "GET",path:url.pathname});
  return originalFetch(url, init);
};
// No real stream is opened. Events can be dispatched by the fixture's own UI.
const sources: FixtureEvents[] = [];
class FixtureEvents {
  static OPEN=1; static CLOSED=2; readyState=1;
  onopen?:()=>void; onerror?:()=>void;
  listeners = new Map<string, ((event:MessageEvent)=>void)[]>();
  constructor(){sources.push(this);setTimeout(()=>fixtureCase === "disconnected" ? this.onerror?.() : this.onopen?.(),0);}
  addEventListener(type:string, handler:(event:MessageEvent)=>void){this.listeners.set(type,[...(this.listeners.get(type)??[]),handler]);}
  close(){this.readyState=2;}
  emit(type:string, value:unknown, id:number){for(const handler of this.listeners.get(type)??[])handler({data:JSON.stringify(value),lastEventId:String(id)} as MessageEvent);}
}
window.EventSource = FixtureEvents as any;
window.MediaSource = undefined as any;
if (fixtureCase === "disconnected") setTimeout(()=>sources.forEach(source=>source.onerror?.()),2000);
createRoot(document.getElementById("root")!).render(<App />);
