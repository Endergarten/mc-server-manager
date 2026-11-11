'use strict';

const el = id => document.getElementById(id);
const html = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const statusNames = {stopped:'已停止', starting:'启动中', running:'运行中', stopping:'保存并停止中', failed:'已退出 / 失败'};
let data = null;
let selection = null;
let importStage = null;
let versions = [];
let versionsLoaded = false;
let settingsLoaded = false;
let polling = false;
let desktopEventsBound = false;
let pendingStart = null;
let serverSignature = '';
let javaSignature = '';
let coverRevision = 0;
let notificationTimer;
let draftAdmins = [];
const requirementCache = new Map();
const iconPaths={maps:'M4 5.5 9 4l6 2 5-1.5v14L15 20l-6-2-5 1.5z M9 4v14 M15 6v14',download:'M12 3v12 m-5-5 5 5 5-5 M4 17v4h16v-4',java:'M5 4h14v16H5z M8 9l3 3-3 3 M13 15h3',settings:'m9 4 .6-2h4.8l.6 2 2 .9 2-.5 2.4 4.2-1.4 1.5v2.2l1.4 1.5-2.4 4.2-2-.5-2 .9-.6 2H9.6l-.6-2-2-.9-2 .5-2.4-4.2L4 12.3v-2.2L2.6 8.6 5 4.4l2 .5z M15.5 11.2a3.5 3.5 0 1 1-7 0 3.5 3.5 0 0 1 7 0',add:'M12 5v14 M5 12h14',upload:'M12 16V4 m-5 5 5-5 5 5 M4 16v5h16v-5',edit:'m4 16-1 5 5-1L20 8l-4-4z M13 7l4 4',backup:'M4 5h16v5H4z M6 10v11h12V10 M10 14h4',delete:'M4 6h16 M9 6V3h6v3 M6 6l1 15h10l1-15 M10 10v7 M14 10v7'};
function initializeIcons(){document.querySelectorAll('[data-icon]').forEach(node=>{const path=iconPaths[node.dataset.icon];if(path)node.innerHTML=`<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.55" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="${path}"></path></svg>`;});}

async function api(path, body) {
  const response = await fetch('/api/' + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: body === undefined ? {} : {'Content-Type':'application/json', 'X-Manager-Token':data?.token || ''},
    body: body === undefined ? undefined : JSON.stringify(body)
  });
  let result;
  try { result = await response.json(); }
  catch { throw new Error('无法读取管理器响应，请重新打开应用。'); }
  if (!response.ok) throw new Error(result.error || '操作失败');
  return result;
}

function notify(message, error = false) {
  clearTimeout(notificationTimer);
  el('notification').textContent = message;
  el('notification').classList.toggle('error', error);
  el('notification').hidden = false;
  notificationTimer = setTimeout(() => el('notification').hidden = true, error ? 9000 : 5500);
}

function setBusy(message) {
  el('busy-overlay').hidden = !message;
  el('busy-label').textContent = message;
  el('busy-progress').removeAttribute('value');
}

function upload(path, file, message) {
  return new Promise((resolve, reject) => {
    setBusy(message);
    const request = new XMLHttpRequest();
    request.open('POST', '/api/' + path);
    request.setRequestHeader('X-Manager-Token', data?.token || '');
    request.upload.onprogress = event => {
      if (!event.lengthComputable) return;
      el('busy-progress').value = event.loaded / event.total * 100;
      if (event.loaded === event.total) {
        el('busy-label').textContent = '正在检查文件…';
        el('busy-progress').removeAttribute('value');
      }
    };
    request.onload = () => {
      setBusy('');
      try {
        const result = JSON.parse(request.responseText);
        if (request.status >= 200 && request.status < 300) resolve(result);
        else reject(new Error(result.error || '导入失败'));
      } catch { reject(new Error('无法读取导入结果')); }
    };
    request.onerror = () => { setBusy(''); reject(new Error('连接中断，文件未导入')); };
    const form = new FormData();
    form.append('file', file);
    request.send(form);
  });
}

