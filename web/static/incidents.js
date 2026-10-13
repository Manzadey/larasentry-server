const live = { active: true, pending: new Set(), fresh: new Set(), timer: 0 };

function toggleLiveStream() {
  live.active = !live.active;
  const btn = document.getElementById('live-toggle-btn');
  btn.setAttribute('aria-pressed', String(live.active));
  btn.style.color = live.active ? '' : 'var(--amber)';
  document.getElementById('live-dot').style.background = live.active ? 'var(--green)' : 'var(--amber)';
  document.getElementById('live-status-text').textContent = live.active ? 'Обновляется в реальном времени' : 'Обновления на паузе';
  if (live.active) flushLive();
}

function flushLive() {
  live.pending.forEach(id => live.fresh.add(id));
  live.pending.clear();
  document.getElementById('live-new-btn').hidden = true;
  if (typeof refreshList === 'function') refreshList();
}

function onLiveIncident(id) {
  if (!live.active) {
    live.pending.add(id);
    const n = live.pending.size;
    document.getElementById('live-new-text').textContent = `Показать ${n} ${plural(n, 'новую', 'новые', 'новых')}`;
    document.getElementById('live-new-btn').hidden = false;
    return;
  }
  live.fresh.add(id);
  // Троттлинг, а не дебаунс: при событиях чаще раза в секунду дебаунс не обновил бы список ни разу
  if (live.timer) return;
  live.timer = setTimeout(() => {
    live.timer = 0;
    refreshList();
  }, 1000);
}

if (document.getElementById('incident-list')) {
  new EventSource('/ui/events/stream').addEventListener('new-incident', e => onLiveIncident(e.data));
}

function plural(n, one, few, many) {
  const m10 = n % 10, m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return one;
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few;
  return many;
}

const relativeTime = new Intl.RelativeTimeFormat('ru', { numeric: 'auto' });
const timeUnits = [['year', 31536000], ['month', 2592000], ['day', 86400], ['hour', 3600], ['minute', 60]];

function formatRelativeTimes(root) {
  root.querySelectorAll('time[data-rel]').forEach(t => {
    const diff = (new Date(t.dateTime) - Date.now()) / 1000;
    if (Math.abs(diff) < 60) { t.textContent = 'только что'; return; }
    const [unit, sec] = timeUnits.find(([, s]) => Math.abs(diff) >= s);
    t.textContent = relativeTime.format(Math.round(diff / sec), unit);
  });
}
htmx.onLoad(formatRelativeTimes);
setInterval(() => formatRelativeTimes(document), 60000);

let drawerRowId = null;
let drawerJustOpened = false;

function openDrawer(source) {
  const row = source && source.closest('.row');
  drawerRowId = row ? row.id : null;
  drawerJustOpened = true;
  activeTab = 'stack';
  document.getElementById('detail-panel-content').innerHTML = '';
  markSelectedRow();
  document.getElementById('detail-drawer').classList.add('open');
  document.getElementById('drawer-backdrop').classList.add('open');
}

function closeDrawer() {
  document.getElementById('detail-drawer').classList.remove('open');
  document.getElementById('drawer-backdrop').classList.remove('open');
  const row = drawerRowId && document.getElementById(drawerRowId);
  drawerRowId = null;
  markSelectedRow();
  if (row) row.querySelector('.row-main').focus();
}

function showDrawerError(e) {
  if (e.detail.target.id !== 'detail-panel-content') return;
  const notFound = e.detail.xhr && e.detail.xhr.status === 404;
  const box = document.getElementById('detail-panel-content');
  box.innerHTML = document.getElementById('drawer-error-template').innerHTML;
  box.querySelector('[role=alert]').textContent = notFound
    ? 'Ошибка не найдена: возможно, её уже удалила очистка инцидентов старше 30 дней.'
    : 'Не удалось загрузить ошибку. Проверьте соединение и откройте её ещё раз.';
}
document.addEventListener('htmx:responseError', showDrawerError);
document.addEventListener('htmx:sendError', showDrawerError);

function isDrawerOpen() {
  return document.getElementById('detail-drawer').classList.contains('open');
}

