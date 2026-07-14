const $=s=>document.querySelector(s);
const SL={0:'无轨迹',1:'待揽收',2:'已揽收',3:'运输中',4:'派送中',5:'已签收',6:'异常',7:'待取件'};
function esc(s){const d=document.createElement('div');d.textContent=s;return d.innerHTML;}
function relTime(t){if(!t)return'-';const d=new Date(t.replace(/-/g,'/')),now=Date.now(),diff=now-d.getTime();if(diff<0)return fmtT(t);const m=Math.floor(diff/60000);if(m<1)return'刚刚';if(m<60)return m+'分钟前';const h=Math.floor(m/60);if(h<24)return h+'小时前';const dy=Math.floor(h/24);return dy+'天前';}
function fmtT(t){if(!t)return'-';return t.replace(/^\d{4}-/,'').slice(0,14);}
let _toastOffset=0;
function toast(msg,type='info'){const el=document.createElement('div');el.className=`toast toast-${type}`;el.textContent=msg;el.style.top=(16+_toastOffset)+'px';_toastOffset+=44;document.body.appendChild(el);setTimeout(()=>{el.remove();_toastOffset=Math.max(0,_toastOffset-44);},3000);}
function csvCell(v){const s=String(v??'');const pref=/^[=+\-@]/.test(s)?`'${s}`:s;return `"${pref.replace(/"/g,'""')}"`;}
function arrayBufferToBase64(buf){const bytes=new Uint8Array(buf);let out='';for(let i=0;i<bytes.length;i+=0x8000){let chunk='';for(const b of bytes.subarray(i,i+0x8000))chunk+=String.fromCharCode(b);out+=chunk;}return btoa(out);}

const COLUMN_DEFS=[
  ['select','34px','选择'],['num','38px','序号'],['no','170px','运单号'],['status','78px','物流状态'],
  ['last-time','112px','最新轨迹时间'],['city','92px','当前城市'],['result','78px','结果'],['carrier','98px','快递公司'],
  ['remarks','88px','备注'],['updated','106px','最近同步'],['latest','minmax(320px,1fr)','最新轨迹'],['expand','34px','详情']
];
const DEFAULT_COLUMNS=COLUMN_DEFS.map(c=>c[0]);
const COLUMN_KEY='logistics-columns';
const AUTO_SYNC_KEY='logistics-auto-sync';
function loadJSON(key,fallback){try{return JSON.parse(localStorage.getItem(key))??fallback;}catch{return fallback;}}

const state={
  records:[],total:0,page:1,pageSize:20,
  filters:{statusCode:'',search:'',carrier:'',tag:'',dateFrom:'',dateTo:'',sort:'updated_at',order:'desc'},
  selectedIds:new Set(),stats:{},carriers:[],tags:[],
  importing:false,importAbort:null,importFailures:[],
  settings:{proxyApi:'',timeout:3,concurrency:5},
  syncing:false,syncAbort:null,syncErrors:{},syncBatches:[],lastSyncTime:null,
  columns:loadJSON(COLUMN_KEY,DEFAULT_COLUMNS),
  autoSync:{enabled:false,minutes:30,timer:null,...loadJSON(AUTO_SYNC_KEY,{})}
};

const api={
  async getRecords(p){const q=new URLSearchParams();for(const[k,v]of Object.entries(p)){if(v!==''&&v!=null)q.set(k,v);}return(await fetch('/api/records?'+q)).json();},
  async getStats(){return(await fetch('/api/stats')).json();},
  async getCarriers(){return(await fetch('/api/carriers')).json();},
  async getSettings(){return(await fetch('/api/settings')).json();},
  async updateSettings(s){return(await fetch('/api/settings',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(s)})).json();},
  async updateRemarks(id,r){await fetch(`/api/records/${id}/remarks`,{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({remarks:r})});},
  async batchRemarks(ids,r){return(await fetch('/api/records/batch-remarks',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({ids,remarks:r})})).json();},
  async deleteRecords(ids){return(await fetch('/api/records',{method:'DELETE',headers:{'Content-Type':'application/json'},body:JSON.stringify({ids})})).json();},
  async getTags(){return(await fetch('/api/tags')).json();},
  async batchTag(ids,action,tagOrTags){return(await fetch('/api/records/batch-tags',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({ids,action,tag:action!=='set'?tagOrTags:undefined,tags:action==='set'?tagOrTags:undefined})})).json();},
  async getLogs(limit=50){return(await fetch('/api/logs?limit='+limit)).json();},
};

// Settings
async function loadSettings(){
  try{const s=await api.getSettings();state.settings=s||state.settings;}catch{}
  state.settings.timeout=3;
  $('#globalProxyApi').value=state.settings.proxyApi||'';
  $('#cfgTimeout').value=3;
  $('#cfgConcurrency').value=state.settings.concurrency||5;
  $('#proxyStatus').textContent=state.settings.proxyApi?'已启用':'必须设置';
  $('#autoSyncEnabled').checked=!!state.autoSync.enabled;
  $('#autoSyncMinutes').value=state.autoSync.minutes||30;
}
async function saveSettings(){
  const proxyApi=$('#globalProxyApi').value.trim();
  if(!proxyApi)return toast('代理API不能为空','err');
  const s={proxyApi,timeout:3,concurrency:Number($('#cfgConcurrency').value)||5};
  state.autoSync.enabled=$('#autoSyncEnabled').checked;
  state.autoSync.minutes=Math.max(5,Math.min(1440,Number($('#autoSyncMinutes').value)||30));
  localStorage.setItem(AUTO_SYNC_KEY,JSON.stringify({enabled:state.autoSync.enabled,minutes:state.autoSync.minutes}));
  scheduleAutoSync();
  try{const r=await api.updateSettings(s);if(r.error)return toast(r.error,'err');state.settings=r.settings||s;state.settings.timeout=3;$('#proxyStatus').textContent='已启用';toast('设置已保存','ok');}catch(e){toast('保存失败','err');}
}
$('#btnSaveSettings').addEventListener('click',saveSettings);

// Carriers
async function loadCarriers(){
  try{state.carriers=await api.getCarriers();}catch{state.carriers=[];}
  const sel=$('#filterCarrier'),cur=sel.value;
  sel.innerHTML='<option value="">全部快递</option>';
  for(const c of state.carriers){const o=document.createElement('option');o.value=c.carrier_name;o.textContent=`${c.carrier_name}(${c.count})`;sel.appendChild(o);}
  sel.value=cur;
}

