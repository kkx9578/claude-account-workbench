/* Local Chinese UI edition 2. Account credentials never enter this page. */
(() => {
  const $ = id => document.getElementById(id);
  let dashboard=null,signature='',inFlight=false,confirmation=null,toastTimer,loginStage=2,identityBusy=false,usageBusy=false;
  const expanded=new Set();
  const api=()=>window.go?.main?.UI;
  function toast(message,error=false){const n=$('toast');n.textContent=String(message);n.classList.toggle('error',error);n.hidden=false;clearTimeout(toastTimer);toastTimer=setTimeout(()=>n.hidden=true,error?10000:4000);}
  async function invoke(method,...args){if(!api()?.[method])throw new Error('请从桌面程序打开工作台');return api()[method](...args);}
  async function action(method,args=[],message=''){try{await invoke(method,...args);if(message)toast(message);await refresh();return true;}catch(e){toast(e,true);return false;}}
  function showPage(page){document.querySelectorAll('.page').forEach(n=>n.hidden=n.id!==`page-${page}`);$('recheck-account').hidden=page!=='accounts';}
  function validDate(value){return value&&!String(value).startsWith('0001')&&!Number.isNaN(new Date(value).valueOf());}
  function clock(value){return validDate(value)?new Date(value).toLocaleString('zh-CN',{month:'numeric',day:'numeric',hour:'2-digit',minute:'2-digit'}):'';}
  function future(value){return validDate(value)&&new Date(value)>new Date();}
  function profileName(id){const p=dashboard?.profiles?.find(p=>p.id===id);return p?`${p.name}${p.email?' · '+p.email:''}`:'尚未绑定';}
  function options(select,value,accountID=''){
    select.replaceChildren(new Option('选择独立个人资料',''));
    for(const p of dashboard.profiles){const owner=dashboard.accounts.find(a=>a.id!==accountID&&a.chromeProfileID===p.id);const o=new Option(`${p.name}${p.email?' · '+p.email:''}${owner?'（已绑定）':''}`,p.id);o.disabled=!!owner;select.add(o);}select.value=value||'';
  }
  function confirm(title,detail,callback){$('confirm-title').textContent=title;$('confirm-detail').textContent=detail;confirmation=callback;$('confirm-dialog').showModal();}
  function renderAccounts(){
    const next=JSON.stringify([dashboard.accounts,dashboard.profiles,dashboard.currentID,dashboard.busy,dashboard.pending]);if(next===signature)return;signature=next;
    $('account-count').textContent=dashboard.accounts.length;$('account-list').replaceChildren();$('empty-state').hidden=dashboard.accounts.length>0;
    for(const a of dashboard.accounts){
      const row=$('account-template').content.firstElementChild.cloneNode(true);row.dataset.account=a.id;const current=a.id===dashboard.currentID;row.classList.toggle('current',current);
      row.querySelector('.account-name').textContent=a.name||a.email||'未命名账号';row.querySelector('.account-email').textContent=a.email;row.querySelector('.plan').textContent=a.plan||'';row.querySelector('.active-badge').hidden=!current;
      const change=row.querySelector('.switch-button');change.textContent=current?'当前账号':'切换';change.disabled=current||dashboard.busy||!!dashboard.pending;change.onclick=()=>confirm('切换到这个账号？',a.email,()=>action('SwitchAccount',[a.id,true]));
      if(a.problem){row.querySelector('.account-problem').hidden=false;row.querySelector('.account-problem').textContent=a.problem;}
      for(const [kind,reset] of [['session','sessionReset'],['weekly','weeklyReset']]){
        const q=row.querySelector(`[data-quota="${kind}"]`);const known=a.usage&&Number.isFinite(a.usage[kind]);const used=known?Math.max(0,Math.min(100,a.usage[kind])):0;
        q.classList.toggle('unknown',!known);q.classList.toggle('high',known&&used>=85);q.querySelector('strong').textContent=known?`${Math.round(used)}%`:'待检查';q.querySelector('i').style.width=`${used}%`;
        q.title=known&&clock(a.usage[reset])?`${clock(a.usage[reset])} 重置`:'额度尚未读取';q.querySelector('small').textContent=q.title;
      }
      const management=row.querySelector('.account-management');management.hidden=!expanded.has(a.id);
      const manage=row.querySelector('.manage-account');manage.textContent=management.hidden?'管理':'收起';manage.setAttribute('aria-expanded',String(!management.hidden));manage.onclick=()=>{management.hidden=!management.hidden;if(management.hidden)expanded.delete(a.id);else expanded.add(a.id);manage.textContent=management.hidden?'管理':'收起';manage.setAttribute('aria-expanded',String(!management.hidden));};
      const select=row.querySelector('select');select.setAttribute('aria-label',`${a.email} 的 Chrome 资料`);options(select,a.chromeProfileID,a.id);select.disabled=dashboard.busy;
      const save=row.querySelector('.save-binding');save.disabled=dashboard.busy;save.onclick=()=>select.value?action('BindChromeProfile',[a.id,select.value],'绑定已保存'):toast('请先选择 Chrome 资料',true);
      const open=row.querySelector('.open-chrome');open.disabled=!a.chromeProfileID||!dashboard.chromeAvailable;open.onclick=()=>action('OpenAccountChrome',[a.id]);
      row.querySelector('.checked-at').textContent=a.usage?`缓存更新于 ${clock(a.usage.checkedAt)} · 比例表示已用额度`:'额度尚未读取；账号已识别。';
      row.querySelector('.next-check').textContent=future(a.nextCheckAt)?`${a.rateLimited?'限流暂停中':'下次检查'} · 最早 ${clock(a.nextCheckAt)}`:'额度检查就绪';
      $('account-list').append(row);
    }
  }
  function render(){
    const current=dashboard.accounts.find(a=>a.id===dashboard.currentID);$('current-account').textContent=current?.email||(dashboard.currentID?'正在识别…':'尚未登录');$('current-verification').textContent=dashboard.currentVerified?'本地已识别':'等待登录';
    $('automatic-warning').hidden=!dashboard.automatic;renderAccounts();
    const limited=dashboard.rateLimited;const waiting=future(dashboard.nextCheckAt);
    $('quota-note').textContent=limited?`额度检查已暂停 · ${waiting?'最早 '+clock(dashboard.nextCheckAt)+' 再检查':'等待下一次检查'} · 保留上次读数`:(!current?'先识别当前账号，再刷新额度':!current.usage?'额度尚未读取 · 点击“刷新额度”立即查询':waiting?`额度为缓存值 · 下次自动检查 ${clock(dashboard.nextCheckAt)}`:'额度为缓存值 · 当前账号每 15 分钟自动检查一次');
    if(dashboard.checkBlocked)$('quota-note').textContent='上次检查未能完整保存，额度检查已暂停；请保留配置文件以便排查。';
    if(dashboard.checking)$('quota-note').textContent='正在检查账号状态…';
    $('refresh').disabled=!current||identityBusy||usageBusy||dashboard.busy||dashboard.checkBlocked||dashboard.checking||(limited&&future(dashboard.rateLimitUntil||dashboard.nextCheckAt))||future(dashboard.usageRetryAt);
    $('refresh').textContent=usageBusy?'刷新中…':'刷新额度';$('refresh').title='立即查询当前账号额度；服务器限流及短时间重复点击仍受保护';
    $('refresh-warning').hidden=!dashboard.refreshError;$('refresh-warning').textContent=dashboard.refreshError||'';
    const identityLocked=identityBusy||usageBusy||dashboard.busy||dashboard.checking||dashboard.checkBlocked||(limited&&future(dashboard.rateLimitUntil||dashboard.nextCheckAt))||future(dashboard.identityRetryAt);
    for(const id of ['recheck-account','recheck-login']){$(id).disabled=identityLocked;$(id).textContent=identityBusy?'正在识别…':'重新识别账号';}
    $('sync-needed-note').hidden=!current?.needsChatSync;
    const phase=dashboard.phase||'idle';const names={idle:'就绪',preparing:'准备中',closing:'退出中',backup:'保存中',restoring:'切换中',syncing:'同步中',starting:'打开中',done:'已完成',error:'需要处理',login:'等待登录'};
    $('operation-panel').hidden=!dashboard.busy&&phase!=='error'&&phase!=='done';$('operation-panel').classList.toggle('error',phase==='error');$('operation-pill').textContent=names[phase]||phase;$('operation-text').textContent=dashboard.status||'';
    for(const id of ['add-account','login-add','sync-chats','open-claude','complete-login','cancel-login','goto-verify'])$(id).disabled=dashboard.busy;
    const pending=dashboard.pending;$('login-working').hidden=!dashboard.busy;$('login-working').textContent=dashboard.status||'正在准备登录，请稍等…';
    $('pending-panel').hidden=!pending||dashboard.busy||phase==='error';$('no-pending').hidden=!!pending||dashboard.busy||phase==='error';$('resume-login').hidden=!pending;$('cancel-login').hidden=!pending||dashboard.busy;
    let loginFeedback=phase==='error'?dashboard.status:'';
    if(!loginFeedback&&dashboard.checkBlocked)loginFeedback='上次账号检查未能保存，检查已暂停。请保留配置文件以便排查。';
    if(!loginFeedback&&limited)loginFeedback=`账号信息检查暂时限流，${waiting?'最早 '+clock(dashboard.nextCheckAt)+' 再检查':'正在等待下一次检查'}。可以先在官方 Claude 完成登录，再回来核对。`;
    if(!loginFeedback&&dashboard.refreshError)loginFeedback=dashboard.refreshError;
    $('login-feedback').hidden=!loginFeedback;$('login-feedback').textContent=loginFeedback;
    if(pending){$('pending-email').textContent=pending.expectedEmail;$('verify-target').textContent=pending.expectedEmail;$('pending-profile').textContent=profileName(pending.profileID);$('observed-email').textContent=current?.email||'尚未识别，完成桌面登录后点“重新识别账号”';}
    $('login-instructions').hidden=loginStage===3;$('login-verify').hidden=loginStage!==3;
    document.querySelectorAll('[data-wizard]').forEach(n=>{n.classList.toggle('active',Number(n.dataset.wizard)===loginStage);n.classList.toggle('done',Number(n.dataset.wizard)<loginStage);});
  }
  async function refresh(){if(inFlight)return;inFlight=true;try{dashboard=await invoke('GetDashboard');render();}catch(e){$('operation-panel').hidden=false;$('operation-text').textContent=String(e);$('operation-pill').textContent='连接未就绪';}finally{inFlight=false;}}
  async function recheckIdentity(){
    if(identityBusy)return;identityBusy=true;if(dashboard)render();
    try{toast(await invoke('RefreshIdentity'));}catch(e){toast(e,true);}
    finally{identityBusy=false;await refresh();if(dashboard)render();}
  }
  $('recheck-account').onclick=recheckIdentity;$('recheck-login').onclick=recheckIdentity;
  async function refreshUsage(){
    if(usageBusy)return;usageBusy=true;if(dashboard)render();
    try{toast(await invoke('RefreshUsage'));}catch(e){toast(e,true);}
    finally{usageBusy=false;await refresh();if(dashboard)render();}
  }
  async function openAdd(){
    await refresh();if(dashboard?.pending){showPage('login');return;}if(!dashboard?.chromeAvailable){toast('请先安装并打开一次 Google Chrome',true);return;}if(!dashboard.profiles.length){toast(dashboard.profileError||'请先在 Chrome 中添加个人资料',true);return;}
    $('add-form').reset();options($('new-profile'),'');$('add-error').hidden=true;const current=dashboard.accounts.find(a=>a.id===dashboard.currentID);
    $('prepare-summary').textContent=current?`先保存 ${current.email} 的登录，再打开同一个 Claude 的登录页。你不用先注销。`:'请登录已有账号；这里不会注册新账号或创建新的 Claude 客户端。';$('add-dialog').showModal();
  }
  $('help').onclick=()=>showPage('help');for(const id of ['back-accounts','back-help'])$(id).onclick=()=>showPage('accounts');
  for(const id of ['add-account','login-add'])$(id).onclick=openAdd;
  $('resume-login').onclick=()=>showPage('login');$('goto-verify').onclick=()=>{loginStage=3;render();};$('back-login').onclick=()=>{loginStage=2;render();};
  document.querySelectorAll('[data-close]').forEach(b=>b.onclick=()=>$(b.dataset.close).close());
  $('expected-email').oninput=()=>{const email=$('expected-email').value.trim().toLowerCase();const a=dashboard?.accounts.find(a=>a.email.toLowerCase()===email);options($('new-profile'),$('new-profile').value,a?.id||'');};
  $('add-form').onsubmit=async event=>{event.preventDefault();$('begin-login').disabled=true;try{await invoke('StartAddAccount',$('new-profile').value,$('expected-email').value.trim(),$('network-confirmed').checked);loginStage=2;$('add-dialog').close();showPage('login');await refresh();}catch(e){$('add-error').textContent=String(e);$('add-error').hidden=false;}finally{$('begin-login').disabled=false;}};
  $('complete-login').onclick=async()=>{
    const finish=async confirmed=>{if(await action('CompleteLogin',[confirmed],'账号已保存，可以切换使用'))showPage('accounts');};
    if(dashboard?.accounts.find(a=>a.id===dashboard.currentID)?.needsChatSync){confirm('同步原聊天并保存账号？','已识别账号。继续将同步聊天入口并重启 Claude，请先保存并停止当前任务。',()=>finish(true));}
    else await finish(false);
  };
  $('cancel-login').onclick=async()=>{if(await action('CancelLogin',[],'已取消添加，可切回已保存的账号'))showPage('accounts');};
  $('pending-chrome').onclick=()=>dashboard?.pending&&action('OpenProfile',[dashboard.pending.profileID]);
  $('confirm-action').onclick=async()=>{const callback=confirmation;confirmation=null;$('confirm-dialog').close();if(callback)await callback();};
  $('sync-chats').onclick=()=>confirm('同步聊天入口？','让当前账号找到其他账号的本地 Code 聊天。',()=>action('SyncChats',[true]));
  $('refresh').onclick=refreshUsage;
  $('open-claude').onclick=()=>action('OpenClaude');$('empty-open').onclick=$('open-claude').onclick;$('open-easyai').onclick=()=>action('OpenEasyAI');$('data-folder').onclick=()=>action('OpenDataFolder');$('manual-mode').onclick=()=>action('UseManualSwitching',[],'已改为手动切换');
  if(window.runtime?.EventsOn){window.runtime.EventsOn('tray-add',openAdd);window.runtime.EventsOn('tray-switch',id=>{const a=dashboard?.accounts.find(a=>a.id===id);if(a){showPage('accounts');confirm('切换到这个账号？',a.email,()=>action('SwitchAccount',[id,true]));}});window.runtime.EventsOn('tray-sync',()=>{showPage('accounts');$('sync-chats').click();});}
  refresh();setInterval(refresh,2000);
})();