function showView(name) {
  document.querySelectorAll('.view').forEach(view => view.classList.toggle('selected', view.id === 'view-' + name));
  document.querySelectorAll('[data-view]').forEach(button => button.classList.toggle('selected', button.dataset.view === name));
  if (name === 'versions' && !versionsLoaded) loadVersions();
}

function running() { return data && ['starting','running','stopping'].includes(data.server.state); }
function selectedServer() { return data?.worlds.find(world => world.id === selection); }
function selectedIsRunning() { return running() && data.server.worldId === selection; }

async function refresh() {
  if (polling) return;
  polling = true;
  try {
    data = await api('state');
    if (!data.worlds.some(world => world.id === selection)) selection = data.worlds[0]?.id || null;
    render();
    if (pendingStart) {
      const world = data.worlds.find(world => world.id === pendingStart);
      if (!world) pendingStart = null;
      else if (data.installed.includes(world.version)) {
        const id = pendingStart;
        pendingStart = null;
        await api('world/start', {id});
        showView('servers');
        notify('下载完成，正在启动 ' + world.name);
      } else {
        const active = data.tasks.some(task => task.version === world.version && ['queued','downloading'].includes(task.state));
        const failure = data.tasks.find(task => task.version === world.version && task.state === 'failed');
        if (!active && failure) { pendingStart = null; notify(failure.error, true); }
      }
    }
  } catch (error) {
    if (data) notify('与应用的连接已断开。', true);
    if (pendingStart) { pendingStart = null; notify(error.message, true); }
  } finally { polling = false; }
}

function render() {
  el('server-count').textContent = data.worlds.length;
  el('server-summary').textContent = data.worlds.length + ' 张地图';
  const signature = JSON.stringify([data.worlds, data.server, data.installed, selection, el('filter-servers').value, coverRevision]);
  if (signature !== serverSignature) { serverSignature = signature; renderServerRows(); }
  renderProperties();
  renderConsole();
  renderJava();
  renderDownloads();
  if (versionsLoaded) renderVersions();
  if (!settingsLoaded) {
    const settings = data.settings;
    el('java-mode').value = settings.javaMode || 'auto';
    el('manual-java').hidden = el('java-mode').value !== 'manual';
    el('memory').value = settings.memory;
    el('game-port').value = settings.port;
	const serverOptions=settings.serverOptions||{onlineMode:false,commandBlocks:true,allowFlight:true};
	el('online-mode').checked=serverOptions.onlineMode;el('command-blocks').checked=serverOptions.commandBlocks;el('allow-flight').checked=serverOptions.allowFlight;
    draftAdmins = [...(settings.operators || [])];
    renderAdmins();
    el('accept-eula').checked = settings.eula;
    el('data-location').textContent = data.dataPath;
    settingsLoaded = true;
  }
  bindDesktopEvents();
}

function renderServerRows() {
  const focused=document.activeElement?.closest?.('.map-row')?.dataset.id;
  const filter = el('filter-servers').value.trim().toLowerCase();
  const worlds = data.worlds.filter(world => [world.name, world.version, world.description].join(' ').toLowerCase().includes(filter));
  el('servers-empty').hidden = data.worlds.length > 0;
  el('server-rows').innerHTML = worlds.map(world => {
    const active = running() && data.server.worldId === world.id;
    const failed = data.server.worldId === world.id && data.server.state === 'failed';
    const preparing=data.tasks.some(task=>task.version===world.version&&['queued','downloading'].includes(task.state));
    return `<button type="button" data-id="${world.id}" class="map-row ${selection === world.id ? 'chosen' : ''}" role="option" aria-selected="${selection === world.id}"><span class="row-icon"><img src="/api/world/icon?id=${world.id}&v=${coverRevision}" alt="" loading="lazy"></span><span class="map-row-copy"><strong>${html(world.name)}</strong><span class="map-row-description">${html(world.description || '未填写简介')}</span><small>Minecraft ${html(world.version)}</small></span><span class="server-status ${active ? 'active' : failed ? 'failed' : ''}">${active ? statusNames[data.server.state] : failed ? '启动失败' : preparing ? '准备中' : '未开服'}</span></button>`;
  }).join('');
  document.querySelectorAll('.row-icon img').forEach(image => image.onerror = () => { image.parentElement.textContent = 'M'; });
  if(focused)el('server-rows').querySelector(`[data-id="${focused}"]`)?.focus();
}