async function loadTags(){
  try{state.tags=await api.getTags();}catch{state.tags=[];}
  const sel=$('#filterTag'),cur=sel.value;
  sel.innerHTML='<option value="">全部标签</option>';
  for(const t of state.tags){const o=document.createElement('option');o.value=t.name;o.textContent=`${t.name}(${t.count})`;sel.appendChild(o);}
  sel.value=cur;
}

// Stats
function renderStats(s){
  const items=[
    {key:'',label:'总量',value:s.total||0,color:'var(--text)'},
    {key:'monitoring',label:'监控中',value:s.monitoring||0,color:'var(--blue)'},
    {key:'0',label:'无轨迹',value:s.noTracking||0,color:'var(--gray)'},
    {key:'1',label:'待揽收',value:s.pendingPickup||0,color:'var(--purple)'},
    {key:'2',label:'已揽收',value:s.pickedUp||0,color:'var(--teal)'},
    {key:'3',label:'运输中',value:s.inTransit||0,color:'var(--blue)'},
    {key:'4',label:'派送中',value:s.delivering||0,color:'var(--warn)'},
    {key:'7',label:'待取件',value:s.waitingPickup||0,color:'var(--gray)'},
    {key:'5',label:'已签收',value:s.delivered||0,color:'var(--ok)'},
    {key:'6',label:'异常',value:s.abnormal||0,color:'var(--err)'},
  ];
  const af=state.filters.statusCode;
  $('#statsRow').innerHTML=items.map(it=>`<div class="stat-card${af===it.key?' active':''}" data-filter="${it.key}"><div class="stat-num" style="color:${it.color}">${it.value}</div><div class="stat-label">${it.label}</div></div>`).join('');
  renderAlert(s);
}

function renderAlert(s){
  const alerts=[];
  if(s.stale>0)alerts.push(`${s.stale} 个监控中单号超过3天未更新`);
  if(s.longTransit>0)alerts.push(`${s.longTransit} 个运输中单号超过7天`);
  if(s.abnormal>0)alerts.push(`${s.abnormal} 个异常件需要关注`);
  const el=$('#alertRow');
  if(alerts.length>0&&!el.dataset.dismissed){
    $('#alertText').textContent=alerts.join(' | ');
    el.classList.add('visible');
  }else{
    el.classList.remove('visible');
  }
}
$('#alertClose').addEventListener('click',()=>{const el=$('#alertRow');el.classList.remove('visible');el.dataset.dismissed='1';});

// Table
function normalizeStatusCodeText(statusCode,statusDesc,lastDesc,traceCount){
  let sc=Number(statusCode)||0;
  const text=`${statusDesc||''} ${lastDesc||''}`;
  if(/异常|退回|退件|拒收|拦截|失败/.test(text))return 6;
  if(/签收|已签收|妥投|已收货/.test(text))return 5;
  if(/派送|派件|投递/.test(text))return 4;
  if(/待取件|待领取|暂存|驿站/.test(text))return 7;
  if(/运输|发出|转运|到达|分拣/.test(text))return 3;
  if(/揽收|收件/.test(text))return 2;
  if(/待揽收|下单|已下单/.test(text))return 1;
  if(sc===0&&traceCount>0)return 3;
  return sc;
}
function buildParsedFromSSE(d){
  const traces=Array.isArray(d.traces)?d.traces:[];
  const lastTrace=traces.length?traces[traces.length-1]:null;
  const statusDesc=d.statusDesc||d.lastDesc||'';
  const lastDesc=d.lastDesc||(lastTrace?lastTrace.desc:'');
  const lastTime=d.lastTime||(lastTrace?lastTrace.time:'');
  const traceCount=Number(d.traceCount)||traces.length||0;
  return {
    cpCode:d.cpCode||'',
    cpName:d.cpName||'',
    status:d.status||'',
    statusCode:d.statusCode||'',
    statusDesc,
    progress:d.progress||'',
    from:d.from||'',
    current:d.current||'',
    predict:d.predict||'',
    traceCount,
    lastTime,
    lastDesc,
    traces
  };
}
function applySyncResultToState(mailNo,data,isError,errorMessage=''){
  const idx=state.records.findIndex(r=>r.tracking_number===mailNo);
  if(idx<0)return;
  const record={...state.records[idx]};
  const parsed=data?buildParsedFromSSE(data):null;
  if(parsed){
    const sc=normalizeStatusCodeText(parsed.statusCode,parsed.statusDesc||parsed.status,parsed.lastDesc,parsed.traceCount);
    record.carrier_code=parsed.cpCode||record.carrier_code;
    record.carrier_name=parsed.cpName||record.carrier_name;
    record.status=parsed.status||record.status;
    record.status_code=sc;
    record.status_desc=parsed.statusDesc||record.status_desc;
    record.last_track_time=parsed.lastTime||record.last_track_time;
    record.last_track_desc=parsed.lastDesc||record.last_track_desc;
    record.current_city=parsed.current||record.current_city;
    record.from_city=parsed.from||record.from_city;
    record.predict=parsed.predict||record.predict;
    record.progress=parsed.progress||record.progress;
    record.trace_count=parsed.traceCount;
    record.result_json={
      mailNo:mailNo,
      cpCode:parsed.cpCode,
      cpName:parsed.cpName,
      status:parsed.status,
      statusCode:parsed.statusCode,
      statusDesc:parsed.statusDesc,
      progress:parsed.progress,
      from:parsed.from,
      current:parsed.current,
      predict:parsed.predict,
      traceCount:parsed.traceCount,
      lastTime:parsed.lastTime,
      lastDesc:parsed.lastDesc,
      traces:parsed.traces
    };
    record.error_msg='';
  }
  if(isError){
    record.error_msg=errorMessage||'同步失败';
  }
  record.updated_at=new Date().toISOString().slice(0,19).replace('T',' ');
  state.records[idx]=record;
  renderTable(state.records);
  updateSelectionUI();
}
function renderTable(records){
  if(!records.length){$('#tableBody').innerHTML=`<div class="empty-state"><svg viewBox="0 0 24 24"><path d="M20 2H4c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h14l4 4V4c0-1.1-.9-2-2-2zm-2 12H6v-2h12v2zm0-3H6V9h12v2zm0-3H6V6h12v2z"/></svg><p>暂无数据</p></div>`;return;}
  const off=(state.page-1)*state.pageSize;
  $('#tableBody').innerHTML=records.map((r,i)=>{
    const sc=r.status_code??0,traces=r.result_json?.traces||[];
    const chk=state.selectedIds.has(r.id)?'checked':'',sel=state.selectedIds.has(r.id)?' selected':'';
    const syncResult=r.error_msg?r.error_msg:(r.trace_count>0?'更新成功':'无数据');
    const resultClass=r.error_msg?'fail':(r.trace_count>0?'ok':'');
    const latestTime=r.last_track_time||'';
    const latestDesc=r.last_track_desc||'-';
    return`<div class="table-row${sel}" data-id="${r.id}">
      <div class="td col-select"><input type="checkbox" class="row-checkbox" data-id="${r.id}" ${chk}></div>
      <div class="td col-num row-num">${off+i+1}</div>
      <div class="td col-no tracking-no" title="${esc(r.tracking_number)}">${esc(r.tracking_number)}${r.tags?'<div style="margin-top:1px">'+r.tags.split(',').filter(Boolean).map(t=>'<span class="tag-badge">'+esc(t)+'</span>').join('')+'</div>':''}</div>
      <div class="td col-status"><span class="badge badge-${sc}">${SL[sc]||'未知'}</span></div>
      <div class="td col-last-time td-time" title="${esc(latestTime||'')}">${latestTime?relTime(latestTime):'-'}</div>
      <div class="td col-city">${esc(r.current_city||'-')}</div>
      <div class="td col-result td-result ${resultClass}" title="${esc(syncResult)}">${esc(syncResult.slice(0,12))}</div>
      <div class="td col-carrier">${esc(r.carrier_name||'-')}</div>
      <div class="td col-remarks"><input class="remarks-input" data-id="${r.id}" data-orig="${esc(r.remarks||'')}" value="${esc(r.remarks||'')}" placeholder="备注"></div>
      <div class="td col-updated td-time" title="${esc(r.updated_at||'')}">${r.updated_at?relTime(r.updated_at):'-'}</div>
      <div class="td col-latest td-latest" title="${esc((latestTime?latestTime+'\n':'')+latestDesc)}">
        <div class="latest-time">${latestTime?fmtT(latestTime):'-'}</div>
        <div class="latest-desc">${esc(latestDesc)}</div>
      </div>
      <div class="td col-expand"><button class="expand-btn" data-id="${r.id}" data-expanded="0">${traces.length?'&#9654;':''}</button></div>
    </div><div class="detail-row" id="detail-${r.id}">${renderDetail(r)}</div>`;
  }).join('');
  applyColumnVisibility();
}
function renderDetail(r){
  const d=r.result_json||{},traces=d.traces||[];
  if(!traces.length&&!r.error_msg)return'<div style="color:var(--text3);font-size:11px">无详细信息</div>';
  if(r.error_msg&&!traces.length)return`<div style="color:var(--err);font-size:11px">${esc(r.error_msg)}</div>`;
  let h='<div class="detail-meta">';
  if(d.from)h+=`<span>发件:<strong>${esc(d.from)}</strong></span>`;
  if(d.current)h+=`<span>当前:<strong>${esc(d.current)}</strong></span>`;
  if(d.predict)h+=`<span>预计:<strong>${esc(d.predict)}</strong></span>`;
  if(d.progress)h+=`<span>进度:<strong>${d.progress}%</strong></span>`;
  if(d.cpName)h+=`<span>快递:<strong>${esc(d.cpName)}</strong></span>`;
  h+='</div>';
  if(traces.length){h+='<div class="timeline">';for(let i=traces.length-1;i>=0;i--)h+=`<div class="timeline-item"><div class="timeline-time">${esc(traces[i].time)}</div><div class="timeline-desc">${esc(traces[i].desc)}</div></div>`;h+='</div>';}
  return h;
}

