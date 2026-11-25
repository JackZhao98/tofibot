import {useEffect,useRef} from "react";
import {Terminal} from "@xterm/xterm";
import {FitAddon} from "@xterm/addon-fit";
import "./vendor/xterm.css";
import {i18n,useTranslation} from "./i18n";

export type TerminalChunk={data_base64:string;next_cursor:number;gap?:boolean;closed?:boolean;drained?:boolean};
type Props={sessionId:string;controlled:boolean;read:(cursor:number,signal:AbortSignal)=>Promise<TerminalChunk>;onInput:(data:string)=>void;onResize:(cols:number,rows:number)=>void;onError:(message:string)=>void};
/** The skin's mono face; xterm needs a concrete list, so the custom property is read, not referenced. */
function readTerminalFont(){return getComputedStyle(document.documentElement).getPropertyValue("--font-mono").trim()||'ui-monospace, "SFMono-Regular", Consolas, monospace'}
function readTerminalTheme(){
 const style=getComputedStyle(document.documentElement);
 const color=(name:string)=>style.getPropertyValue(name).trim();
 const palette={red:color("--danger-text"),green:color("--lime-text"),yellow:color("--mustard-text"),blue:color("--focus"),magenta:color("--grape-text"),cyan:color("--teal-text")};
 return {...palette,brightRed:palette.red,brightGreen:palette.green,brightYellow:palette.yellow,brightBlue:palette.blue,brightMagenta:palette.magenta,brightCyan:palette.cyan,background:color("--sunk"),foreground:color("--ink"),cursor:color("--ink"),cursorAccent:color("--sunk"),selectionBackground:color("--hover"),black:color("--ink"),brightBlack:color("--ink-soft"),white:color("--ink"),brightWhite:color("--ink")};
}
export function TerminalScreen(props:Props){
 const {t}=useTranslation("computer");
 const host=useRef<HTMLDivElement>(null);const terminal=useRef<Terminal|null>(null);const latest=useRef(props);latest.current=props;
 useEffect(()=>{
  if(!host.current)return;
  const term=new Terminal({fontFamily:readTerminalFont(),fontSize:13,lineHeight:1.3,cursorBlink:false,disableStdin:!latest.current.controlled,scrollback:4000,theme:readTerminalTheme()});
  const fit=new FitAddon();term.loadAddon(fit);term.open(host.current);terminal.current=term;
  const syncTheme=()=>{term.options.theme=readTerminalTheme();term.options.fontFamily=readTerminalFont()};
  // The skin's face is a web font: cell size is measured again once it has loaded.
  void document.fonts?.ready.then(()=>{if(host.current){syncTheme();fit.fit()}});
  const themeObserver=new MutationObserver(syncTheme);
  themeObserver.observe(document.documentElement,{attributes:true,attributeFilter:["data-theme","data-skin"]});
  const resize=()=>{fit.fit();if(latest.current.controlled)latest.current.onResize(term.cols,term.rows)};
  const observer=new ResizeObserver(resize);observer.observe(host.current);resize();
  const subscription=term.onData(data=>{if(latest.current.controlled)latest.current.onInput(data)});
  const controller=new AbortController();let cursor=0;let stopped=false;let timer:ReturnType<typeof setTimeout>;let failures=0;
  async function poll(){
   try{const chunk=await latest.current.read(cursor,controller.signal);if(stopped)return;
    if(chunk.gap){term.reset();term.writeln(`\x1b[90m${i18n.t("computer:terminal.trimmed")}\x1b[0m`)}
    if(chunk.data_base64){const data=Uint8Array.from(atob(chunk.data_base64),c=>c.charCodeAt(0));await new Promise<void>(resolve=>term.write(data,resolve))}
    cursor=chunk.next_cursor;if(failures>=3)latest.current.onError("");failures=0;if(chunk.drained)return;
   }catch{if(stopped)return;if(++failures===3)latest.current.onError(i18n.t("computer:terminal.reconnecting"))}
   if(!stopped)timer=setTimeout(poll,failures?1500:80);
  }
  void poll();return()=>{stopped=true;controller.abort();clearTimeout(timer);observer.disconnect();themeObserver.disconnect();subscription.dispose();term.dispose();terminal.current=null};
 },[props.sessionId]);
 useEffect(()=>{const term=terminal.current;if(!term)return;term.options.disableStdin=!props.controlled;term.options.cursorBlink=props.controlled;if(props.controlled){latest.current.onResize(term.cols,term.rows);term.focus()}},[props.controlled]);
 return <div className="terminal-screen" ref={host} aria-label={t("terminal.live_aria")}/>;
}