function renderProperties() {
  const world = selectedServer();
  el('properties-empty').hidden = !!world;
  el('properties').hidden = !world;
  el('start-server').disabled = !world || running() || !!pendingStart;
  el('stop-server').disabled = !['starting','running'].includes(data.server.state);
  el('edit-server').disabled = !world || selectedIsRunning();
  el('backup-server').disabled = !world || selectedIsRunning();
  el('delete-map').disabled = !world || selectedIsRunning();
  if (!world) return;
  el('selected-name').textContent = world.name;
  el('selected-version').textContent = world.version;
  el('selected-state').textContent = selectedIsRunning() ? statusNames[data.server.state] : '未开服';
  el('selected-description').textContent = world.description || '未填写简介。';
  el('start-server').innerHTML = data.installed.includes(world.version) ? '<span>▶</span>开服' : '<span>↓</span>下载并开服';
  const coverKey = world.id + ':' + coverRevision;
  if (el('selected-cover').dataset.key !== coverKey) {
    el('selected-cover').dataset.key = coverKey;
    el('selected-cover').innerHTML = `<img src="/api/world/icon?id=${world.id}&v=${coverRevision}" alt="地图图片">`;
    el('selected-cover').firstChild.onerror = () => el('selected-cover').textContent = 'M';
  }
  if (data.settings.javaMode === 'manual') { el('selected-java').textContent = '手动固定'; return; }
  const key = world.version + ':' + (data.javas || []).map(java => java.path).join('|') + ':' + data.installed.join(',');
  const cached = requirementCache.get(key);
  if (cached) el('selected-java').textContent = cached;
  else {
    el('selected-java').textContent = '正在匹配…';
    requirementCache.set(key, '正在匹配…');
    api('java/requirement?version=' + encodeURIComponent(world.version)).then(result => {
      requirementCache.set(key, result.missing ? '缺少 Java ' + result.requirement.allowed.join(' / ') : 'Java ' + result.selected.version + '（自动）');
      if (selectedServer()?.version === world.version) renderProperties();
    }).catch(() => { requirementCache.set(key, '下载服务端后自动匹配'); if (selectedServer()?.version === world.version) renderProperties(); });
  }
}

function renderConsole() {
  const world = data.worlds.find(world => world.id === data.server.worldId);
  el('console-instance').textContent = world ? world.name + ' · ' + world.version : '未启动服务器';
  const log = el('console-log');
  const text = data.logs.length ? data.logs.join('\n') : '等待启动服务器。';
  if (log.textContent !== text) {
    const nearBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 40;
    log.textContent = text;
    if (nearBottom) log.scrollTop = log.scrollHeight;
  }
  el('console-send').disabled = !['starting','running'].includes(data.server.state);
}