function renderPagination(total,page,ps){
  const pages=Math.ceil(total/ps)||1;let b='';
  b+=`<button class="page-btn" data-page="${page-1}" ${page<=1?'disabled':''}>&laquo;</button>`;
  const s=Math.max(1,page-3),e=Math.min(pages,page+3);
  if(s>1)b+=`<button class="page-btn" data-page="1">1</button>`;
  if(s>2)b+='<span style="color:var(--text3)">...</span>';
  for(let p=s;p<=e;p++)b+=`<button class="page-btn${p===page?' active':''}" data-page="${p}">${p}</button>`;
  if(e<pages-1)b+='<span style="color:var(--text3)">...</span>';
  if(e<pages)b+=`<button class="page-btn" data-page="${pages}">${pages}</button>`;
  b+=`<button class="page-btn" data-page="${page+1}" ${page>=pages?'disabled':''}>&raquo;</button>`;
  $('#pagination').innerHTML=`<div class="info">共<strong>${total}</strong>条 第${page}/${pages}页</div><div style="display:flex;align-items:center;gap:4px"><div class="pages">${b}</div><select class="page-size-select" id="pageSizeSelect">${[20,50,100].map(n=>`<option value="${n}" ${n===ps?'selected':''}>${n}条/页</option>`).join('')}</select></div>`;
}

async function refreshData(opts={}){
  const p={page:state.page,pageSize:state.pageSize,...state.filters};
  const[stats,recs]=await Promise.all([api.getStats(),api.getRecords(p)]);
  state.stats=stats;state.records=recs.records||[];state.total=recs.total||0;
  renderStats(state.stats);renderTable(state.records);renderPagination(state.total,state.page,state.pageSize);updateSelectionUI();applyColumnVisibility();
  if(opts.meta)await Promise.all([loadCarriers(),loadTags()]);
}
function refreshSelectionOnly(){
  renderTable(state.records);
  updateSelectionUI();
}
function updateSelectionUI(){
  const c=state.selectedIds.size,busy=state.syncing||state.importing;
  $('#btnSyncSelected').disabled=c===0||busy;$('#btnDeleteSelected').disabled=c===0||busy;$('#btnBatchRemarks').disabled=c===0||busy;$('#btnBatchTag').disabled=c===0||busy;
  for(const id of ['#btnSyncMonitor','#btnSyncNoTracking','#btnSyncFailed','#btnSyncCurrent'])$(id).disabled=busy;
  $('#btnImport').disabled=busy;$('#btnStopSync').classList.toggle('hidden',!state.syncing);
  $('#selectionCount').textContent=c>0?`已选${c}条`:'';
  const clearBtn=$('#btnClearSel');if(c>0){clearBtn.classList.remove('hidden');}else{clearBtn.classList.add('hidden');}
  $('#checkAll').checked=state.records.length>0&&state.records.every(r=>state.selectedIds.has(r.id));
}
function updateSyncButtons(){updateSelectionUI();}
function updateLastSyncInfo(){
  if(state.lastSyncTime){$('#lastSyncInfo').textContent='最近同步: '+new Date(state.lastSyncTime).toLocaleTimeString('zh-CN',{hour12:false});}
}

