import { intlLocale } from "./i18n/format";
import { i18n } from "./i18n";
const fallbackZones=["UTC","America/Los_Angeles","America/New_York","Europe/London","Europe/Paris","Asia/Shanghai","Asia/Hong_Kong","Asia/Tokyo","Asia/Singapore","Australia/Sydney"];
export function browserTimezone(){return Intl.DateTimeFormat().resolvedOptions().timeZone||"UTC"}
export function validTimezone(value:string){try{new Intl.DateTimeFormat("en-US",{timeZone:value}).format();return Boolean(value)}catch{return false}}
export function timezoneChoices(current:string){
 const supported=(Intl as typeof Intl&{supportedValuesOf?:(key:string)=>string[]}).supportedValuesOf?.("timeZone")??fallbackZones;
 return [...new Set([current,browserTimezone(),"UTC",...supported])].filter(validTimezone).sort((a,b)=>a.localeCompare(b));
}
export function dateInTimezone(value:string,timeZone:string){
 return new Intl.DateTimeFormat("en-CA",{timeZone,year:"numeric",month:"2-digit",day:"2-digit"}).format(new Date(value));
}
export function formatZonedTime(value:string,timeZone:string){
 if(!value||!Number.isFinite(Date.parse(value)))return i18n.t("common:time.unknown");
 return new Intl.DateTimeFormat(intlLocale(),{timeZone,year:"numeric",month:"2-digit",day:"2-digit",hour:"2-digit",minute:"2-digit",hourCycle:"h23"}).format(new Date(value));
}