function renderJava() {
  const found = data.javas || [];
  el('scan-java').disabled = data.javaScanning;
  el('scan-java').textContent = data.javaScanning ? '正在搜索…' : '重新搜索';
  el('java-scan-message').textContent = data.javaScanning ? '搜索中' : data.javaScanError || '共 ' + found.length + ' 个环境';
  el('java-empty').hidden = found.length > 0;
  el('java-roots').textContent = (data.settings.javaRoots || []).length ? '补充目录：' + data.settings.javaRoots.join('；') : '';
  el('java-rule-path').textContent = '兼容规则文件：' + data.dataPath + '/java-rules.json';
  document.querySelectorAll('.browse-java').forEach(button => { button.disabled = !data.desktop; button.title = data.desktop ? '打开本地文件浏览器' : '本地浏览功能需要桌面版'; });
  const signature = JSON.stringify([found, data.settings.java]);
  if (signature === javaSignature) return;
  javaSignature = signature;
  el('java-rows').innerHTML = found.map(java => `<tr><td><strong>Java ${html(java.version)}</strong><small>${html(java.vendor || java.source)}</small></td><td>${html(java.arch)}</td><td class="path-cell">${html(java.path)}</td></tr>`).join('');
  const pinned = el('pinned-java').value || data.settings.java;
  const choices = [...found];
  if (pinned && !choices.some(java => java.path === pinned)) choices.push({path:pinned, version:'已保存路径'});
  el('pinned-java').innerHTML = choices.map(java => `<option value="${html(java.path)}">Java ${html(java.version)} · ${html(java.path)}</option>`).join('');
  if (pinned) el('pinned-java').value = pinned;
}

async function loadVersions() {
  el('version-message').textContent = '正在读取 Mojang 官方版本列表…';
  try {
    versions = (await api('versions')).versions;
    versionsLoaded = true;
    el('version-message').textContent = '正式版、快照与候选版不会混用。';
    renderVersions();
  } catch (error) { el('version-message').textContent = error.message; notify(error.message, true); }
}

function renderVersions() {
  const filter = el('filter-versions').value.toLowerCase().trim();
  const snapshots = el('include-snapshots').checked;
  el('installed-list').innerHTML = data.installed.length ? data.installed.map(version => `<span class="installed-item">${html(version)}</span>`).join('') : '<span class="muted">没有已安装版本。</span>';
  const results = versions.filter(version => (snapshots || version.type === 'release') && version.id.toLowerCase().includes(filter));
  el('version-rows').innerHTML = results.slice(0, 120).map(version => {
    const installed = data.installed.includes(version.id);
    const downloading = data.tasks.some(task => task.version === version.id && ['queued','downloading'].includes(task.state));
    return `<tr><td>${html(version.id)}</td><td>${version.type === 'release' ? '正式版' : '测试版'}</td><td><button data-download="${html(version.id)}" ${installed || downloading ? 'disabled' : ''}>${installed ? '已安装' : downloading ? '下载中' : '下载'}</button></td></tr>`;
  }).join('');
  if (results.length > 120) el('version-message').textContent = '显示前 120 个结果，请搜索指定版本。';
}

function renderDownloads() {
  el('download-list').innerHTML = data.tasks.map(task => {
    const active = ['queued','downloading'].includes(task.state);
    const status = task.state === 'done' ? '完成，已通过校验' : task.state === 'failed' ? task.error : task.state === 'queued' ? '读取官方信息…' : `${(task.bytes / 1048576).toFixed(1)} / ${(task.total / 1048576).toFixed(1)} MB`;
    return `<div class="download-item">${html(task.version)}　${html(status)}${active ? `<progress max="${task.total || 1}" value="${task.bytes}"></progress>` : ''}</div>`;
  }).join('');
}

async function startSelected() {
  const world = selectedServer();
  if (!world || running()) return;
  if (!data.settings.eula) { showView('settings'); notify('请先阅读并同意 Minecraft EULA，然后保存设置。', true); return; }
  try {
    if (!data.installed.includes(world.version)) {
      await api('versions/download', {version:world.version});
      pendingStart = world.id;
      showView('versions');
      notify('下载完成后将自动启动 ' + world.name);
    } else { await api('world/start', {id:world.id}); showView('servers'); }
    await refresh();
  } catch (error) {
    if (error.message.includes('Java')) showView('java');
    notify(error.message, true);
  }
}