// Events
$('#statsRow').addEventListener('click',e=>{const c=e.target.closest('.stat-card');if(!c)return;const k=c.dataset.filter;state.filters.statusCode=state.filters.statusCode===k?'':k;state.page=1;$('#filterStatus').value=['0','1','2','3','4','5','6','7'].includes(state.filters.statusCode)?state.filters.statusCode:'';refreshData();});
$('#btnSearch').addEventListener('click',()=>{state.filters.statusCode=$('#filterStatus').value;state.filters.dateFrom=$('#filterDateFrom').value;state.filters.dateTo=$('#filterDateTo').value;state.filters.search=$('#filterSearch').value.trim();state.filters.carrier=$('#filterCarrier').value;state.filters.tag=$('#filterTag').value;state.page=1;refreshData();});
$('#btnReset').addEventListener('click',()=>{state.filters={statusCode:'',search:'',carrier:'',tag:'',dateFrom:'',dateTo:'',sort:'updated_at',order:'desc'};$('#filterStatus').value='';$('#filterDateFrom').value='';$('#filterDateTo').value='';$('#filterSearch').value='';$('#filterCarrier').value='';$('#filterTag').value='';state.page=1;refreshData();});
$('#filterSearch').addEventListener('keydown',e=>{if(e.key==='Enter')$('#btnSearch').click();});
$('.table-head').addEventListener('click',e=>{const th=e.target.closest('.th[data-sort]');if(!th)return;const col=th.dataset.sort;if(state.filters.sort===col)state.filters.order=state.filters.order==='asc'?'desc':'asc';else{state.filters.sort=col;state.filters.order='desc';}state.page=1;refreshData();});
$('#checkAll').addEventListener('change',e=>{if(e.target.checked)state.records.forEach(r=>state.selectedIds.add(r.id));else state.records.forEach(r=>state.selectedIds.delete(r.id));refreshSelectionOnly();});
$('#btnClearSel').addEventListener('click',()=>{state.selectedIds.clear();refreshSelectionOnly();});
$('#tableBody').addEventListener('change',e=>{if(!e.target.classList.contains('row-checkbox'))return;const id=Number(e.target.dataset.id);if(e.target.checked)state.selectedIds.add(id);else state.selectedIds.delete(id);updateSelectionUI();const row=e.target.closest('.table-row');if(row)row.classList.toggle('selected',e.target.checked);});
$('#tableBody').addEventListener('click',e=>{const btn=e.target.closest('.expand-btn');if(!btn)return;const id=btn.dataset.id,det=$(`#detail-${id}`);if(!det)return;const exp=btn.dataset.expanded==='1';btn.dataset.expanded=exp?'0':'1';btn.innerHTML=exp?'&#9654;':'&#9660;';det.classList.toggle('open',!exp);});
$('#tableBody').addEventListener('focusout',async e=>{if(!e.target.classList.contains('remarks-input'))return;const v=e.target.value.trim();if(v===e.target.dataset.orig)return;e.target.dataset.orig=v;await api.updateRemarks(Number(e.target.dataset.id),v);},true);
$('#pagination').addEventListener('click',e=>{const btn=e.target.closest('.page-btn');if(!btn||btn.disabled)return;state.page=Number(btn.dataset.page);refreshData();});
$('#pagination').addEventListener('change',e=>{if(e.target.id==='pageSizeSelect'){state.pageSize=Number(e.target.value);state.page=1;refreshData();}});
$('#btnDeleteSelected').addEventListener('click',async()=>{const ids=[...state.selectedIds];if(!ids.length||!confirm(`确定删除${ids.length}条？`))return;await api.deleteRecords(ids);state.selectedIds.clear();toast(`已删除${ids.length}条`,'ok');refreshData({meta:true});});
$('#btnBatchRemarks').addEventListener('click',()=>{const c=state.selectedIds.size;if(!c)return;$('#batchRemarksCount').textContent=c;$('#batchRemarksInput').value='';$('#batchRemarksModal').classList.remove('hidden');});
$('#batchRemarksClose').addEventListener('click',()=>$('#batchRemarksModal').classList.add('hidden'));
$('#batchRemarksCancel').addEventListener('click',()=>$('#batchRemarksModal').classList.add('hidden'));
$('#batchRemarksSave').addEventListener('click',async()=>{const ids=[...state.selectedIds],r=$('#batchRemarksInput').value.trim();await api.batchRemarks(ids,r);$('#batchRemarksModal').classList.add('hidden');toast(`已更新${ids.length}条备注`,'ok');refreshData();});
$('#btnExport').addEventListener('click',()=>{const p={...state.filters},q=new URLSearchParams();for(const[k,v]of Object.entries(p)){if(v!==''&&v!=null)q.set(k,v);}fetch('/api/export?'+q).then(r=>r.json()).then(data=>{const rows=data.records||[];if(!rows.length)return toast('无数据','err');const csv='\uFEFF'+['单号,状态,快递公司,当前城市,最新物流,最新时间,备注,同步时间',...rows.map(r=>[csvCell(r.tracking_number),csvCell(SL[r.status_code]||''),csvCell(r.carrier_name||''),csvCell(r.current_city||''),csvCell(r.last_track_desc||''),csvCell(r.last_track_time||''),csvCell(r.remarks||''),csvCell(r.updated_at||'')].join(','))].join('\n');const a=document.createElement('a');a.href=URL.createObjectURL(new Blob([csv],{type:'text/csv;charset=utf-8'}));a.download=`logistics-${new Date().toISOString().slice(0,10)}.csv`;a.click();toast('已导出','ok');});});
$('#btnCopy').addEventListener('click',async()=>{const ids=[...state.selectedIds],nums=ids.length?state.records.filter(r=>ids.includes(r.id)).map(r=>r.tracking_number):state.records.map(r=>r.tracking_number);if(!nums.length)return toast('无单号','err');await navigator.clipboard.writeText(nums.join('\n'));toast(`已复制${nums.length}个单号`,'ok');});