function markSelectedRow() {
  document.querySelectorAll('.row').forEach(r => r.classList.toggle('is-selected', r.id === drawerRowId));
}

// Ищем только внутри обновлённой цели: в чужой цели элемент ещё старый и сейчас будет заменён
function restoreFocusAfterRefresh(target) {
  const el = focusAfterRefresh && target.querySelector(focusAfterRefresh);
  if (el) {
    el.focus();
    focusAfterRefresh = null;
  }
}

document.addEventListener('htmx:afterSettle', e => {
  if (e.detail.target.id !== 'detail-panel-content') return;
  const msg = document.getElementById('incident-message-text');
  const more = document.querySelector('.dh-more');
  if (msg && more) more.hidden = msg.scrollHeight <= msg.clientHeight + 1;
  selectTab(activeTab, false);
  if (drawerJustOpened) {
    drawerJustOpened = false;
    const title = document.getElementById('drawer-title');
    if (title) title.focus();
  }
  restoreFocusAfterRefresh(e.detail.target);
});

// afterSettle, а не afterSwap: при settle htmx возвращает строкам с тем же id прежние class
document.addEventListener('htmx:afterSettle', e => {
  if (e.detail.target.id !== 'incident-list') return;
  restoreFocusAfterRefresh(e.detail.target);
  markSelectedRow();
  live.fresh.forEach(id => {
    const row = document.getElementById('incident-row-' + id);
    if (row) row.classList.add('row-highlight');
  });
  live.fresh.clear();
});

const ICON_CHECK = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M5 13l4 4L19 7"/></svg>';
const ICON_REOPEN = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"/></svg>';

function incidentFromElement(el) {
  const host = el.closest('[data-id]');
  const d = host.dataset;
  const cls = d.class || '';
  const cut = Math.max(cls.lastIndexOf('\\'), cls.lastIndexOf('/')) + 1;
  return {
    id: d.id, ns: cls.slice(0, cut), name: cls.slice(cut), message: d.message,
    release: d.latestRelease !== undefined ? d.latestRelease : latestReleaseFor(d.projectId),
    state: { status: d.status, is_regression: d.regression, resolved_in_release: d.resolvedIn, resolved_at: d.resolvedAt }
  };
}

function latestReleaseFor(projectId) {
  try {
    return JSON.parse(document.getElementById('latest-releases').textContent || '{}')[projectId] || '';
  } catch (err) {
    return '';
  }
}

function fillFields(root, values) {
  Object.entries(values).forEach(([key, value]) => {
    root.querySelectorAll(`[data-field="${key}"]`).forEach(el => { el.textContent = value; });
  });
}

let dialogSource = null;
let dialogIncident = null;

function openResolveDialog(source) {
  const inc = incidentFromElement(source);
  const dialog = document.getElementById('resolve-dialog');
  fillFields(dialog, { ns: inc.ns, name: inc.name, message: inc.message, release: inc.release || 'релиз не указан' });
  dialog.querySelector('input[value=current]').checked = true;
  showActionDialog(dialog, source, inc);
  dialog.querySelector('input:checked').focus();
}

function openReopenDialog(source) {
  const inc = incidentFromElement(source);
  const dialog = document.getElementById('reopen-dialog');
  const resolvedIn = inc.state.resolved_in_release;
  const release = resolvedIn && !['next', 'immediate', 'current'].includes(resolvedIn) ? ' в релизе ' + resolvedIn : '';
  fillFields(dialog, { ns: inc.ns, name: inc.name, message: inc.message, warn: `Ошибка вернётся в открытые, отметка о решении${release} будет снята.` });
  showActionDialog(dialog, source, inc);
  dialog.querySelector('button[value=confirm]').focus();
}

function showActionDialog(dialog, source, inc) {
  dialogSource = source;
  dialogIncident = inc;
  dialog.returnValue = '';
  dialog.showModal();
}

