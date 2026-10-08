// Synthetic only. None of these fixtures contain real people, accounts or tool data.
export const at = "2026-10-05T10:00:00Z";
export const run = {id:"synthetic-run-1",conversation_id:"synthetic-chat",bot_id:"synthetic-bot",status:"running",trigger_message_id:"synthetic-request",model:"codex-gpt-6-sol",created_at:at,updated_at:at};
export const request = {id:"synthetic-request",conversation_id:run.conversation_id,role:"user",content:"测试一下邮件工具。",seq:1,created_at:at};
export const tool = {conversation_id:run.conversation_id,bot_id:run.bot_id,run_id:run.id,call_id:"synthetic-call-1",name:"mcp_email_read",arguments:'{"synthetic":true}',result:"",status:"failed",truncated:false,started_at:at,updated_at:at,outcome:{code:"mcp_review_context_missing",status:"context_required",execution_certainty:"not_executed",message:"Synthetic context gap",next_action:"provide_context"}};
export const question = {question_id:"synthetic-question-1",conversation_id:run.conversation_id,bot_id:run.bot_id,run_id:run.id,type:"question",question_type:"approval",question:"Approve this synthetic operation?",status:"pending",created_at:at,updated_at:at,approval:{action:"Read synthetic data",target:"Synthetic fixture",impact:"Read one synthetic record",review:{source:"auto-review",status:"human_required",reason:"PRIVATE_REASONING_DO_NOT_COPY",model:"synthetic-review",policy_version:"mcp-all-external-v5"}}};
export const draft = {draft_id:"synthetic-draft-1",conversation_id:run.conversation_id,bot_id:run.bot_id,run_id:run.id,to:"synthetic@example.invalid",subject:"Synthetic subject",body:"Synthetic body",demo:false,status:"unknown",revision:1,created_at:at,updated_at:at};
export const summary = {run_id:run.id,bot_id:run.bot_id,tool_count:1,completed_count:0,failed_count:1,interrupted_count:0,pending_count:0,started_at:at,updated_at:at};
export function scenario(name) {
  const context = {...question,status:"cancelled",approval:{...question.approval,review:{...question.approval.review,status:"context_required",context_failure:{code:"intent_provenance_unverified"}}},outcome:{status:"context_required",execution_certainty:"not_executed",message:"PRIVATE_BODY",next_action:"provide_context"}};
  const failed = {...run,status:"failed",error:"main-model server_is_overloaded PRIVATE_SECRET",failure:{code:"execution_failed",source:"runtime",message:"PRIVATE_BODY"}};
  const result = {run:{...run},messages:[request],tools:[],questions:[],drafts:[],summaries:[],connected:true};
  if(name==="incident") Object.assign(result,{run:failed,tools:[tool],questions:[context],summaries:[summary]});
  if(name==="busy") Object.assign(result,{run:failed,tools:[{...tool,status:"completed",outcome:undefined,result:"Synthetic confirmed read result"}],summaries:[{...summary,tool_count:10,completed_count:1}]});
  if(name==="setup") Object.assign(result,{tools:[{...tool,outcome:{...tool.outcome,code:"mcp_review_setup_missing",status:"setup_required"}}],questions:[{...context,outcome:{...context.outcome,status:"setup_required"},approval:{...context.approval,review:{...context.approval.review,status:"setup_required"}}}]});
  if(name==="approval") Object.assign(result,{questions:[question]});
  if(name==="shadow") Object.assign(result,{run:{...run,status:"done"},questions:[{...question,status:"run_done",approval:{...question.approval,review_only:true,review:{...question.approval.review,status:"shadow_allow"}}}]});
  if(name==="expired") Object.assign(result,{run:{...run,status:"cancelled",stop_reason:"approval_expired"},questions:[{...question,status:"expired",outcome:{status:"approval_expired",execution_certainty:"not_executed",message:"Synthetic expiry",next_action:"none"}}]});
  if(name==="unknown") Object.assign(result,{run:failed,tools:[{...tool,outcome:{...tool.outcome,code:"mcp_result_unknown",status:"uncertain_effect",execution_certainty:"unknown"}}],drafts:[draft,{...draft,draft_id:"synthetic-draft-confirmed",status:"sent"}]});
  if(name==="disconnected") result.connected=false;
  if(name==="done") Object.assign(result,{run:{...run,status:"done"},tools:[{...tool,status:"completed",outcome:undefined,result:"Synthetic confirmed read result"}],messages:[request,{id:"synthetic-answer",conversation_id:run.conversation_id,run_id:run.id,sender_bot_id:run.bot_id,role:"assistant",seq:2,created_at:at,content:"已收到读取结果。这次合成测试没有发送邮件。"}]});
  return result;
}