// Import
function parseImportInput(){
  const raw=($('#importInput').value||'').split(/[\n,;]+/).map(s=>s.trim());
  const seen=new Set(),numbers=[],duplicates=[],invalid=[];
  for(const no of raw){
    if(!no)continue;
    if(no.length<5){invalid.push(no);continue;}
    if(seen.has(no)){duplicates.push(no);continue;}
    seen.add(no);numbers.push(no);
  }
  return{numbers,duplicates,invalid};
}
function getImportNumbers(){return parseImportInput().numbers;}
function updateImportCount(){const p=parseImportInput();let text=`${p.numbers.length} 个单号`;const extra=[];if(p.duplicates.length)extra.push(`重复${p.duplicates.length}`);if(p.invalid.length)extra.push(`无效${p.invalid.length}`);if(extra.length)text+=`（已排除${extra.join('，')}）`;$('#importCount').textContent=text;}
function resetImportFailures(){state.importFailures=[];$('#importFailures').classList.add('hidden');$('#importFailuresList').innerHTML='';}
function renderImportFailures(){
  const box=$('#importFailures'),list=$('#importFailuresList');
  if(!state.importFailures.length){box.classList.add('hidden');list.innerHTML='';return;}
  box.classList.remove('hidden');
  list.innerHTML=state.importFailures.map((f,i)=>`<div class="import-failure-item"><span>${i+1}. ${esc(f.mailNo||'')}</span><em>${esc(f.error||'失败')}</em></div>`).join('');
}
$('#btnImport').addEventListener('click',()=>{$('#importModal').classList.remove('hidden');updateImportCount();$('#importProgress').classList.add('hidden');$('#importDone').textContent='0';$('#importTotal').textContent='0';$('#importOk').textContent='0';$('#importFail').textContent='0';$('#importElapsed').textContent='0';$('#importProgressFill').style.width='0%';resetImportFailures();});
$('#modalClose').addEventListener('click',()=>{if(!state.importing)$('#importModal').classList.add('hidden');});
$('#importCancel').addEventListener('click',()=>{if(state.importing&&state.importAbort){state.importAbort.abort();state.importing=false;$('#importStart').textContent='开始导入';$('#importStart').disabled=false;}else{$('#importModal').classList.add('hidden');}});
$('#copyImportFailures').addEventListener('click',async()=>{const nums=state.importFailures.map(f=>f.mailNo).filter(Boolean);if(!nums.length)return toast('无失败单号','err');await navigator.clipboard.writeText(nums.join('\n'));toast(`已复制${nums.length}个失败单号`,'ok');});
$('#retryImportFailures').addEventListener('click',()=>{const nums=state.importFailures.map(f=>f.mailNo).filter(Boolean);if(!nums.length)return toast('无失败单号','err');$('#importInput').value=nums.join('\n');updateImportCount();resetImportFailures();$('#importStart').click();});
$('#importInput').addEventListener('input',updateImportCount);
$('#importFile').addEventListener('change',e=>{const f=e.target.files[0];if(!f)return;const ext=f.name.split('.').pop().toLowerCase();if(ext==='xls'){toast('旧版XLS暂不支持，请另存为XLSX或CSV','err');e.target.value='';return;}if(ext==='xlsx'){const r=new FileReader();r.onload=async ev=>{try{const b64=arrayBufferToBase64(ev.target.result);const resp=await fetch('/api/parse-excel',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({data:b64})});const d=await resp.json();if(d.error){toast(d.error,'err');return;}$('#importInput').value=d.numbers.join('\n');updateImportCount();toast(`从Excel解析出${d.count}个单号`,'ok');}catch(err){toast('Excel解析失败','err');}};r.readAsArrayBuffer(f);}else{const r=new FileReader();r.onload=ev=>{$('#importInput').value=ev.target.result;updateImportCount();};r.readAsText(f,'UTF-8');}e.target.value='';});
$('#importStart').addEventListener('click',async()=>{
  let nums=getImportNumbers();if(!nums.length)return toast('请输入单号','err');
  try{
    const dupResp=await fetch('/api/check-duplicates',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({numbers:nums})});
    const dupData=await dupResp.json();
    if(dupData.duplicates.length>0){
      const action=prompt(`发现 ${dupData.duplicates.length} 个已存在的单号（共 ${dupData.totalCount} 个）。\n输入 "skip" 跳过重复，"all" 全部导入（覆盖更新），或取消导入：`,'all');
      if(!action)return;
      if(action.trim().toLowerCase()==='skip'){
        const dupSet=new Set(dupData.duplicates.map(d=>d.trackingNumber));
        nums=nums.filter(n=>!dupSet.has(n));
        if(!nums.length)return toast('跳过重复后无新单号','info');
        toast(`已跳过${dupData.duplicates.length}个重复，导入${nums.length}个新单号`,'info');
      }
    }
  }catch{}
  state.importing=true;state.importAbort=new AbortController();updateSyncButtons();
  $('#importStart').textContent='导入中...';$('#importStart').disabled=true;
  $('#importProgress').classList.remove('hidden');resetImportFailures();
  $('#importDone').textContent='0';$('#importTotal').textContent=nums.length;$('#importOk').textContent='0';$('#importFail').textContent='0';$('#importElapsed').textContent='0';$('#importProgressFill').style.width='0%';
  const t0=Date.now(),timer=setInterval(()=>{$('#importElapsed').textContent=((Date.now()-t0)/1000).toFixed(0);},500);
  try{
    const resp=await fetch('/api/import',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({numbers:nums,cpCode:$('#importCpCode').value.trim(),tags:$('#importTags').value.trim(),remarks:$('#importRemarks').value.trim(),proxyApi:state.settings.proxyApi||'',timeout:3,concurrency:state.settings.concurrency}),signal:state.importAbort.signal});
    if(!resp.ok){const d=await resp.json().catch(()=>({}));throw new Error(d.error||`HTTP ${resp.status}`);}
    const reader=resp.body.getReader(),dec=new TextDecoder();let buf='';
    while(true){const{done,value}=await reader.read();if(done)break;buf+=dec.decode(value,{stream:true});const lines=buf.split('\n');buf=lines.pop();
      for(const line of lines){if(!line.startsWith('data: '))continue;try{const d=JSON.parse(line.slice(6));if(d.type==='result'||d.type==='error'){const dn=(d.ok||0)+(d.fail||0);$('#importDone').textContent=dn;$('#importOk').textContent=d.ok||0;$('#importFail').textContent=d.fail||0;$('#importProgressFill').style.width=`${(dn/nums.length*100).toFixed(1)}%`;if(d.type==='error'){state.importFailures.push({mailNo:d.mailNo,error:d.error});renderImportFailures();}}if(d.type==='complete'){$('#importProgressFill').style.width='100%';toast(`导入完成: ${d.ok}成功 ${d.fail}失败 ${d.elapsed}秒`,'ok');renderImportFailures();}}catch{}}
    }
  }catch(e){if(e.name!=='AbortError')toast(`导入失败: ${e.message||'未知错误'}`,'err');}
  clearInterval(timer);state.importing=false;updateSyncButtons();$('#importStart').textContent='开始导入';$('#importStart').disabled=false;refreshData({meta:true});
});

