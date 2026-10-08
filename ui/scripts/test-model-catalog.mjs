import assert from "node:assert/strict";
import {openUiModules} from "./ui-modules.mjs";
// Labels are asserted in zh-CN (the shipped Chinese copy), then once in English.
const ui=await openUiModules({language:"zh-CN"});
try {
 const m=await ui.load("/src/modelCatalog.ts");
 // 1. Routing mirrors the server rule.
 for(const [id,provider] of [["codex-gpt-6-luna","codex"],["codex-auto-review","codex"],["claude-opus-5-5","anthropic"],["claude-haiku-4-5-20251001","anthropic"],["gpt-6-luna","openai"],["o4-mini","openai"],["","openai"]]) assert.equal(m.providerForModel(id),provider,id);
 console.log("PASS 1: provider routing by id");
 // 2. Grouping: Codex, OpenAI, Claude order regardless of input order; server order kept inside a group; duplicates dropped.
 const models=[
  {id:"claude-sonnet-5-5",name:"Claude Sonnet 5.5",provider:"anthropic",reasoning_efforts:["low","medium","high","xhigh","max"],default_reasoning:"medium"},
  {id:"gpt-6-luna",name:"GPT-6 Luna",provider:"openai",reasoning_efforts:["low","medium","high"],default_reasoning:"medium"},
  {id:"codex-gpt-6-sol",name:"Codex · GPT-6 Sol",provider:"codex",reasoning_efforts:["low","medium","high","xhigh"],default_reasoning:"high"},
  {id:"codex-gpt-6-luna",name:"GPT-6 Luna",reasoning_efforts:["medium"],default_reasoning:"medium"},
  {id:"gpt-4.1",name:"GPT-4.1",provider:"openai",reasoning_efforts:[],default_reasoning:""},
  {id:"claude-opus-5-5",name:"",provider:"anthropic",reasoning_efforts:["high"],default_reasoning:"high"},
  {id:"gpt-6-luna",name:"duplicate",provider:"openai",reasoning_efforts:[],default_reasoning:""},
 ];
 const groups=m.groupModels(models);
 assert.deepEqual(groups.map(g=>[g.provider,g.label]),[["codex","Codex"],["openai","OpenAI"],["anthropic","Claude"]]);
 assert.deepEqual(groups[0].models.map(x=>x.label),["Codex · GPT-6 Sol","Codex · GPT-6 Luna"],"bare Codex names gain the prefix once");
 assert.deepEqual(groups[1].models.map(x=>x.id),["gpt-6-luna","gpt-4.1"]);assert.equal(groups[1].models[0].label,"GPT-6 Luna");
 assert.deepEqual(groups[2].models.map(x=>x.label),["Claude Sonnet 5.5","claude-opus-5-5"],"empty name falls back to id");
 assert.deepEqual(m.groupModels([{id:"gpt-6-luna",name:"GPT-6 Luna",reasoning_efforts:[],default_reasoning:""}]).map(g=>g.provider),["openai"],"missing groups are omitted");
 assert.deepEqual(m.groupModels([]),[]);
 console.log("PASS 2: grouped by provider in Codex/OpenAI/Claude order with display names");
 // 3. Effort follows the selected model.
 const [claude,openai,codex,,plain]=models;
 assert.deepEqual(m.modelEfforts(plain),[]);assert.deepEqual(m.modelEfforts(undefined),[]);assert.deepEqual(m.modelEfforts(claude),["low","medium","high","xhigh","max"]);
 assert.equal(m.effortForModel(claude,"max"),"max");assert.equal(m.effortForModel(openai,"max"),"medium","unsupported effort falls back to default");
 assert.equal(m.effortForModel(codex,""),"high");assert.equal(m.effortForModel(plain,"high"),"","non-reasoning models carry no effort");assert.equal(m.effortForModel(undefined,"high"),"");
 console.log("PASS 3: effort options and carry-over follow the model");
 // 4. Bot packages keep any provider's model id.
 const pkg=await ui.load("/src/botPackage.ts");
 for(const model of ["claude-opus-5-5","gpt-6-luna","codex-gpt-6-luna",""]){
  const parsed=pkg.parseBotPackage(JSON.stringify({format:"tofi.bot",version:1,included:["bot_config"],bot:{name:"Synthetic",instructions:"",model,reasoning_effort:model?"high":undefined}}));
  assert.equal(parsed.bot.model,model);
 }
 console.log("PASS 4: bot package import keeps Codex, OpenAI and Claude model ids");
 // 5. Follow-global: "default" (and legacy "") follow; pinning keeps a supported effort else defers to the model default.
 assert.equal(m.FOLLOW_GLOBAL,"default");
 assert.equal(m.followsGlobal("default"),true);assert.equal(m.followsGlobal(""),true);assert.equal(m.followsGlobal(undefined),true);assert.equal(m.followsGlobal("codex-gpt-6-luna"),false);
 assert.equal(m.effortForPinnedModel(claude,"max"),"max");assert.equal(m.effortForPinnedModel(openai,"max"),"default");assert.equal(m.effortForPinnedModel(claude,"default"),"default");assert.equal(m.effortForPinnedModel(plain,"high"),"default");
 const labels={low:"低 · 更快",medium:"中 · 均衡",high:"高 · 深入"};
 assert.equal(m.followGlobalLabel(models,{model:"codex-gpt-6-luna",reasoning_effort:"medium"},labels),"跟随全局（当前：Codex · GPT-6 Luna · 中）");
 assert.equal(m.followGlobalLabel(models,{model:"retired-model",reasoning_effort:""},labels),"跟随全局（当前：retired-model）");
 assert.equal(m.followGlobalLabel(models,null,labels),"跟随全局");
 const settings=await ui.load("/src/ModelSettings.tsx");
 assert.equal(settings.effortLabels().medium,"中 · 均衡");assert.equal(m.shortEffortLabel("medium",settings.effortLabels()),"中");
 await ui.setLanguage("en");
 assert.equal(m.followGlobalLabel(models,{model:"codex-gpt-6-luna",reasoning_effort:"medium"},settings.effortLabels()),"Follow global (now: Codex · GPT-6 Luna · Medium)");
 assert.equal(m.followGlobalLabel(models,null,labels),"Follow global");
 await ui.setLanguage("zh-CN");
 const followed=pkg.parseBotPackage(JSON.stringify({format:"tofi.bot",version:1,included:["bot_config"],bot:{name:"Follower",instructions:"",model:"default",reasoning_effort:"default"}}));
 assert.equal(followed.bot.model,"default");assert.equal(followed.bot.reasoning_effort,"default");
 console.log("PASS 5: follow-global sentinel, pinned effort and label; bot packages carry \"default\" through");
} finally {await ui.close();}
