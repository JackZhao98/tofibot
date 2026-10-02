import {createContext, useContext, useEffect, useId, useRef} from "react";

export type SettingsDraft = {page:string; label:string; dirty:boolean; busy:boolean; save:()=>Promise<boolean>; discard:()=>void};
type DraftContext = {page:string; register:(id:string, draft:SettingsDraft|null)=>void};
export const SettingsDraftContext = createContext<DraftContext|null>(null);

export function useSettingsDraft(draft:Omit<SettingsDraft,"page">) {
 const context=useContext(SettingsDraftContext);
 const id=useId();
 const latest=useRef(draft);latest.current=draft;
 const register=context?.register;
 const page=context?.page;
 useEffect(()=>{
  if(!register||!page)return;
  register(id,{page,label:draft.label,dirty:draft.dirty,busy:draft.busy,save:()=>latest.current.save(),discard:()=>latest.current.discard()});
 },[register,page,id,draft.label,draft.dirty,draft.busy]);
 useEffect(()=>()=>{register?.(id,null)},[register,id]);
}