// Sync with progress
function showSync(title){state.syncing=true;state.syncErrors={};updateSyncButtons();$('#syncProgress').classList.add('active');$('#syncTitle').textContent=title;$('#syncDone').textContent='0';$('#syncTotal').textContent='0';$('#syncOk').textContent='0';$('#syncFail').textContent='0';$('#syncBarFill').style.width='0%';$('#syncTime').textContent='0s';$('#syncLog').innerHTML='';$('#syncErrorStats').innerHTML='';}
function addLog(msg,type=''){const log=$('#syncLog'),div=document.createElement('div');div.className=`sync-log-item ${type}`;div.textContent=msg;log.appendChild(div);log.scrollTop=log.scrollHeight;}
function classifySyncError(msg=''){if(msg.includes('取代理失败'))return'代理提取失败';if(msg.includes('取token失败'))return'token获取失败';if(msg.includes('token'))return'token无效';if(msg.includes('限流')||msg.includes('RGV587'))return'接口限流';if(msg.includes('无物流数据'))return'无物流数据';if(msg.includes('已取消'))return'已取消';return'其他错误';}
function renderSyncErrorStats(){const entries=Object.entries(state.syncErrors);$('#syncErrorStats').innerHTML=entries.length?entries.map(([k,v])=>`<span>${esc(k)}: <strong>${v}</strong></span>`).join(''):'';}
function syncBody(extra={}){return{timeout:3,concurrency:state.settings.concurrency,proxyApi:state.settings.proxyApi||'',...extra};}
function currentFilterBody(mode='current'){return syncBody({mode,statusCode:String(state.filters.statusCode||''),search:state.filters.search||'',carrier:state.filters.carrier||'',tag:state.filters.tag||'',dateFrom:state.filters.dateFrom||'',dateTo:state.filters.dateTo||''});}
function pushBatch(title,ok,fail,total,elapsed,aborted){state.syncBatches.unshift({title,ok,fail,total,elapsed,aborted,time:new Date().toLocaleString('zh-CN',{hour12:false}),errors:{...state.syncErrors}});state.syncBatches=state.syncBatches.slice(0,50);}

async function runSync(url,body,title){
  showSync(title);state.syncAbort=new AbortController();let completed=false,finalInfo={ok:0,fail:0,total:0,elapsed:'0'};
  const t0=Date.now(),timer=setInterval(()=>{$('#syncTime').textContent=((Date.now()-t0)/1000).toFixed(0)+'s';},500);
  try{
    const resp=await fetch(url,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body),signal:state.syncAbort.signal});
    const ct=resp.headers.get('content-type')||'';
    if(!ct.includes('text/event-stream')){const j=await resp.json();addLog(j.error||j.message||'暂无可同步记录',j.error?'fail':'');completed=true;}
    if(!ct.includes('text/event-stream'))throw new Error('__NON_STREAM_DONE__');
    const reader=resp.body.getReader(),dec=new TextDecoder();let buf='';
    while(true){const{done,value}=await reader.read();if(done)break;buf+=dec.decode(value,{stream:true});const lines=buf.split('\n');buf=lines.pop();
      for(const line of lines){if(!line.startsWith('data: '))continue;try{const d=JSON.parse(line.slice(6));
        if(d.type==='init'){$('#syncTotal').textContent=d.total;finalInfo.total=d.total;}
        if(d.type==='result'){const dn=(d.ok||0)+(d.fail||0);finalInfo={ok:d.ok||0,fail:d.fail||0,total:d.total||finalInfo.total,elapsed:finalInfo.elapsed};$('#syncDone').textContent=dn;$('#syncOk').textContent=d.ok||0;$('#syncFail').textContent=d.fail||0;$('#syncBarFill').style.width=`${(dn/d.total*100).toFixed(1)}%`;addLog(`${d.mailNo} -> ${d.data?.status||'ok'}`,'ok');applySyncResultToState(d.mailNo,d.data,false);}
        if(d.type==='error'){const dn=(d.ok||0)+(d.fail||0),cat=classifySyncError(d.error);state.syncErrors[cat]=(state.syncErrors[cat]||0)+1;renderSyncErrorStats();finalInfo={ok:d.ok||0,fail:d.fail||0,total:d.total||finalInfo.total,elapsed:finalInfo.elapsed};$('#syncDone').textContent=dn;$('#syncOk').textContent=d.ok||0;$('#syncFail').textContent=d.fail||0;$('#syncBarFill').style.width=`${(dn/d.total*100).toFixed(1)}%`;addLog(`${d.mailNo} -> ${d.error}`,'fail');applySyncResultToState(d.mailNo,null,true,d.error);}
        if(d.type==='complete'){const ok=d.ok||0,fail=d.fail||0,elapsed=d.elapsed||'0';completed=true;finalInfo={ok,fail,total:d.total||0,elapsed};$('#syncBarFill').style.width='100%';$('#syncTitle').textContent=`同步完成: ${ok}成功 ${fail}失败`;toast(`同步完成: ${ok}成功 ${fail}失败 ${elapsed}秒`,'ok');}
      }catch{}}
    }
  }catch(e){if(e.message==='__NON_STREAM_DONE__'){}else if(e.name==='AbortError'){toast('已停止同步','info');$('#syncTitle').textContent='同步已停止';}else{toast('同步失败: '+e.message,'err');}}
  clearInterval(timer);$('#syncTime').textContent=((Date.now()-t0)/1000).toFixed(0)+'s';state.syncing=false;state.syncAbort=null;updateSyncButtons();state.lastSyncTime=Date.now();updateLastSyncInfo();pushBatch(title,finalInfo.ok,finalInfo.fail,finalInfo.total,finalInfo.elapsed,!completed);refreshData({meta:true});
}

