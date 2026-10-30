import {type CSSProperties} from "react";
import {useTranslation} from "./i18n";
import "./computer-resources.css";

export type Allocation={vcpus:number;memory_mib:number;disk_gib:number};
export type Resources={current:Allocation;desired:Allocation;pending:boolean;state:string;error?:string;host:{cpus:number;memory_total_mib:number;memory_available_mib:number;disk_available_gib:number};limits:{max_vcpus:number;max_memory_mib:number;max_disk_gib:number}};

const GB=1024;
type FieldKey=keyof Allocation;
export function validAllocation(a:Allocation,data:Resources){return [a.vcpus,a.memory_mib,a.disk_gib].every(Number.isFinite)&&Number.isInteger(a.vcpus)&&a.vcpus>=1&&a.vcpus<=data.limits.max_vcpus&&a.memory_mib>=512&&a.memory_mib<=data.limits.max_memory_mib&&a.disk_gib>=data.current.disk_gib&&a.disk_gib<=data.limits.max_disk_gib}
export const gbText=(mib:number)=>String(Number((mib/GB).toFixed(1)));

/** One resource: current value, the new value, a slider with -/+ steppers, and what the host can give. */
function ResourceField({id,label,unit,current,value,min,max,step,hint,disabled,format,onChange}:{id:string;label:string;unit:string;current:number;value:number;min:number;max:number;step:number;hint:string;disabled:boolean;format:(n:number)=>string;onChange:(n:number)=>void}) {
 const {t}=useTranslation("computer");
 const clamp=(n:number)=>Math.min(max,Math.max(min,n));
 const changed=value!==current;
 const pct=max>min?((value-min)/(max-min))*100:100;
 return <div className={`resource-field${changed?" is-changed":""}`} role="group" aria-labelledby={id+"-label"}>
  <div className="resource-field-head">
   <span id={id+"-label"} className="resource-field-label">{label}</span>
   <span className="resource-field-values"><span className="resource-field-was">{format(current)}</span>{changed&&<><span aria-hidden="true" className="resource-field-arrow">→</span><strong className="resource-field-now">{format(value)}</strong></>}<small>{unit}</small></span>
  </div>
  <div className="resource-field-control">
   <button type="button" className="resource-step" aria-label={t("resources.decrease",{field:label})} disabled={disabled||value<=min} onClick={()=>onChange(clamp(value-step))}>−</button>
   <input id={id} type="range" aria-labelledby={id+"-label"} min={min} max={max} step={step} value={value} disabled={disabled||max<=min} style={{"--fill":pct+"%"} as CSSProperties} onChange={event=>onChange(clamp(Number(event.target.value)))}/>
   <button type="button" className="resource-step" aria-label={t("resources.increase",{field:label})} disabled={disabled||value>=max} onClick={()=>onChange(clamp(value+step))}>+</button>
  </div>
  <div className="resource-field-foot"><span>{format(min)}</span><span className="resource-field-hint">{hint}</span><span>{format(max)}</span></div>
 </div>;
}

export function ResourceSheet({data,desired,dirty,busy,onChange,onSave,onCancel,flush=false}:{flush?:boolean;data:Resources;desired:Allocation;dirty:boolean;busy:boolean;onChange:(a:Allocation)=>void;onSave:()=>void;onCancel:()=>void}) {
 const {t}=useTranslation("computer");
 const valid=validAllocation(desired,data);
 const set=(key:FieldKey)=>(n:number)=>onChange({...desired,[key]:n});
 const lines:string[]=[];
 if(desired.vcpus!==data.desired.vcpus)lines.push(`CPU ${data.desired.vcpus} → ${desired.vcpus} ${t("resources.unit.cores")}`);
 if(desired.memory_mib!==data.desired.memory_mib)lines.push(`${t("resources.memory")} ${gbText(data.desired.memory_mib)} → ${gbText(desired.memory_mib)} GB`);
 if(desired.disk_gib!==data.desired.disk_gib)lines.push(`${t("resources.disk_allocated")} ${data.desired.disk_gib} → ${desired.disk_gib} GB`);
 return <form aria-busy={busy} className={flush?"resource-sheet is-flush":"resource-sheet"} onSubmit={event=>{event.preventDefault();if(valid&&dirty&&!busy)onSave()}}>
  <fieldset className="resource-fields" disabled={busy}>
   <ResourceField id="resource-cpus" label={t("resources.field.cpus")} unit={t("resources.unit.cores")} current={data.desired.vcpus} value={desired.vcpus} min={1} max={data.limits.max_vcpus} step={1} format={String} disabled={busy} hint={t("resources.host_cpus",{cpus:data.host.cpus})} onChange={set("vcpus")}/>
   <ResourceField id="resource-memory" label={t("resources.field.memory")} unit="GB" current={data.desired.memory_mib} value={desired.memory_mib} min={512} max={Math.max(512,data.limits.max_memory_mib)} step={512} format={gbText} disabled={busy} hint={t("resources.host_memory",{value:gbText(data.host.memory_available_mib)})} onChange={set("memory_mib")}/>
   <ResourceField id="resource-disk" label={t("resources.field.disk")} unit="GB" current={data.desired.disk_gib} value={desired.disk_gib} min={data.current.disk_gib} max={Math.max(data.current.disk_gib,data.limits.max_disk_gib)} step={1} format={String} disabled={busy} hint={t("resources.host_disk",{value:Math.floor(data.host.disk_available_gib)})} onChange={set("disk_gib")}/>
  </fieldset>
  <div className="resource-summary" aria-live="polite">
   {!valid?<p className="resource-summary-error" role="alert">{t("resources.error.out_of_range")}</p>:dirty?<><strong>{t("resources.summary.title")}</strong><ul>{lines.map(line=><li key={line}>{line}</li>)}</ul><p>{t("resources.summary.restart")}</p></>:<p>{t("resources.summary.none")}</p>}
   <p className="resource-summary-note">{t("resources.field_note")}</p>
  </div>
  <div className="resource-sheet-actions"><button type="button" className="secondary-button" disabled={busy} onClick={onCancel}>{t("resources.cancel")}</button><button type="submit" className="primary-button" disabled={busy||!valid||!dirty}>{busy?t("resources.saving"):t("resources.save")}</button></div>
 </form>;
}

