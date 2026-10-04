// Real browser interactions; every API route is a newly created synthetic fixture.
import assert from 'node:assert/strict';
import {mkdtemp,writeFile,rm} from 'node:fs/promises';
import {dirname,join,basename} from 'node:path';
import {fileURLToPath} from 'node:url';
import {randomUUID} from 'node:crypto';
import {chromium} from '@playwright/test';
import {createServer} from 'vite';
import react from '@vitejs/plugin-react';

const root=dirname(dirname(fileURLToPath(import.meta.url)));
const fixture=await mkdtemp(join(root,'.admin-computer-fixture-'));
let server,browser;
try {
 await writeFile(join(fixture,'index.html'),'<div id="root"></div><script type="module" src="./main.tsx"></script>');
 await writeFile(join(fixture,'main.tsx'),`import React from 'react';import {createRoot} from 'react-dom/client';import '../src/styles.css';import '../src/interaction-system.css';import '../src/v2-foundations.css';import {OwnerSessionGate} from '../src/OwnerSession';import {AdminAccounts} from '../src/AdminAccounts';createRoot(document.getElementById('root')!).render(<OwnerSessionGate><AdminAccounts/></OwnerSessionGate>);`);
 server=await createServer({configFile:false,root,plugins:[react()],server:{host:'127.0.0.1',port:0},logLevel:'error'});
 await server.listen();
 const port=server.httpServer.address().port;
 browser=await chromium.launch(process.env.TOFI_TEST_CHROME?{executablePath:process.env.TOFI_TEST_CHROME}:{});
 const page=await browser.newPage();
 const admin={id:randomUUID(),username:'Synthetic Admin',email:'admin@example.test',role:'admin',disabled:false,must_change_password:false};
 const user={id:randomUUID(),username:'Synthetic Computer User',email:'user@example.test',role:'user',disabled:false,must_change_password:false};
 const originalGeneration=randomUUID();
 const states=new Map([admin,user].map((account,index)=>[account.id,{account_id:account.id,computer_id:account.id,generation:index?originalGeneration:randomUUID(),state:'active',phase:'ready',error:'',supported:true,resources_released:false,slot:index+1,quota_bytes:8*2**30}]));
 const deletes=[],recreates=[],ensures=[];
 await page.route('**/api/**',async route=>{
  const request=route.request(),path=new URL(request.url()).pathname;
  let status=200,body;
  if(path==='/api/server-info')body={service:'tofi',protocol_version:1,instance_id:admin.id,auth:{mode:'accounts'}};
  else if(path==='/api/auth/session')body={enabled:true,authenticated:true,setup_required:false,password_transport_allowed:true,multi_account:true,owner:admin};
  else if(path==='/api/admin/accounts'&&request.method()==='GET')body=[admin,user];
  else if(path==='/api/admin/capacity')body={available_bytes:80*2**30,admission_remaining_bytes:64*2**30,warning:false,accounts:[...states.values()].filter(s=>s.slot).map(s=>({...s,logical_bytes:8*2**30,allocated_bytes:1*2**30}))};
  else if(path===`/api/admin/accounts/${user.id}`&&request.method()==='PATCH'){user.disabled=request.postDataJSON().disabled;body=user}
  else if(path.endsWith('/computer/recreate')){
   const input=request.postDataJSON();recreates.push(input);const current=states.get(user.id);
   states.set(user.id,{...current,state:'active',generation:randomUUID(),operation_id:input.operation_id,quota_bytes:input.quota_gib*2**30,slot:2,resources_released:false});body=states.get(user.id);
  } else if(path.endsWith('/computer')){
   const id=path.split('/')[4];
   if(request.method()==='DELETE'){
    const input=request.postDataJSON();deletes.push(input);const current=states.get(id);
    assert.equal(input.expected_generation,current.generation);
    await new Promise(resolve=>setTimeout(resolve,100));
    if(deletes.length===1){states.set(id,{...current,state:'cleanup_failed',operation_id:input.operation_id,phase:'stopping',error:'synthetic cleanup failure'});status=503;body={error:{message:'清理尚未完成，资源未确认释放'}}}
    else{states.set(id,{...current,state:'deleted',operation_id:input.operation_id,phase:'complete',error:'',slot:0,quota_bytes:0,resources_released:true});body=states.get(id)}
   }else body=states.get(id);
  }else{ensures.push(path);status=500;body={error:{message:'unexpected fixture request'}}}
  await route.fulfill({status,contentType:'application/json',body:JSON.stringify(body)});
 });
 await page.goto(`http://127.0.0.1:${port}/${basename(fixture)}/index.html`);
 const row=page.locator('article').filter({has:page.getByText(user.username,{exact:true})});
 await row.getByRole('button',{name:'删除云电脑并释放资源',exact:true}).click();
 const dialog=page.getByRole('dialog');
 await dialog.getByText(/聊天附件/).first().waitFor();
 if(process.env.TOFI_TEST_SCREENSHOT)await page.screenshot({path:process.env.TOFI_TEST_SCREENSHOT.replace(/\.png$/,'-confirmation.png'),fullPage:true});
 assert.match(await dialog.innerText(),/浏览器登录状态/);
 assert.match(await dialog.innerText(),/恢复备份/);
 assert.match(await dialog.innerText(),/保留账号和登录权限/);
 assert.equal(await dialog.getByRole('button',{name:'确认永久删除并释放资源'}).isEnabled(),false);
 await dialog.getByRole('button',{name:'取消',exact:true}).click();assert.equal(deletes.length,0);
 await row.getByRole('button',{name:'删除云电脑并释放资源',exact:true}).click();
 await page.keyboard.press('Escape');assert.equal(deletes.length,0);
 await row.getByRole('button',{name:'删除云电脑并释放资源',exact:true}).click();
 await dialog.getByLabel('输入完整电脑 ID 以确认').fill(user.id);
 assert.equal(await dialog.getByRole('button',{name:'确认永久删除并释放资源'}).isEnabled(),false);
 await dialog.getByRole('checkbox').check();
 await dialog.getByRole('button',{name:'确认永久删除并释放资源'}).click();
 await row.getByRole('button',{name:'重试原删除操作',exact:true}).waitFor();
 assert.match(await row.innerText(),/预留容量仍保留/);
 assert.doesNotMatch(await row.innerText(),/资源已释放/);
 assert.equal(deletes.length,1);
 await row.getByRole('button',{name:'重试原删除操作',exact:true}).click();
 await dialog.getByLabel('输入完整电脑 ID 以确认').fill(user.id);await dialog.getByRole('checkbox').check();
 await dialog.getByRole('button',{name:'确认永久删除并释放资源'}).click();
 await row.getByRole('button',{name:'重新创建空白电脑',exact:true}).waitFor();
 assert.equal(deletes.length,2);assert.deepEqual(deletes[1],deletes[0]);
 assert.match(await row.innerText(),/已删除，资源已释放/);
 await row.getByRole('button',{name:'停用账号',exact:true}).click();
 await row.getByRole('button',{name:'恢复账号',exact:true}).waitFor();
 assert.equal(await row.getByRole('button',{name:'重新创建空白电脑',exact:true}).isEnabled(),false);
 await row.getByRole('button',{name:'恢复账号',exact:true}).click();
 await row.getByRole('button',{name:'停用账号',exact:true}).waitFor();
 assert.match(await row.innerText(),/已删除，资源已释放/);assert.equal(recreates.length,0);
 await row.getByRole('button',{name:'重新创建空白电脑',exact:true}).click();
 await dialog.getByLabel('新电脑容量（GiB）').fill('16');await dialog.getByLabel('输入完整电脑 ID 以确认').fill(user.id);await dialog.getByRole('checkbox').check();
 await dialog.getByRole('button',{name:'确认创建空白电脑'}).click();
 await row.getByRole('button',{name:'删除云电脑并释放资源',exact:true}).waitFor();
 assert.equal(recreates.length,1);assert.equal(recreates[0].expected_generation,originalGeneration);assert.equal(recreates[0].quota_gib,16);
 assert.notEqual(states.get(user.id).generation,originalGeneration);
 assert.equal(ensures.length,0,'status reads and account restore must never provision a computer');
 if(process.env.TOFI_TEST_SCREENSHOT)await page.screenshot({path:process.env.TOFI_TEST_SCREENSHOT,fullPage:true});
 console.log('PASS: browser confirmation, cancel/Escape, typed identity, failure status, same-operation retry, disabled-account restore, explicit recreation, and zero implicit provisioning');
}catch(error){
 if(browser){const pages=browser.contexts().flatMap(c=>c.pages());for(const page of pages){console.error(JSON.stringify(await page.locator('dialog').evaluateAll(ds=>ds.map(d=>({open:d.open,inputs:[...d.querySelectorAll('input')].map(i=>({value:i.value,checked:i.checked,disabled:i.disabled})),buttons:[...d.querySelectorAll('button')].map(b=>({text:b.textContent,disabled:b.disabled}))}))),null,2));}}
 throw error;
}finally{
 await browser?.close();await server?.close();await rm(fixture,{recursive:true,force:true});
}