$('#btnSyncSelected').addEventListener('click',()=>{const ids=[...state.selectedIds];if(!ids.length)return;state.selectedIds.clear();runSync('/api/sync',syncBody({ids}),`同步选中 ${ids.length} 条...`);});
$('#btnSyncMonitor').addEventListener('click',()=>{runSync('/api/sync/monitoring',syncBody(),'同步全部记录...');});
$('#btnSyncNoTracking').addEventListener('click',()=>runSync('/api/sync/filter',currentFilterBody('noTracking'),'同步无轨迹...'));
$('#btnSyncFailed').addEventListener('click',()=>runSync('/api/sync/filter',currentFilterBody('failed'),'同步失败记录...'));
$('#btnSyncCurrent').addEventListener('click',()=>runSync('/api/sync/filter',currentFilterBody('current'),'同步当前筛选...'));
$('#btnStopSync').addEventListener('click',()=>{if(state.syncAbort)state.syncAbort.abort();});

// Batch view, columns and auto sync
function visibleGrid(){return COLUMN_DEFS.filter(c=>state.columns.includes(c[0])).map(c=>c[1]).join(' ');}
function applyColumnVisibility(){
  const grid=visibleGrid();
  document.querySelectorAll('.table-head,.table-row').forEach(el=>{el.style.gridTemplateColumns=grid;});
  for(const [key] of COLUMN_DEFS){document.querySelectorAll('.col-'+key).forEach(el=>{el.style.display=state.columns.includes(key)?'':'none';});}
}
function renderColumnOptions(){
  $('#columnOptions').innerHTML=COLUMN_DEFS.filter(c=>!['select','expand'].includes(c[0])).map(([key,,label])=>`<label class="column-option"><input type="checkbox" value="${key}" ${state.columns.includes(key)?'checked':''}> ${label}</label>`).join('');
}
function saveColumns(){
  const fixed=['select','expand'];
  const picked=[...document.querySelectorAll('#columnOptions input:checked')].map(x=>x.value);
  state.columns=[...new Set([...fixed,...picked])];
  localStorage.setItem(COLUMN_KEY,JSON.stringify(state.columns));
  renderTable(state.records);applyColumnVisibility();
}
function renderBatchView(){
  const box=$('#batchViewList');
  if(!state.syncBatches.length){box.innerHTML='<div class="empty-state" style="padding:24px">暂无同步批次</div>';return;}
  box.innerHTML=state.syncBatches.map(b=>`<div class="batch-item"><div class="batch-item-head"><span>${esc(b.title)}</span><span>${esc(b.time)}</span></div><div class="batch-item-meta"><span>总数 ${b.total||0}</span><span>成功 ${b.ok||0}</span><span>失败 ${b.fail||0}</span><span>耗时 ${esc(b.elapsed||'0')}s</span>${b.aborted?'<span style="color:var(--warn)">已停止</span>':''}</div>${Object.keys(b.errors||{}).length?'<div class="batch-error-line">'+Object.entries(b.errors).map(([k,v])=>`${esc(k)} ${v}`).join(' / ')+'</div>':''}</div>`).join('');
}
function scheduleAutoSync(){
  if(state.autoSync.timer){clearInterval(state.autoSync.timer);state.autoSync.timer=null;}
  if(!state.autoSync.enabled)return;
  const ms=Math.max(5,state.autoSync.minutes||30)*60000;
  state.autoSync.timer=setInterval(()=>{if(!state.syncing&&!state.importing)runSync('/api/sync/monitoring',syncBody(),'自动同步全部记录...');},ms);
}
$('#btnBatchView').addEventListener('click',()=>{renderBatchView();$('#batchViewModal').classList.remove('hidden');});
$('#batchViewClose').addEventListener('click',()=>$('#batchViewModal').classList.add('hidden'));
$('#batchViewOk').addEventListener('click',()=>$('#batchViewModal').classList.add('hidden'));
$('#btnColumns').addEventListener('click',()=>{renderColumnOptions();$('#columnsModal').classList.remove('hidden');});
$('#columnsClose').addEventListener('click',()=>$('#columnsModal').classList.add('hidden'));
$('#columnsOk').addEventListener('click',()=>{saveColumns();$('#columnsModal').classList.add('hidden');});
$('#columnsReset').addEventListener('click',()=>{state.columns=[...DEFAULT_COLUMNS];localStorage.setItem(COLUMN_KEY,JSON.stringify(state.columns));renderColumnOptions();renderTable(state.records);applyColumnVisibility();});

// Theme toggle
const THEME_KEY='logistics-theme';
const sunPath='M6.76 4.84l-1.8-1.79-1.41 1.41 1.79 1.79zM4 10.5H1v2h3zm9-9.95h-2V3.5h2zm7.45 3.91l-1.41-1.41-1.79 1.79 1.41 1.41zM17.24 18.16l1.79 1.8 1.41-1.41-1.8-1.79zM20 10.5v2h3v-2zm-8-5a6 6 0 1 0 0 12 6 6 0 0 0 0-12zm-1 16.95h2V19.5h-2zm-7.45-3.91l1.41 1.41 1.79-1.8-1.41-1.41z';
const moonPath='M12 3a9 9 0 1 0 9 9c0-.46-.04-.92-.1-1.36a5.389 5.389 0 0 1-4.4 2.26 5.403 5.403 0 0 1-3.14-9.8c-.44-.06-.9-.1-1.36-.1z';
function applyTheme(dark){
  document.documentElement.setAttribute('data-theme',dark?'dark':'');
  const icon=$('#themeIcon');if(icon)icon.innerHTML=`<path d="${dark?sunPath:moonPath}"/>`;
}
(function initTheme(){const saved=localStorage.getItem(THEME_KEY);const dark=saved==='dark'||(saved===null&&window.matchMedia('(prefers-color-scheme: dark)').matches);applyTheme(dark);})();
$('#themeToggle').addEventListener('click',()=>{const isDark=document.documentElement.getAttribute('data-theme')==='dark';const next=!isDark;localStorage.setItem(THEME_KEY,next?'dark':'light');applyTheme(next);});