document.getElementById('resolve-dialog').addEventListener('close', e => {
  if (e.target.returnValue !== 'confirm') return restoreDialogFocus();
  const mode = e.target.querySelector('input[name=resolve-mode]:checked').value;
  // Без релиза литерал current ломает сравнение версий при регрессии — тогда решаем «сейчас»
  const type = mode === 'next' ? 'next' : (dialogIncident.release ? 'current' : 'immediate');
  runIncidentAction(dialogIncident, 'resolve', { type });
});

document.getElementById('reopen-dialog').addEventListener('close', e => {
  if (e.target.returnValue !== 'confirm') return restoreDialogFocus();
  runIncidentAction(dialogIncident, 'reopen', {});
});

function restoreDialogFocus() {
  // После close браузер сам возвращает фокус на прежний элемент — ставим свой следующим тиком
  setTimeout(() => {
    if (dialogSource && dialogSource.isConnected) dialogSource.focus();
  });
}

let focusAfterRefresh = null;

async function postIncident(id, action, params) {
  const res = await fetch(`/ui/incidents/${id}/${action}`, { method: 'POST', body: new URLSearchParams(params) });
  if (!res.ok) throw new Error(`${action}: HTTP ${res.status}`);
  htmx.trigger(document.body, 'incident-updated');
}

async function runIncidentAction(inc, action, params) {
  const row = dialogSource && dialogSource.closest('.row');
  focusAfterRefresh = row ? `#incident-act-${inc.id}` : '#drawer-action';
  try {
    await postIncident(inc.id, action, params);
  } catch (err) {
    console.error('Не удалось обновить ошибку:', err);
    showToast('Не удалось обновить ошибку');
    return;
  }
  pushUndo(inc, action === 'resolve');
}

const undoItems = [];

function pushUndo(inc, resolved) {
  undoItems.filter(item => item.inc.id === inc.id).forEach(dismissUndo);
  while (undoItems.length >= 4) dismissUndo(undoItems[0]);

  const el = document.getElementById('undo-toast-template').content.firstElementChild.cloneNode(true);
  const color = resolved ? 'var(--green)' : 'var(--red)';
  const icon = el.querySelector('[data-field=icon]');
  icon.innerHTML = resolved ? ICON_CHECK : ICON_REOPEN;
  icon.style.color = color;
  icon.style.background = resolved ? 'rgba(63,201,142,.15)' : 'rgba(242,107,116,.15)';
  fillFields(el, { text: resolved ? 'Ошибка отмечена решённой' : 'Ошибка снова открыта', name: inc.ns + inc.name, message: inc.message });
  el.querySelector('[data-field=message]').title = inc.message;
  const bar = el.querySelector('[data-field=bar]');
  bar.style.background = color;

  const item = { inc, el, left: 10 };
  item.timer = setInterval(() => {
    item.left -= 1;
    el.querySelector('[data-field=left]').textContent = item.left + ' с';
    if (item.left <= 0) dismissUndo(item);
  }, 1000);
  el.querySelector('[data-action=undo]').addEventListener('click', () => applyUndo(item));
  el.querySelector('[data-action=dismiss]').addEventListener('click', () => dismissUndo(item));
  undoItems.push(item);
  document.getElementById('undo-stack').append(el);
  requestAnimationFrame(() => requestAnimationFrame(() => { bar.style.width = '0%'; }));
}

function dismissUndo(item) {
  const index = undoItems.indexOf(item);
  if (index < 0) return;
  undoItems.splice(index, 1);
  clearInterval(item.timer);
  item.el.classList.add('leaving');
  setTimeout(() => item.el.remove(), 240);
}

async function applyUndo(item) {
  dismissUndo(item);
  try {
    await postIncident(item.inc.id, 'restore', item.inc.state);
  } catch (err) {
    console.error('Не удалось отменить действие:', err);
    showToast('Не удалось отменить действие');
  }
}

function toggleMessage(button) {
  const msg = document.getElementById(button.getAttribute('aria-controls'));
  const expanded = msg.classList.toggle('expanded');
  button.setAttribute('aria-expanded', String(expanded));
  button.textContent = expanded ? 'Свернуть' : 'Показать полностью';
}

