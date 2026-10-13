function ddOpen(dd) {
  document.querySelectorAll('[data-dd]').forEach(other => other !== dd && ddClose(other));
  const list = dd.querySelector('[role=listbox]');
  list.hidden = false;
  dd.querySelector('.dd-btn').setAttribute('aria-expanded', 'true');
  (list.querySelector('[aria-selected=true]') || list.querySelector('[role=option]')).focus();
}

function ddClose(dd, focusButton) {
  const list = dd.querySelector('[role=listbox]');
  if (list.hidden) return false;
  list.hidden = true;
  dd.querySelector('.dd-btn').setAttribute('aria-expanded', 'false');
  if (focusButton) dd.querySelector('.dd-btn').focus();
  return true;
}

function ddSelect(dd, option, notify = true) {
  if (!option) return;
  dd.querySelectorAll('[role=option]').forEach(o => o.setAttribute('aria-selected', String(o === option)));
  dd.querySelector('input[type=hidden]').value = option.dataset.value;
  const button = dd.querySelector('.dd-btn');
  dd.querySelector('.dd-label').textContent = option.dataset.label;
  if (button.hasAttribute('aria-label')) {
    button.setAttribute('aria-label', button.getAttribute('aria-label').replace(/:.*$/, ': ' + option.dataset.label));
  }
  if (notify) dd.dispatchEvent(new CustomEvent('dd-change', { bubbles: true }));
}

document.addEventListener('click', e => {
  const dd = e.target.closest('[data-dd]');
  document.querySelectorAll('[data-dd]').forEach(other => other !== dd && ddClose(other));
  if (!dd) return;
  if (e.target.closest('.dd-btn')) {
    dd.querySelector('[role=listbox]').hidden ? ddOpen(dd) : ddClose(dd);
    return;
  }
  const option = e.target.closest('[role=option]');
  if (option) {
    ddSelect(dd, option);
    ddClose(dd, true);
  }
});

document.addEventListener('keydown', e => {
  const dd = e.target.closest && e.target.closest('[data-dd]');
  if (!dd) return;
  if (e.target.closest('.dd-btn') && ['ArrowDown', 'ArrowUp'].includes(e.key)) {
    e.preventDefault();
    ddOpen(dd);
    return;
  }
  const options = [...dd.querySelectorAll('[role=option]')];
  const index = options.indexOf(e.target);
  if (index < 0) return;
  const moves = { ArrowDown: index + 1, ArrowUp: index - 1, Home: 0, End: options.length - 1 };
  if (e.key in moves) {
    e.preventDefault();
    options[Math.max(0, Math.min(options.length - 1, moves[e.key]))].focus();
  } else if (e.key === 'Escape') {
    e.preventDefault();
    ddClose(dd, true);
  } else if (e.key === 'Tab') {
    ddClose(dd);
  }
});

function openGitHubModal() {
  document.getElementById('github-modal').showModal();
  onProjectSelectChange(document.getElementById('github-project-select'));
}

function closeGitHubModal() {
  document.getElementById('github-modal').close();
}

function onProjectSelectChange(select) {
  const opt = select && select.options[select.selectedIndex];
  if (!opt) return;
  const dd = document.getElementById('github-repo-dd');
  // Привязанный репозиторий мог пропасть из выбранных — тогда в списке нет такой опции
  const option = dd.querySelector(`[role=option][data-value="${opt.dataset.repoId || '0'}"]`) || dd.querySelector('[role=option][data-value="0"]');
  ddSelect(dd, option, false);
  document.getElementById('github-branch-input').value = opt.dataset.branch || 'main';
  document.getElementById('github-prefix-input').value = opt.dataset.prefix || '';
  document.getElementById('github-branch-hint').hidden = true;
}

document.addEventListener('dd-change', e => {
  if (e.target.id !== 'github-repo-dd') return;
  const branch = e.target.querySelector('[role=option][aria-selected=true]').dataset.branch;
  if (!branch) return;
  document.getElementById('github-branch-input').value = branch;
  document.getElementById('github-branch-hint').hidden = false;
});
