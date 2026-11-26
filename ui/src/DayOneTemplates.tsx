import {useEffect, useId, useState, type FormEvent} from "react";
import {api, request} from "./api";
import {errorText} from "./i18n/errors";
import {formatClock, formatDateTime} from "./i18n/format";
import {useTranslation} from "./i18n";
import {Select} from "./Select";
import {Button} from "./ds/Button/Button";
import {Card} from "./ds/Card/Card";
import {Field} from "./ds/Field/Field";
import {TextInput} from "./ds/TextInput/TextInput";
import {Banner} from "./ds/Banner/Banner";
import {ScheduleCard} from "./ds/ScheduleCard/ScheduleCard";
import {weeklyCadence} from "./scheduleCadence";
import {useUserTimezone} from "./UserTimezone";
import {linkRuntime} from "./settings/deepLinks";
import type {GoogleStatus} from "./connections/localPlugins";
import type {Schedule} from "./types";
import "./layout/day-one.css";

type Template = "briefing" | "inbox" | "watch";
type Watch = "hourly" | "six_hours" | "daily";
const DISMISSED_KEY = "tofi:day-one-dismissed";
const HOUR_MS = 3_600_000;

function wasDismissed() {
 try { return localStorage.getItem(DISMISSED_KEY) === "1"; } catch { return false; }
}

/** The instructions a template hands the Bot. They are model-facing, so they stay in English whatever the page language. */
export function templateInstructions(template: Template, input: {topics?: string; url?: string; focus?: string}): string {
 if (template === "briefing") return `Prepare today's morning briefing on these topics: ${input.topics}. Search the web for news from the last 24 hours on each topic. Write a short briefing with one heading line per topic, then two to four items, each one sentence with the outlet name and a link. Leave out a topic that has nothing new. Post the briefing in this chat, in the language the person writes to you in.`;
 if (template === "inbox") return `Prepare the daily inbox digest. Read the mail that arrived in the last 24 hours with the Gmail tool. Group it as needs a reply, worth knowing, and everything else (count only). For each message in the first two groups give the sender, the subject and one sentence on why it matters.${input.focus ? ` Pay extra attention to: ${input.focus}.` : ""} Do not send, delete, archive or label any mail. Post the digest in this chat, in the language the person writes to you in.`;
 return `Check the web page ${input.url}. Open it and read its current content. Compare it with the summary you stored in your memory after the previous check of this page. On the first check, store a short summary of the page and say that watching has started. When the page changed, report what was added, removed or edited, quoting the relevant text briefly. When nothing meaningful changed, reply with one short sentence saying so. After every check, store the updated summary of the page in your memory. Reply in the language the person writes to you in.`;
}

/**
 * Day-one templates: three one-click ways to get value on the first day. Each opens a short form and creates an ordinary
 * schedule through the existing schedule API, then shows it. Offered in a Bot's direct chat until the person has written
 * to it, any schedule exists, or the card is dismissed.
 */
