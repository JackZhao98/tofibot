// Local synthetic fixture server: no backend proxy, account or provider calls.
import {createServer} from "vite";
import react from "@vitejs/plugin-react";
import {readFile, writeFile} from "node:fs/promises";
import {fileURLToPath} from "node:url";
import {dirname, join} from "node:path";
import {scenario, question, run, at} from "./task-issue-fixtures.mjs";
const ui=dirname(dirname(fileURLToPath(import.meta.url)));
const calls=[];const answers=new Map();
const plugin={name:"synthetic-task-issue-fixture",enforce:"pre",transform(source,id){if(id.split("?")[0]===join(ui,"src/v2-foundations.css"))return source.replace(/^@import url\([^\n]+\);\n/m,"");},configureServer(server){server.middlewares.use(async(req,res,next)=>{
 const url=new URL(req.url,"http://127.0.0.1");
 res.setHeader("Content-Security-Policy","default-src 'self' data: blob:; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'; font-src 'self' data:; img-src 'self' data: blob:");
 if(url.pathname==="/b/synthetic-bot") {const raw=await readFile(join(ui,"fixtures/task-issue-ux.html"),"utf8");res.setHeader("Content-Type","text/html");res.end(await server.transformIndexHtml(url.pathname,raw));return;}
 if(url.pathname==="/gallery") {const width=Number(url.searchParams.get("width")??390), name=url.searchParams.get("case")??"incident",lang=url.searchParams.get("lang")??"zh",theme=url.searchParams.get("theme")??"light",scale=url.searchParams.get("scale")??"100";res.setHeader("Content-Type","text/html");res.end(`<html><title>Synthetic viewport ${width}</title><body style="margin:0;background:#777"><iframe title="Synthetic ${width}px viewport" style="border:0;width:${width}px;height:900px" src="/b/synthetic-bot?case=${name}&lang=${lang}&theme=${theme}&scale=${scale}"></iframe></body></html>`);return;}
 if(!url.pathname.startsWith("/api/"))return next();
 const fixtureCase=url.searchParams.get("fixture_case")??"incident",f=scenario(fixtureCase);calls.push({method:req.method,path:url.pathname,case:fixtureCase});
 await writeFile(join(ui,"../review-evidence/browser-requests.json"),JSON.stringify(calls,null,2));
 const reply=(body,status=200)=>{res.statusCode=status;res.setHeader("Content-Type","application/json");res.end(JSON.stringify(body));};
 const key=fixtureCase+":"+question.question_id;
 if(req.method==="POST"&&url.pathname===`/api/questions/${question.question_id}/answer`&&fixtureCase==="approval"){let raw="";for await(const part of req)raw+=part;const value=JSON.parse(raw).value;const answered={...question,status:"answered",answer:value,answered_by:"synthetic-human",updated_at:"2026-10-05T10:00:01Z"};answers.set(key,answered);return reply({question:answered});}
 if(req.method==="POST"&&url.pathname.endsWith("/read"))return reply({read_seq:2});
 if(req.method!=="GET")return reply({error:{code:"synthetic_write_forbidden",message:"Fixture forbids this write"}},405);
 if(url.pathname==="/api/server-info")return reply({service:"tofi",protocol_version:1,instance_id:"synthetic-instance",auth:{mode:"none"}});
 if(url.pathname==="/api/preferences")return reply({timezone:"UTC",timezone_configured:true});
 if(url.pathname==="/api/config")return reply({model_configured:true,default_model:"synthetic-model",provider:"synthetic"});
 if(url.pathname==="/api/bots")return reply({bots:[{id:run.bot_id,name:"Synthetic Bot",instructions:"Synthetic role",model:"synthetic-model",reasoning_effort:"medium",dm_conversation_id:run.conversation_id,created_at:at}]});
 if(url.pathname==="/api/conversations")return reply({conversations:[{id:run.conversation_id,kind:"dm",name:"Synthetic Bot",bot_id:run.bot_id,bot_ids:[run.bot_id],updated_at:at,read_seq:2,user_visible:true,task_state:{status:fixtureCase==="done"?"completed":f.run.status==="failed"?"failed":fixtureCase==="approval"?"needs_attention":"executing",run_id:run.id,can_retry:true}}]});
 if(url.pathname==="/api/questions")return reply({questions:answers.has(key)?[answers.get(key)]:f.questions});
 if(url.pathname==="/api/mail-drafts")return reply({drafts:f.drafts});
 if(url.pathname==="/api/secret-inputs")return reply({requests:[]});
 if(url.pathname==="/api/computer/desktop-ownership")return reply({owner:null,waiting:[]});
 if(url.pathname==="/api/computers/firecracker/info")return reply({kind:"synthetic",state:"ready"});
 if(url.pathname.endsWith("/messages"))return reply({messages:f.messages,drafts:[],has_more:false,event_cursor:0});
 if(url.pathname.endsWith("/runs"))return reply({runs:[f.run]});
 if(url.pathname.endsWith("/memories"))return reply({memories:[]});
 if(url.pathname.endsWith("/tools"))return url.searchParams.has("run_ids")?reply({summaries:f.summaries}):url.searchParams.has("run_id")?reply({activities:f.tools,tool_count:fixtureCase==="busy"?10:f.tools.length,has_more:fixtureCase==="busy"}):reply({activities:f.tools});
 if(url.pathname.endsWith("/schedules"))return reply({schedules:[]});
 return reply({error:{code:"unexpected_fixture_read",message:"Unexpected synthetic request"}},404);
 });}};
const server=await createServer({configFile:false,root:ui,plugins:[plugin,react()],server:{host:"127.0.0.1",port:4178,strictPort:true,hmr:false},logLevel:"error"});await server.listen();console.log("Synthetic task UX fixture: http://127.0.0.1:4178/b/synthetic-bot?case=incident");
