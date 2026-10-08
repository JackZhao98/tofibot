import { useRef, useState, useEffect } from "react";
import { ApprovalCard, type ApprovalResolution } from "../../../ApprovalCard";
import { BotAvatar } from "../../../BotAvatar";
import { TofiIcon } from "../../../icons";
import { MailDraftCard, type MailDraft } from "../../../MailDraftCard";
import { normalizeConfig } from "../../../lib/tofi-avatar/index.js";
import "./approval-demo.css";

const cat = normalizeConfig({ shape:"curl", pattern:"calico", palette:"calico" });
const ops = normalizeConfig({ shape:"tall", pattern:"tabby", palette:"tabby" });
const sampleDraft: MailDraft = { draft_id:"lab-mail-draft", conversation_id:"lab", bot_id:"lab-approval-mail", run_id:"lab", to:"lin@acme.co, mei@acme.co, tao@acme.co", subject:"9 月份价格调整通知", body:"你好，\n\n9 月份的服务价格将按我们已确认的方案调整。具体明细请参考报价单；如果有疑问，请直接回复这封邮件。\n\n谢谢。", demo:true, status:"pending", revision:1, created_at:"2026-09-29T14:20:00-07:00", updated_at:"2026-09-29T14:20:00-07:00" };
const samplePayload = JSON.stringify({ target:"synthetic-weekly-report", sections:["进展", "风险", "下周计划"], note:"这是一段多行合成参数。\n".repeat(55), literal:"<img src=x onerror=alert(1)>" }, null, 2);
export function ApprovalDemo() {
  const [resolution, setResolution] = useState<ApprovalResolution>();
  const [busy, setBusy] = useState<"accept" | "decline">();
  const [draft, setDraft] = useState(false);
  const [restart, setRestart] = useState<ApprovalResolution>();
  const [payloadResolution, setPayloadResolution] = useState<ApprovalResolution>();
  const [groupPreview, setGroupPreview] = useState(false);
  const [demoDraft, setDemoDraft] = useState<MailDraft>(sampleDraft);
  const timer = useRef<number | undefined>(undefined);
  const pending = useRef(false);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const answer = (accepted: boolean) => {
    if (pending.current || resolution) return;
    pending.current = true; setBusy(accepted ? "accept" : "decline");
    timer.current = window.setTimeout(() => {
      setResolution({ label:accepted ? "已批准" : "未批准", answer:accepted ? "发送邮件" : "先别发", accepted });
      pending.current = false; setBusy(undefined); setDraft(false);
    }, 650);
  };
  return <div className="approval-demo">
    <div className="stage approval-stage">
      <ApprovalCard title="要把这封邮件发给 3 位客户吗？" avatar={<BotAvatar id="lab-approval-mail" config={cat} mini />} time="14:20"
        facts={[{ label:"动作", value:"发送邮件「9 月份价格调整通知」" }, { label:"对象", value:"lin@acme.co 等 3 人" }, { label:"影响", value:"发出后不能撤回" }]}
        acceptLabel="发送" declineLabel="先别发" onAnswer={answer} busy={busy} resolution={resolution}
        secondaryAction={<button type="button" aria-expanded={draft} disabled={Boolean(busy)} onClick={() => setDraft(!draft)}>看草稿</button>} />
      {draft && !resolution && <div className="approval-draft-preview"><strong>9 月份价格调整通知</strong><p>你好，9 月份的服务价格将按我们已确认的方案调整，具体明细请参考报价单。</p></div>}
      <ApprovalCard title="允许 Ops Watcher 重启 tofi-vm-01 吗？" avatar={<BotAvatar id="lab-approval-ops" config={ops} mini />}
        facts={[{ label:"原因", value:"内存占用 97%，服务已无响应 3 分钟" }, { label:"停机", value:"约 40 秒" }]}
        acceptLabel="重启" declineLabel="再等等" resolution={restart} onAnswer={accepted => setRestart({ label:accepted ? "已批准" : "未批准", answer:accepted ? "重启" : "再等等", accepted })} />
      <ApprovalCard title="允许调用外部工具处理这份周报吗？" facts={[{ label:"动作", value:"调用 update_report" }, { label:"对象", value:"synthetic-workspace" }, { label:"影响", value:"合成演示；请展开核对完整参数" }]}
        payload={samplePayload} resolution={payloadResolution} onAnswer={accepted => setPayloadResolution({ label:accepted ? "已批准" : "未批准", accepted })} />
      <div className="approval-production-preview"><div className="approval-production-head"><strong>生产组件 · 邮件草稿</strong><button type="button" className="lab-btn" onClick={() => setGroupPreview(value => !value)}>{groupPreview ? "切换到私信" : "切换到群聊"}</button></div><div className="workspace" style={{ height:"auto", overflow:"visible", display:"block" }}><MailDraftCard draft={demoDraft} group={groupPreview} onChanged={async () => {}} onDemoAction={async (action, fields) => setDemoDraft(current => ({ ...current, ...fields, revision: action === "save" ? current.revision + 1 : current.revision, status: action === "send" ? "sent" : action === "decline" ? "declined" : "pending" }))} /></div><button type="button" className="lab-btn" onClick={() => setDemoDraft(sampleDraft)}>重播邮件草稿</button></div>
    </div>
    <div className="stage-controls"><button type="button" className="lab-btn" onClick={() => { window.clearTimeout(timer.current); pending.current = false; setBusy(undefined); setResolution(undefined); setRestart(undefined); setPayloadResolution(undefined); setDraft(false); }}><TofiIcon name="retry" size={16} />重播</button><span className="approval-demo-note">交互演示，不会发送邮件、调用外部工具或重启电脑。</span></div>
  </div>;
}
