package fleetsync

import (
	"io"
	"net/http"
)

func serveUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, fleetPage)
}

const fleetPage = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>KubeAtlas · Fleet Manager</title>
<style>
:root{color-scheme:light;--bg-base:#f0f3f9;--bg-surface:#f8fbfe;--bg-elevated:#e6ecf5;--text-primary:#1a1c20;--text-secondary:#555860;--text-tertiary:#6b7080;--border-default:#d8e0ee;--accent:#4a7cc9;--accent-muted:rgba(74,124,201,.12);--warning-text:#92400e}
*{box-sizing:border-box}body{margin:0;background:var(--bg-base);color:var(--text-primary);font:14px/1.55 'DM Sans',system-ui,sans-serif}button,input,select{font:inherit}button{cursor:pointer;border:1px solid var(--border-default);border-radius:8px;padding:7px 13px;background:var(--bg-surface);color:var(--text-primary)}button:hover{background:var(--bg-elevated)}button:disabled{cursor:wait;opacity:.55}.primary{background:var(--accent);color:white;border-color:var(--accent)}.primary:hover{background:#3b66a8}button:focus-visible,input:focus-visible,select:focus-visible{outline:2px solid var(--accent);outline-offset:3px}input,select{width:100%;border:1px solid var(--border-default);border-radius:7px;background:var(--bg-surface);padding:8px 10px;color:var(--text-primary)}input[type=checkbox]{width:auto;accent-color:var(--accent)}h1,h2,h3,p{margin:0}h1{font-size:26px;font-weight:650}h2{font-size:17px}h3{font-size:14px}code,pre{font-family:'DM Mono',ui-monospace,monospace}code{font-size:12px}.muted{color:var(--text-tertiary)}.small{font-size:12px}.row{display:flex;align-items:center;gap:10px}.between{justify-content:space-between}.wrap{flex-wrap:wrap}.stack{display:grid;gap:14px}.shell{display:grid;grid-template-columns:225px minmax(0,1fr);min-height:100vh}aside{padding:28px 20px;background:var(--bg-surface);border-right:1px solid var(--border-default);display:flex;flex-direction:column;gap:32px}.brand{font-size:22px;font-weight:700;letter-spacing:-.6px}.brand-mark{background:var(--accent);color:white;border-radius:9px;width:32px;height:32px;display:grid;place-content:center;font-size:19px}.nav{background:var(--accent-muted);color:#3b66a8;padding:10px 12px;border-radius:8px;font-weight:600}.aside-foot{margin-top:auto;overflow-wrap:anywhere}.aside-foot code{display:block;margin-top:8px}main{padding:30px;max-width:1600px;width:100%;margin:auto}header{margin-bottom:22px}.notice{margin:18px 0;padding:10px 14px;border-left:3px solid #f59e0b;background:#fff9ec;color:var(--warning-text);border-radius:0 6px 6px 0}.stats{display:grid;grid-template-columns:repeat(3,1fr);gap:16px;margin-bottom:22px}.stat,.card{background:var(--bg-surface);border:1px solid var(--border-default);border-radius:14px}.stat{padding:16px 20px}.stat strong{display:block;font-size:28px;font-weight:600;margin:3px 0}.card{padding:20px;min-width:0}.columns{display:grid;grid-template-columns:minmax(0,1.55fr) minmax(260px,1fr);gap:20px;align-items:start}.table-wrap{overflow:auto;margin-top:18px}table{border-collapse:collapse;width:100%;text-align:left;white-space:nowrap}th{font-size:12px;font-weight:500;color:var(--text-tertiary);padding:9px 8px;border-bottom:1px solid var(--border-default)}td{padding:13px 8px;border-bottom:1px solid var(--border-default);font-size:13px}tbody tr:last-child td{border-bottom:0}.empty{padding:30px 8px;color:var(--text-tertiary);text-align:center}.group{border:1px solid var(--border-default);border-radius:9px;padding:12px;margin-top:12px}.group .members{margin:8px 0;color:var(--text-secondary);overflow-wrap:anywhere}.label{display:grid;gap:5px;font-size:12px;color:var(--text-secondary)}.form-grid{display:grid;grid-template-columns:1fr 1fr;gap:12px}.members-picker{display:grid;gap:6px;max-height:180px;overflow:auto;padding:10px;background:var(--bg-base);border-radius:8px}.member{display:flex;gap:8px;align-items:center;font-size:13px}.context{margin-top:22px}.context-controls{display:grid;grid-template-columns:1fr auto;align-items:end;gap:12px}.context-body{display:grid;grid-template-columns:250px minmax(0,1fr);gap:18px;margin-top:18px}.documents{max-height:540px;overflow:auto}.doc-button{width:100%;display:block;text-align:left;margin-top:6px;padding:9px 10px;overflow-wrap:anywhere;font-size:12px}.doc-button.active{background:var(--accent-muted);border-color:var(--accent)}pre{font-size:12px;line-height:1.65;white-space:pre-wrap;overflow-wrap:anywhere;margin:0;max-height:480px;overflow:auto;padding:16px;background:var(--bg-base);border-radius:9px;border:1px solid var(--border-default)}.cluster-heading{border-bottom:1px solid var(--border-default);padding:10px 0 6px;margin-top:8px}.payload-header{margin-bottom:10px}.resource-list{max-height:180px;overflow:auto;margin:12px 0}.resource-list button{margin:4px;font-size:12px}.status{min-height:22px;margin-top:12px;color:var(--text-secondary)}.status.error{color:#b91c1c}.status.ok{color:#166534}details{font-size:12px;color:var(--text-secondary)}summary{cursor:pointer}#group-form{padding-top:16px;margin-top:16px;border-top:1px solid var(--border-default)}[hidden]{display:none!important}
@media(max-width:1100px){.shell{grid-template-columns:180px minmax(0,1fr)}main{padding:22px}.columns{grid-template-columns:1fr}.context-body{grid-template-columns:210px minmax(0,1fr)}}@media(max-width:700px){.shell{display:block}aside{padding:14px 20px;flex-direction:row;justify-content:space-between;border-right:0;border-bottom:1px solid var(--border-default)}aside .aside-foot,aside .subtitle{display:none}.nav{font-size:12px}main{padding:18px}.stats{gap:8px}.stat{padding:12px}.stat strong{font-size:23px}.context-body{grid-template-columns:1fr}.documents{max-height:210px}.context-controls{grid-template-columns:1fr}.form-grid{grid-template-columns:1fr}h1{font-size:22px}}
</style>
</head>
<body>
<div class="shell">
<aside>
  <div><div class="brand row"><span class="brand-mark" aria-hidden="true">K</span>KubeAtlas</div><p class="muted small subtitle" style="margin-top:10px">集群事实 · 统一上下文</p></div>
  <div class="nav">Fleet Manager</div>
  <div class="aside-foot muted small">Agent 接入点<code>/mcp-fleet</code><p style="margin-top:14px">数据由各 KubeAtlas 主动同步。中心无需 Kubernetes 凭据。</p></div>
</aside>
<main>
<header class="row between wrap"><div><p class="muted small">集中式管理平台</p><h1>多云集群与 Agent 上下文</h1></div><button id="refresh">刷新数据</button></header>
<div class="notice small">功能开发预览 · 尚未实现认证、权限与租户隔离。仅用于可信的隔离开发环境，不适用于公网或生产。</div>
<section class="stats" aria-label="同步概览">
 <div class="stat"><span class="muted small">已同步集群</span><strong id="cluster-count">—</strong><span class="small muted">独立的集群数据源</span></div>
 <div class="stat"><span class="muted small">多云分组</span><strong id="group-count">—</strong><span class="small muted">由中心定义上下文范围</span></div>
 <div class="stat"><span class="muted small">快照已过期</span><strong id="stale-count">—</strong><span class="small muted">超过 3 分钟未采集或接收</span></div>
</section>
<div class="columns">
 <section class="card"><div class="row between"><h2>集群数据源</h2><span class="small muted">周期性快照</span></div><p class="small muted" style="margin-top:6px">单集群保留 KubeAtlas 原始数据语义；同步状态不代表集群健康。</p>
  <div class="table-wrap"><table><thead><tr><th>集群 / ID</th><th>快照状态</th><th>采集时间</th><th>上下文</th></tr></thead><tbody id="clusters"></tbody></table></div>
  <div id="cluster-empty" class="empty" hidden>尚无集群数据。配置 KubeAtlas 的 KUBEATLAS_FLEET_URL 和 KUBEATLAS_FLEET_CLUSTER_ID 后开始同步。</div>
 </section>
 <section class="card"><div class="row between"><h2>多云集群分组</h2><button id="new-group">新建分组</button></div><p class="small muted" style="margin-top:6px">分组决定 Agent 可以一起查看哪些集群。</p><div id="groups"></div>
  <form id="group-form" class="stack" hidden>
   <h3 id="form-title">新建分组</h3>
   <div class="form-grid"><label class="label">分组 ID<input id="group-id" required pattern="[a-z0-9][a-z0-9-]{0,62}" placeholder="production"></label><label class="label">显示名称<input id="group-name" required maxlength="256" placeholder="生产多云集群"></label></div>
   <div><p class="small muted" style="margin-bottom:6px">选择成员集群</p><div id="member-picker" class="members-picker"></div></div>
   <div class="row"><button class="primary" type="submit">保存分组</button><button type="button" id="cancel-group">取消</button></div>
  </form>
 </section>
</div>
<section class="card context">
 <div class="row between wrap"><div><h2>Agent 上下文预览</h2><p class="small muted" style="margin-top:6px">原始模型与关系按集群保留，不将同名资源合并，不推断跨云连通性。</p></div><code class="muted">get_agent_context</code></div>
 <div class="context-controls" style="margin-top:18px"><label class="label">上下文范围<select id="scope"><option value="">选择一个单集群或多云分组</option></select></label><button id="load-context" class="primary">生成上下文</button></div>
 <div id="context-empty" class="empty">选择范围，查看提供给 Agent 的同步数据。</div>
 <div id="context-body" class="context-body" hidden>
  <div><p id="context-mode" class="small muted"></p><div id="documents" class="documents"></div></div>
  <div><div class="payload-header row between wrap"><h3 id="payload-title">上下文</h3><button id="show-envelope">完整上下文 JSON</button></div><p id="payload-source" class="small muted" style="margin-bottom:10px"></p><div id="resource-list" class="resource-list" hidden></div><pre id="payload" tabindex="0"></pre><details style="margin-top:12px"><summary>数据覆盖与限制</summary><ul id="limitations"></ul></details></div>
 </div>
</section>
<p id="status" class="status" role="status" aria-live="polite"></p>
</main></div>
<script>
'use strict';
const $ = id => document.getElementById(id);
let clusters = [], groups = [], currentContext = null, activeScope = '', requestVersion = 0;
function node(tag, text, className) { const el = document.createElement(tag); if (text !== undefined) el.textContent = text; if(className) el.className = className; return el; }
function status(message, error) { $('status').textContent = message; $('status').className = 'status ' + (error ? 'error' : 'ok'); }
async function api(path, method, body) { const response = await fetch(path, {method:method || 'GET', headers:body === undefined ? {} : {'Content-Type':'application/json'}, body:body === undefined ? undefined : JSON.stringify(body)}); const data = await response.json(); if (!response.ok) throw new Error(data.error || '请求失败'); return data; }
function action(label, fn, className) { const button = node('button', label, className); button.type = 'button'; button.onclick = () => Promise.resolve().then(fn).catch(error => status(error.message, true)); return button; }
function stamp(time) { const date = new Date(time); return Number.isNaN(date.getTime()) || date.getFullYear() < 2000 ? '无快照' : date.toLocaleString('zh-CN', {hour12:false}); }
function stateLabel(state) { return state === 'current' ? '近期采集' : state === 'stale' ? '快照过期' : '数据不可用'; }
function resetContext() { requestVersion++; currentContext = null; activeScope = ''; $('context-body').hidden = true; $('context-empty').hidden = false; $('documents').replaceChildren(); $('payload').textContent = ''; }
function rebuildScope() { const old = $('scope').value; $('scope').replaceChildren(node('option','选择一个单集群或多云分组')); $('scope').firstChild.value = ''; for(const [items,type,title] of [[groups,'fleet','多云分组'],[clusters,'cluster','单集群']]) { if(!items.length) continue; const section = node('optgroup'); section.label = title; for(const item of items) { const option = node('option', item.name + ' · ' + (item.id || item.clusterId)); option.value = type + ':' + (item.id || item.clusterId); section.append(option); } $('scope').append(section); } if([...$('scope').options].some(option=>option.value === old)) $('scope').value = old; }
async function refresh() {
 const values = await Promise.all([api('/api/clusters'),api('/api/fleets')]); clusters = values[0]; groups = values[1];
 $('cluster-count').textContent = clusters.length; $('group-count').textContent = groups.length; $('stale-count').textContent = clusters.filter(cluster=>cluster.state === 'stale').length;
 $('clusters').replaceChildren(); $('cluster-empty').hidden = clusters.length > 0;
 for(const cluster of clusters) { const tr = node('tr'); const title = node('td'); title.append(node('strong',cluster.name),node('div',cluster.clusterId,'muted small')); const time = node('td', stamp(cluster.observedAt), 'small muted'); time.title = '中心接收：' + stamp(cluster.receivedAt); const target = node('td'); target.append(action('查看',()=>selectScope('cluster:'+cluster.clusterId))); tr.append(title,node('td',stateLabel(cluster.state)),time,target); $('clusters').append(tr); }
 $('groups').replaceChildren(); if(!groups.length) $('groups').append(node('p','尚未创建分组。单集群可独立查看。','empty'));
 for(const group of groups) { const card = node('div',undefined,'group'); const heading = node('div',undefined,'row between'); heading.append(node('strong',group.name),node('code',group.id,'muted')); const members = node('p',group.clusterIds.length ? group.clusterIds.join(' / ') : '空分组，尚未指定成员','members small'); const controls = node('div',undefined,'row wrap'); controls.append(action('预览上下文',()=>selectScope('fleet:'+group.id)),action('编辑',()=>editGroup(group)),action('删除',async()=>{ if(!confirm('删除分组 '+group.name+'？不会删除集群快照。')) return; await api('/api/fleets/'+encodeURIComponent(group.id),'DELETE'); resetContext(); await refresh(); status('分组已删除，集群数据保留。'); })); card.append(heading,members,controls); $('groups').append(card); }
 rebuildScope();
}
function editGroup(group) {
 $('group-form').hidden = false; $('form-title').textContent = group ? '编辑分组成员' : '新建分组'; $('group-id').value = group ? group.id : ''; $('group-id').readOnly = !!group; $('group-name').value = group ? group.name : ''; $('member-picker').replaceChildren();
 const choices = new Map(clusters.map(cluster=>[cluster.clusterId,cluster.name])); if(group) for(const id of group.clusterIds) if(!choices.has(id)) choices.set(id,id+'（尚无数据）');
 for(const [id,name] of choices) { const label = node('label',undefined,'member'); const input = node('input'); input.type = 'checkbox'; input.value = id; input.checked = !!group && group.clusterIds.includes(id); label.append(input,node('span',name+' · '+id)); $('member-picker').append(label); }
 if(!choices.size) $('member-picker').append(node('p','尚无可选集群，请先接入数据源。','muted small'));
 $('group-name').focus();
}
async function selectScope(value) { $('scope').value = value; await loadContext(); $('context-body').scrollIntoView({behavior:'smooth',block:'nearest'}); }
async function loadContext() {
 const value = $('scope').value; if(!value) { status('请先选择一个集群或分组。',true); return; }
 resetContext(); const version = requestVersion; const parts = value.split(':'); const selection = parts[0] === 'fleet' ? {fleetId:parts[1]} : {clusterId:parts[1]}; $('load-context').disabled = true;
 try { const result = await api('/api/context','POST',selection); if(version !== requestVersion) return; currentContext = result; activeScope = value; renderContext(); status('已生成同步上下文；这不是实时 Kubernetes 查询。'); }
 finally { $('load-context').disabled = false; }
}
function renderContext() {
 $('context-empty').hidden = true; $('context-body').hidden = false; $('documents').replaceChildren(); $('context-mode').textContent = currentContext.mode === 'single' ? '单集群 · 原始 KubeAtlas 数据' : '多云分组 · '+currentContext.clusters.length+' 个成员';
 $('limitations').replaceChildren(); for(const text of currentContext.limitations || []) $('limitations').append(node('li',text));
 for(const cluster of currentContext.clusters) {
  const heading = node('div',undefined,'cluster-heading'); heading.append(node('strong',cluster.name || cluster.clusterId),node('div',cluster.clusterId+' · '+stateLabel(cluster.state),'muted small')); $('documents').append(heading);
  for(const text of cluster.limitations || []) $('limitations').append(node('li',cluster.clusterId+': '+text));
  for(const doc of cluster.documents || []) { const args = doc.input || {}; const label = doc.tool + (args.kind ? ' / '+args.kind+(args.group ? '.'+args.group : '') : args.view ? ' / '+args.view : ''); const button = action(label,()=>{ for(const item of $('documents').querySelectorAll('button')) item.classList.remove('active'); button.classList.add('active'); showDocument(cluster,doc); },'doc-button'); $('documents').append(button); }
 }
 showEnvelope();
}
function showEnvelope() { $('payload-title').textContent = '完整上下文 JSON'; $('payload-source').textContent = '每个 clusterId 下的数据独立保留；原始引用仅在所属集群内解析。'; $('resource-list').hidden = true; $('payload').textContent = JSON.stringify(currentContext,null,2); }
function showDocument(cluster,doc) {
 requestVersion++;
 $('payload-title').textContent = doc.tool; $('payload-source').textContent = cluster.clusterId+' · '+stateLabel(cluster.state)+' · 采集 '+stamp(cluster.observedAt); $('payload').textContent = JSON.stringify(doc.data === undefined ? {error:doc.error} : doc.data,null,2); $('resource-list').replaceChildren(); $('resource-list').hidden = true;
 if(doc.tool === 'list_resources' && doc.data && Array.isArray(doc.data.items)) {
  $('resource-list').hidden = false;
  for(const item of doc.data.items) { if(!item.name) continue; $('resource-list').append(action((item.namespace ? item.namespace+'/' : '')+item.name, async()=>{
   const version = ++requestVersion; const result = await api('/api/resource-context','POST',{clusterId:cluster.clusterId,kind:doc.input.kind,group:doc.input.group || '',namespace:item.namespace || '',name:item.name});
   if(version !== requestVersion || activeScope !== $('scope').value) return;
   const detail = result.documents[0]; $('payload-title').textContent = 'get_resource · '+item.name; $('payload-source').textContent = result.clusterId+' · '+stateLabel(result.state)+' · 采集 '+stamp(result.observedAt); $('payload').textContent = JSON.stringify(detail.data === undefined ? {error:detail.error} : detail.data,null,2);
  })); }
 }
}
$('new-group').onclick = ()=>editGroup(null); $('cancel-group').onclick = ()=>$('group-form').hidden = true;
$('scope').onchange = resetContext; $('show-envelope').onclick = ()=>{requestVersion++;showEnvelope();};
$('refresh').onclick = async()=>{ $('refresh').disabled = true; try { resetContext(); await refresh(); status('数据已刷新。'); } catch(error) {status(error.message,true);} finally {$('refresh').disabled = false;} };
$('load-context').onclick = ()=>loadContext().catch(error=>status(error.message,true));
$('group-form').onsubmit = async event=>{ event.preventDefault(); const button = event.submitter; button.disabled = true; try { const id = $('group-id').value.trim(); const group = {id:id,name:$('group-name').value.trim(),clusterIds:[...$('member-picker').querySelectorAll('input:checked')].map(input=>input.value)}; await api('/api/fleets/'+encodeURIComponent(id),'PUT',group); $('group-form').hidden = true; resetContext(); await refresh(); $('scope').value = 'fleet:'+id; status('分组已保存，下一次生成上下文将使用新成员。'); } catch(error) {status(error.message,true);} finally {button.disabled = false;} };
refresh().catch(error=>status(error.message,true));
</script>
</body></html>`