function showImport(stage) {
  if (importStage) api('import/cancel', {stage:importStage.id}).catch(() => {});
  importStage = stage;
  el('map-candidate').innerHTML = stage.candidates.map((candidate,index) => `<option value="${index}">${html(candidate.name)} — ${html(candidate.root)}</option>`).join('');
  fillCandidate();
  el('map-version').setAttribute('list','map-version-options');
  fillVersionOptions();
  if (!versionsLoaded) api('versions').then(result=>{versions=result.versions;versionsLoaded=true;fillVersionOptions();}).catch(()=>{});
  if (!el('import-dialog').open) el('import-dialog').showModal();
}

function fillVersionOptions() {
  const options = [...new Set([...(data?.installed || []), ...versions.map(version=>version.id)])];
  el('map-version-options').innerHTML = options.map(version=>`<option value="${html(version)}"></option>`).join('');
}
function renderAdmins() {
  el('admin-list').innerHTML = draftAdmins.length ? draftAdmins.map((name,index)=>`<div class="admin-row"><span class="admin-avatar">${html(name[0].toUpperCase())}</span><span>${html(name)}</span><button type="button" data-remove-admin="${index}" aria-label="移除 ${html(name)}">移除</button></div>`).join('') : '<div class="admin-empty">未添加管理员</div>';
}
function addAdmin() {
  const name = el('new-admin').value.trim();
  if (!/^[A-Za-z0-9_]{1,16}$/.test(name)) { notify('游戏名需为 1–16 位英文、数字或下划线。',true);return; }
  if (draftAdmins.some(value=>value.toLowerCase()===name.toLowerCase())) {notify('该管理员已在列表中。',true);return;}
  if (draftAdmins.length>=64) {notify('最多添加 64 个管理员。',true);return;}
  draftAdmins.push(name);el('new-admin').value='';renderAdmins();
}
function selectWorkspaceTab(name) {
  el('maps-panel').hidden=name!=='maps';el('console-panel').hidden=name!=='console';
  document.querySelectorAll('[data-workspace-tab]').forEach(button=>{const active=button.dataset.workspaceTab===name;button.classList.toggle('tab-selected',active);button.setAttribute('aria-selected',String(active));});
  if(name==='console') el('console-log').scrollTop=el('console-log').scrollHeight;
}

function fillCandidate() {
  const candidate = importStage.candidates[Number(el('map-candidate').value)];
  el('map-name').value = candidate.name;
  el('map-version').value = candidate.version;
  el('map-description').value = '';
  el('map-version-hint').textContent = candidate.version ? '识别来源：' + candidate.source : '未识别版本，请手动填写。';
}

async function importFile(file) {
  if (!file) return;
  if (!file.name.toLowerCase().endsWith('.zip')) { notify('请选择地图 ZIP 文件。', true); return; }
  try { showImport(await upload('import/inspect', file, '正在读取地图…')); }
  catch (error) { notify(error.message, true); }
  finally { el('map-file').value = ''; }
}

function bindDesktopEvents() {
  if (!data.desktop || desktopEventsBound || !window.runtime?.EventsOn) return;
  desktopEventsBound = true;
  window.runtime.EventsOn('manager:drop-progress', () => setBusy('正在读取地图 ZIP…'));
  window.runtime.EventsOn('manager:drop', result => {
    setBusy('');
    if (result.error) notify(result.error, true);
    else showImport(result.stage);
  });
  window.runtime.EventsEmit('manager:ready');
}