// Tag management
let _pendingTags=[];
function renderExistingTags(){
  $('#existingTags').innerHTML=state.tags.map(t=>`<span class="tag-existing" data-tag="${esc(t.name)}">${esc(t.name)}<span style="opacity:.5;font-size:9px">(${t.count})</span></span>`).join('');
}
function renderSelectedTags(){
  $('#tagSelectedList').innerHTML=_pendingTags.map(t=>`<span class="tag-selected">${esc(t)}<span class="tag-remove" data-tag="${esc(t)}">&times;</span></span>`).join('');
}
$('#btnBatchTag').addEventListener('click',async()=>{
  const c=state.selectedIds.size;if(!c)return;
  $('#batchTagCount').textContent=c;_pendingTags=[];
  try{state.tags=await api.getTags();}catch{}
  renderExistingTags();renderSelectedTags();$('#tagInput').value='';
  $('#batchTagModal').classList.remove('hidden');
});
$('#batchTagClose').addEventListener('click',()=>$('#batchTagModal').classList.add('hidden'));
$('#batchTagCancel').addEventListener('click',()=>$('#batchTagModal').classList.add('hidden'));
$('#existingTags').addEventListener('click',e=>{
  const el=e.target.closest('.tag-existing');if(!el)return;
  const tag=el.dataset.tag;if(!_pendingTags.includes(tag)){_pendingTags.push(tag);renderSelectedTags();}
});
$('#tagAddBtn').addEventListener('click',()=>{
  const tag=$('#tagInput').value.trim();if(!tag)return;
  if(!_pendingTags.includes(tag)){_pendingTags.push(tag);renderSelectedTags();}
  $('#tagInput').value='';
});
$('#tagInput').addEventListener('keydown',e=>{if(e.key==='Enter'){e.preventDefault();$('#tagAddBtn').click();}});
$('#tagSelectedList').addEventListener('click',e=>{
  const rm=e.target.closest('.tag-remove');if(!rm)return;
  _pendingTags=_pendingTags.filter(t=>t!==rm.dataset.tag);renderSelectedTags();
});
$('#batchTagSave').addEventListener('click',async()=>{
  const ids=[...state.selectedIds];if(!ids.length)return;
  const tags=_pendingTags.join(',');
  try{await api.batchTag(ids,'set',tags);$('#batchTagModal').classList.add('hidden');toast(`已更新${ids.length}条标签`,'ok');refreshData({meta:true});}catch{toast('标签更新失败','err');}
});

// Operation logs
$('#btnLogs').addEventListener('click',async()=>{
  try{
    const logs=await api.getLogs(100);
    $('#logTableBody').innerHTML=logs.map(l=>`<tr style="border-bottom:1px solid var(--border)"><td style="padding:4px 6px;white-space:nowrap">${esc(l.created_at||'')}</td><td style="padding:4px 6px">${esc(l.action||'')}</td><td style="padding:4px 6px">${esc(l.detail||'')}</td><td style="padding:4px 6px;text-align:right">${l.count||0}</td></tr>`).join('')||'<tr><td colspan="4" style="text-align:center;padding:16px;color:var(--text3)">暂无日志</td></tr>';
    $('#logModal').classList.remove('hidden');
  }catch{toast('加载日志失败','err');}
});
$('#logModalClose').addEventListener('click',()=>$('#logModal').classList.add('hidden'));
$('#logModalOk').addEventListener('click',()=>$('#logModal').classList.add('hidden'));

// Dashboard
let _charts={};
function destroyCharts(){for(const k of Object.keys(_charts)){if(_charts[k]){_charts[k].destroy();delete _charts[k];}}}
const STATUS_COLORS=['#94a3b8','#7c3aed','#0891b2','#2563eb','#f59e0b','#16a34a','#dc2626','#64748b'];
const STATUS_NAMES=['无轨迹','待揽收','已揽收','运输中','派送中','已签收','异常','待取件'];

$('#btnDashboard').addEventListener('click',async()=>{
  destroyCharts();
  $('#dashboardModal').classList.remove('hidden');
  try{
    const data=await(await fetch('/api/dashboard')).json();
    const isDark=document.documentElement.getAttribute('data-theme')==='dark';
    const gridColor=isDark?'rgba(255,255,255,.08)':'rgba(0,0,0,.06)';
    const textColor=isDark?'#94a3b8':'#64748b';
    Chart.defaults.color=textColor;Chart.defaults.borderColor=gridColor;

    _charts.status=new Chart($('#chartStatus'),{type:'doughnut',data:{labels:data.byStatus.map(s=>STATUS_NAMES[s.code]||'未知'),datasets:[{data:data.byStatus.map(s=>s.c),backgroundColor:data.byStatus.map(s=>STATUS_COLORS[s.code]||'#ccc'),borderWidth:0}]},options:{responsive:true,plugins:{legend:{position:'right',labels:{boxWidth:10,font:{size:10}}}}}});

    _charts.carrier=new Chart($('#chartCarrier'),{type:'pie',data:{labels:data.byCarrier.map(c=>c.carrier_name),datasets:[{data:data.byCarrier.map(c=>c.count),backgroundColor:['#3b82f6','#8b5cf6','#06b6d4','#10b981','#f59e0b','#ef4444','#ec4899','#6366f1','#14b8a6','#f97316'],borderWidth:0}]},options:{responsive:true,plugins:{legend:{position:'right',labels:{boxWidth:10,font:{size:10}}}}}});

    _charts.daily=new Chart($('#chartDaily'),{type:'bar',data:{labels:data.byDate.map(d=>d.d?d.d.slice(5):''),datasets:[{label:'导入量',data:data.byDate.map(d=>d.c),backgroundColor:isDark?'rgba(59,130,246,.5)':'rgba(37,99,235,.6)',borderRadius:3}]},options:{responsive:true,plugins:{legend:{display:false}},scales:{x:{grid:{display:false}},y:{beginAtZero:true,ticks:{stepSize:1}}}}});
  }catch{toast('加载仪表盘失败','err');}
});
$('#dashboardClose').addEventListener('click',()=>{$('#dashboardModal').classList.add('hidden');destroyCharts();});
$('#dashboardOk').addEventListener('click',()=>{$('#dashboardModal').classList.add('hidden');destroyCharts();});

loadSettings().finally(()=>{scheduleAutoSync();refreshData({meta:true});});
