import {useSyncExternalStore} from "react";
const key="tofi:debug-mode";
const event="tofi-debug-change";
function snapshot(){try{return localStorage.getItem(key)==="true"}catch{return false}}
function subscribe(listener:()=>void){window.addEventListener(event,listener);window.addEventListener("storage",listener);return()=>{window.removeEventListener(event,listener);window.removeEventListener("storage",listener)}}
export function useDebugMode(){return useSyncExternalStore(subscribe,snapshot,()=>false)}
export function setDebugMode(value:boolean){try{localStorage.setItem(key,String(value))}catch{/* Storage may be disabled. */}window.dispatchEvent(new Event(event))}