async function copyFromButton(button) {
  const target = button.dataset.copyTarget && document.querySelector(button.dataset.copyTarget);
  const text = target ? target.innerText.trim() : button.dataset.copy;
  const label = button.dataset.label || button.getAttribute('aria-label');
  button.dataset.label = label;
  try {
    await navigator.clipboard.writeText(text);
    button.classList.add('copied');
    button.setAttribute('aria-label', 'Скопировано');
  } catch (err) {
    console.error('Ошибка копирования:', err);
    button.classList.add('copy-failed');
    button.setAttribute('aria-label', 'Не удалось скопировать');
  }
  clearTimeout(button.copyTimer);
  button.copyTimer = setTimeout(() => {
    button.classList.remove('copied', 'copy-failed');
    button.setAttribute('aria-label', label);
  }, 1500);
}

document.addEventListener('click', e => {
  const button = e.target.closest('.copy-btn');
  if (button) copyFromButton(button);
});

let activeTab = 'stack';

function selectTab(name, focus) {
  const tabs = document.querySelectorAll('#incident-detail [role=tab]');
  if (!tabs.length) return;
  if (![...tabs].some(t => t.dataset.tab === name)) name = 'stack';
  activeTab = name;
  tabs.forEach(tab => {
    const selected = tab.dataset.tab === name;
    tab.setAttribute('aria-selected', String(selected));
    tab.tabIndex = selected ? 0 : -1;
    document.getElementById(tab.getAttribute('aria-controls')).hidden = !selected;
    if (selected && focus) tab.focus();
  });
}

document.addEventListener('click', e => {
  const tab = e.target.closest('#incident-detail [role=tab]');
  if (tab) selectTab(tab.dataset.tab, false);

  const view = e.target.closest('[data-stack-view]');
  if (view) {
    const section = view.closest('.dsec');
    section.querySelectorAll('[data-stack-view]').forEach(b => b.setAttribute('aria-pressed', String(b === view)));
    section.querySelectorAll('[data-view]').forEach(el => { el.hidden = el.dataset.view !== view.dataset.stackView; });
  }

  const vendor = e.target.closest('[data-vendor-toggle]');
  if (vendor) {
    const open = vendor.getAttribute('aria-pressed') !== 'true';
    vendor.setAttribute('aria-pressed', String(open));
    vendor.textContent = open ? 'Скрыть vendor' : `Показать vendor (${vendor.dataset.count})`;
    vendor.closest('.dsec').querySelectorAll('details.vgroup').forEach(d => { d.open = open; });
  }
});

document.addEventListener('keydown', e => {
  const tab = e.target.closest && e.target.closest('#incident-detail [role=tab]');
  if (!tab) return;
  const tabs = [...document.querySelectorAll('#incident-detail [role=tab]')];
  const index = tabs.indexOf(tab);
  const moves = { ArrowRight: index + 1, ArrowLeft: index - 1, Home: 0, End: tabs.length - 1 };
  if (!(e.key in moves)) return;
  e.preventDefault();
  selectTab(tabs[(moves[e.key] + tabs.length) % tabs.length].dataset.tab, true);
});

function showToast(message) {
  const toast = document.getElementById('toast');
  document.getElementById('toast-message').textContent = message;
  toast.classList.add('show');
  clearTimeout(showToast.timer);
  showToast.timer = setTimeout(() => toast.classList.remove('show'), 2500);
}

function isTypingTarget(el) {
  return el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName));
}

document.addEventListener('keydown', function(e) {
  if (e.defaultPrevented) return;
  if ((e.ctrlKey || e.metaKey) && e.code === 'KeyZ' && !isTypingTarget(document.activeElement) && undoItems.length) {
    e.preventDefault();
    applyUndo(undoItems[undoItems.length - 1]);
    return;
  }
  if (e.key === 'Escape') {
    if (!document.querySelector('dialog[open]') && isDrawerOpen()) closeDrawer();
    return;
  }
  if (e.key === '/' && !isTypingTarget(document.activeElement)) {
    const searchInput = document.getElementById('search-input');
    if (searchInput) {
      e.preventDefault();
      searchInput.focus();
      searchInput.select();
    }
  }
});