export default function DayOneTemplates({conversationId, botId, onOpenTasks}: {conversationId: string; botId: string; /** Opens the Tasks panel, where schedules are listed and edited. */ onOpenTasks?: () => void}) {
 const {t} = useTranslation("schedules");
 const {timezone} = useUserTimezone();
 const [hidden, setHidden] = useState(wasDismissed);
 const [existing, setExisting] = useState<boolean | null>(null);
 const [open, setOpen] = useState<Template | null>(null);
 const [created, setCreated] = useState<Schedule | null>(null);
 const [google, setGoogle] = useState<GoogleStatus | null | undefined>(undefined);
 const [topics, setTopics] = useState("");
 const [focus, setFocus] = useState("");
 const [url, setUrl] = useState("");
 const [watch, setWatch] = useState<Watch>("daily");
 const [time, setTime] = useState("08:00");
 const [busy, setBusy] = useState(false);
 const [error, setError] = useState("");
 const ids = useId();

 useEffect(() => {
  const controller = new AbortController();
  api.schedules(conversationId).then(result => { if (!controller.signal.aborted) setExisting(result.schedules.some(item => item.status !== "deleted")); }).catch(() => { if (!controller.signal.aborted) setExisting(false); });
  return () => controller.abort();
 }, [conversationId]);
 // Gmail is checked only when the person looks at the inbox template.
 useEffect(() => {
  if (open !== "inbox" || google !== undefined) return;
  let alive = true;
  request<GoogleStatus>("/api/extensions/local-mcp/gog/oauth/status").then(status => { if (alive) setGoogle(status); }).catch(() => { if (alive) setGoogle(null); });
  return () => { alive = false; };
 }, [open, google]);

 if (hidden || (existing !== false && !created)) return null;
 const cadence = (schedule: Schedule) => schedule.kind === "daily" ? t("dayone.cadence_daily", {time: schedule.daily_time}) : schedule.kind === "weekly" ? weeklyCadence(schedule.weekdays, schedule.daily_time) : schedule.kind === "interval" ? t("dayone.cadence_hours", {count: Math.max(1, Math.round((schedule.interval_seconds ?? 3600) / 3600))}) : t("dayone.cadence_once");
 const gmailReady = Boolean(google?.connected && !google.reauth_required && (google.services ? google.services.includes("gmail") : true));
 const choose = (template: Template) => {
  setOpen(current => current === template ? null : template);
  setError("");
  setTime(template === "inbox" ? "18:00" : "08:00");
 };
 const dismiss = () => { try { localStorage.setItem(DISMISSED_KEY, "1"); } catch { /* the card simply returns next time */ } setHidden(true); };

 async function submit(event: FormEvent) {
  event.preventDefault();
  if (!open || busy) return;
  setBusy(true); setError("");
  try {
   let input;
   if (open === "watch" && watch !== "daily") {
    const every = watch === "hourly" ? 1 : 6;
    input = {kind: "interval" as const, interval_seconds: every * 3600, run_at: new Date(Date.now() + every * HOUR_MS).toISOString(), timezone};
   } else input = {kind: "daily" as const, daily_time: time, timezone};
   const host = open === "watch" ? hostOf(url) : "";
   const schedule = await api.createSchedule(conversationId, {
    bot_id: botId,
    title: open === "briefing" ? t("dayone.briefing.schedule_title") : open === "inbox" ? t("dayone.inbox.schedule_title") : t("dayone.watch.schedule_title", {host}),
    description: open === "briefing" ? t("dayone.briefing.schedule_description", {topics: topics.trim()}) : open === "inbox" ? t("dayone.inbox.schedule_description") : t("dayone.watch.schedule_description", {url: url.trim()}),
    content: templateInstructions(open, {topics: topics.trim(), url: url.trim(), focus: focus.trim()}),
    ...input,
   });
   setCreated(schedule); setOpen(null);
  } catch (cause) {
   setError(errorText(cause, "schedules", t("dayone.failed")));
  } finally { setBusy(false); }
 }

 if (created) return <section className="day-one" aria-label={t("dayone.title")} data-day-one="created">
  <ScheduleCard state="done" statusLabel={t("dayone.created")} time={formatClock(created.next_at_utc, {timeZone: created.timezone || timezone})} title={created.title} meta={`${cadence(created)} · ${t("dayone.next_run", {time: formatDateTime(created.next_at_utc, {dateStyle: "medium", timeStyle: "short", timeZone: created.timezone || timezone})})}`}/>
  <div className="day-one-actions">{onOpenTasks && <Button variant="ghost" size="sm" onClick={onOpenTasks}>{t("dayone.manage")}</Button>}<Button variant="ghost" size="sm" onClick={() => { setCreated(null); setTopics(""); setUrl(""); setFocus(""); }}>{t("dayone.add_another")}</Button></div>
 </section>;

 return <Card variant="block" className="day-one" aria-label={t("dayone.title")} data-day-one="offer" title={t("dayone.title")} actions={<Button variant="ghost" size="sm" onClick={dismiss}>{t("dayone.dismiss")}</Button>}>
  <p className="day-one-lead">{t("dayone.lead")}</p>
  <div className="day-one-choices" role="group" aria-label={t("dayone.title")}>
   {(["briefing", "inbox", "watch"] as const).map(template => <Button key={template} variant="secondary" className="day-one-choice" data-template={template} aria-pressed={open === template} aria-controls={`${ids}-form`} icon={template === "briefing" ? "sun" : template === "inbox" ? "inbox" : "eye"} onClick={() => choose(template)}>{t(`dayone.${template}.name`)}</Button>)}
  </div>
  {open && <div id={`${ids}-form`} className="day-one-form">
   {open === "inbox" && google === undefined ? <p className="day-one-note" role="status">{t("dayone.checking")}</p>
    : open === "inbox" && !gmailReady ? <div className="day-one-needs" role="status">
     <p>{t("dayone.inbox.needs_gmail")}</p>
     <Button onClick={() => void linkRuntime().then(module => module.openHref("/settings/connections"))}>{t("dayone.inbox.connect")}</Button>
    </div>
    : <form onSubmit={event => void submit(event)} aria-busy={busy}>
     <p className="day-one-note">{t(`dayone.${open}.summary`)}</p>
     {open === "briefing" && <Field label={t("dayone.briefing.topics")}><TextInput name="topics" required maxLength={200} autoComplete="off" value={topics} disabled={busy} placeholder={t("dayone.briefing.topics_placeholder")} onChange={event => setTopics(event.target.value)}/></Field>}
     {open === "inbox" && <Field label={t("dayone.inbox.focus")}><TextInput name="focus" maxLength={200} autoComplete="off" value={focus} disabled={busy} placeholder={t("dayone.inbox.focus_placeholder")} onChange={event => setFocus(event.target.value)}/></Field>}
     {open === "watch" && <>
      <Field label={t("dayone.watch.url")}><TextInput name="url" type="url" required maxLength={500} autoComplete="off" spellCheck={false} placeholder="https://" value={url} disabled={busy} onChange={event => setUrl(event.target.value)}/></Field>
      <Field label={t("dayone.watch.every")}><Select value={watch} disabled={busy} onValueChange={value => setWatch(value as Watch)} options={[{value: "hourly", label: t("dayone.watch.hourly")}, {value: "six_hours", label: t("dayone.watch.six_hours")}, {value: "daily", label: t("dayone.watch.daily")}]}/></Field>
     </>}
     {(open !== "watch" || watch === "daily") && <Field label={open === "inbox" ? t("dayone.inbox.time") : t("dayone.time")}><TextInput name="time" type="time" required value={time} disabled={busy} onChange={event => setTime(event.target.value)}/></Field>}
     <p className="day-one-note">{t("dayone.zone_note", {zone: timezone})}</p>
     {error && <Banner tone="error">{error}</Banner>}
     <div className="day-one-actions"><Button type="submit" variant="primary" busy={busy} disabled={busy || (open === "briefing" && !topics.trim()) || (open === "watch" && !url.trim())}>{busy ? t("dayone.creating") : t("dayone.create")}</Button></div>
    </form>}
  </div>}
 </Card>;
}

function hostOf(value: string) {
 try { return new URL(value.trim()).host; } catch { return value.trim(); }
}