document.querySelectorAll('[data-view]').forEach(button => button.onclick = () => showView(button.dataset.view));
document.querySelectorAll('.close-dialog').forEach(button => button.onclick = () => el(button.dataset.dialog).close());
el('import-map').onclick = el('empty-import').onclick = () => { if (data) el('map-file').click(); };
el('map-file').onchange = event => importFile(event.target.files[0]);
el('map-candidate').onchange = fillCandidate;
el('filter-servers').oninput = () => { if (data) renderServerRows(); };
el('server-rows').onclick = event => { const row = event.target.closest('.map-row[data-id]'); if (row) { selection = row.dataset.id; render(); } };
el('server-rows').ondblclick = () => startSelected();
el('server-rows').onkeydown = event => {
  const rows=[...el('server-rows').querySelectorAll('.map-row')];const at=rows.findIndex(row=>row.dataset.id===selection);
  if(['ArrowDown','ArrowUp','Home','End'].includes(event.key)){event.preventDefault();const next=event.key==='Home'?0:event.key==='End'?rows.length-1:Math.max(0,Math.min(rows.length-1,at+(event.key==='ArrowDown'?1:-1)));if(rows[next]){selection=rows[next].dataset.id;render();el('server-rows').querySelector(`[data-id="${selection}"]`)?.focus();}}
  if(event.key==='Delete'&&!el('delete-map').disabled){event.preventDefault();el('delete-map').click();}
};
el('start-server').onclick = startSelected;
el('stop-server').onclick = async () => { try { await api('server/stop', {}); await refresh(); } catch (error) { notify(error.message, true); } };
el('console-form').onsubmit = async event => {
  event.preventDefault();
  const command = el('console-command').value.trim();
  if (!command) return;
  try { await api('server/command', {command}); el('console-command').value = ''; await refresh(); }
  catch (error) { notify(error.message, true); }
};
el('copy-log').onclick = async () => { try { await navigator.clipboard.writeText(el('console-log').textContent); notify('日志已复制。'); } catch { notify('无法访问剪贴板，可在控制台选择文本后复制。', true); } };
el('import-form').onsubmit = async event => {
  event.preventDefault();
  if (!importStage) return;
  try {
    const world = await api('import/commit', {stage:importStage.id, index:Number(el('map-candidate').value), name:el('map-name').value.trim(), version:el('map-version').value.trim(), description:el('map-description').value.trim()});
    importStage = null;
    selection = world.id;
    el('import-dialog').close();
    await refresh();
    showView('servers');
    if (!data.installed.includes(world.version)) {
      try { await api('versions/download',{version:world.version});notify('地图已导入，正在下载所选服务端版本。'); }
      catch(error) {notify('地图已导入，服务端下载未开始：'+error.message,true);}
    } else notify('地图已导入，可以开服。');
  } catch (error) { notify(error.message, true); }
};
el('import-dialog').onclose = () => { if (importStage) { api('import/cancel', {stage:importStage.id}).catch(() => {}); importStage = null; } };
el('edit-server').onclick = () => {
  const world = selectedServer(); if (!world) return;
  el('edit-id').value = world.id; el('edit-name').value = world.name; el('edit-version').value = world.version; el('edit-description').value = world.description;
  el('properties-dialog').showModal();
};
el('properties-form').onsubmit = async event => {
  event.preventDefault();
  try { await api('world/edit', {id:el('edit-id').value, name:el('edit-name').value.trim(), version:el('edit-version').value.trim(), description:el('edit-description').value.trim(), added:''}); el('properties-dialog').close(); requirementCache.clear(); await refresh(); }
  catch (error) { notify(error.message, true); }
};
el('choose-cover').onclick = () => el('cover-file').click();
el('cover-file').onchange = async event => {
  const file = event.target.files[0]; if (!file) return;
  try { await upload('world/icon?id=' + encodeURIComponent(el('edit-id').value), file, '正在保存图片…'); coverRevision++; await refresh(); }
  catch (error) { notify(error.message, true); }
  finally { event.target.value = ''; }
};
el('backup-server').onclick = async () => {
  if (!selection) return;
  setBusy('正在备份实例…');
  try { const result = await api('world/backup', {id:selection}); notify('备份已保存：' + result.path); }
  catch (error) { notify(error.message, true); }
  finally { setBusy(''); }
};
el('import-jar').onclick = () => el('jar-file').click();
el('jar-file').onchange = async event => {
  const file = event.target.files[0]; if (!file) return;
  try { const result = await upload('versions/import', file, '正在检查服务端…'); await refresh(); notify('已导入 ' + result.version); }
  catch (error) { notify(error.message, true); }
  finally { event.target.value = ''; }
};
el('refresh-versions').onclick = loadVersions;
el('filter-versions').oninput = renderVersions;
el('include-snapshots').onchange = renderVersions;
el('version-rows').onclick = async event => {
  const button = event.target.closest('[data-download]'); if (!button) return;
  try { button.disabled = true; await api('versions/download', {version:button.dataset.download}); await refresh(); }
  catch (error) { button.disabled = false; notify(error.message, true); }
};
el('scan-java').onclick = async () => { try { requirementCache.clear(); await api('java/scan', {}); await refresh(); } catch (error) { notify(error.message, true); } };
document.querySelectorAll('.browse-java').forEach(button => button.onclick = async () => {
  try { const result = await api('java/browse', {kind:button.dataset.kind}); if (result.cancelled) return; requirementCache.clear(); await refresh(); if (result.path) el('pinned-java').value = result.path; notify(result.path ? '已添加 Java ' + result.version : '已添加文件夹，正在搜索。'); }
  catch (error) { notify(error.message, true); }
});
el('java-mode').onchange = () => el('manual-java').hidden = el('java-mode').value !== 'manual';
el('settings-form').onsubmit = async event => {
  event.preventDefault();
  try { await api('settings', {java:el('pinned-java').value, javaMode:el('java-mode').value, javaRoots:data.settings.javaRoots || [], memory:Number(el('memory').value), port:Number(el('game-port').value), operators:[...draftAdmins], eula:el('accept-eula').checked,serverOptions:{onlineMode:el('online-mode').checked,commandBlocks:el('command-blocks').checked,allowFlight:el('allow-flight').checked}}); requirementCache.clear(); await refresh(); notify('设置已保存，下次开服时生效。'); }
  catch (error) { notify(error.message, true); }
};
document.querySelectorAll('[data-workspace-tab]').forEach(button=>button.onclick=()=>selectWorkspaceTab(button.dataset.workspaceTab));
el('add-admin').onclick=addAdmin;
el('new-admin').onkeydown=event=>{if(event.key==='Enter'){event.preventDefault();addAdmin();}};
el('admin-list').onclick=event=>{const button=event.target.closest('[data-remove-admin]');if(!button)return;draftAdmins.splice(Number(button.dataset.removeAdmin),1);renderAdmins();};
el('delete-map').onclick=()=>{const world=selectedServer();if(!world)return;el('delete-id').value=world.id;el('delete-name').value=world.name;el('delete-message').textContent='确定删除地图“'+world.name+'”？';el('delete-dialog').showModal();};
el('delete-form').onsubmit=async event=>{event.preventDefault();try{await api('world/delete',{id:el('delete-id').value,confirmName:el('delete-name').value});el('delete-dialog').close();selection=null;await refresh();notify('地图已移到应用回收站。');}catch(error){notify(error.message,true);}};
for (const type of ['dragenter','dragover']) el('map-drop-target').addEventListener(type, event => { event.preventDefault(); el('map-drop-target').classList.add('dragging'); });
for (const type of ['dragleave','drop']) el('map-drop-target').addEventListener(type, event => { event.preventDefault(); el('map-drop-target').classList.remove('dragging'); });
el('map-drop-target').addEventListener('drop', event => { if (!event.dataTransfer.files.length) return; if (event.dataTransfer.files.length !== 1) { notify('请每次导入一个 ZIP。', true); return; } importFile(event.dataTransfer.files[0]); });
window.addEventListener('dragover', event => event.preventDefault());
window.addEventListener('drop', event => event.preventDefault());
initializeIcons();
refresh();
setInterval(refresh, 1500);
