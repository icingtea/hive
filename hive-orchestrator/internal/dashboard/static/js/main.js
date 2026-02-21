// ── Theme ─────────────────────────────────────────────────────────────────────
function toggleTheme() {
  const cur = document.documentElement.getAttribute('data-theme') || 'light';
  const next = cur === 'light' ? 'dark' : 'light';
  document.documentElement.setAttribute('data-theme', next);
  localStorage.setItem('hive-theme', next);
}

// ── Modal ─────────────────────────────────────────────────────────────────────
function openModal() {
  document.getElementById('deploy-modal')?.classList.add('open');
  setTimeout(() => document.getElementById('repo-url')?.focus(), 60);
}
function closeModal() {
  document.getElementById('deploy-modal')?.classList.remove('open');
}
document.addEventListener('keydown', e => { if (e.key === 'Escape') closeModal(); });
document.getElementById('deploy-modal')?.addEventListener('click', e => {
  if (e.target === e.currentTarget) closeModal();
});

// ── Deploy form ───────────────────────────────────────────────────────────────
function handleDeployResponse(event) {
  const btn = document.getElementById('deploy-btn');
  if (btn) btn.disabled = false;

  if (event.detail.successful) {
    const html = event.detail.xhr.responseText;
    if (!html.includes('flash--error')) {
      // Extract deployment ID from flash message to redirect
      const match = html.match(/Deployment ([a-f0-9]{8})/);
      setTimeout(() => {
        closeModal();
        document.getElementById('deploy-form')?.reset();
        // Refresh list while user is redirected
        const list = document.getElementById('deployments-list');
        if (list) htmx.trigger(list, 'load');
      }, 500);
    }
  }
}

// ── Logs ──────────────────────────────────────────────────────────────────────
function scrollLogsToBottom() {
  const lb = document.getElementById('log-body');
  if (lb) lb.scrollTop = lb.scrollHeight;
}

// ── Flash auto-dismiss ────────────────────────────────────────────────────────
function initFlashDismiss(root) {
  (root || document).querySelectorAll('.flash').forEach(el => {
    if (el._timer) return;
    el._timer = setTimeout(() => {
      el.style.transition = 'opacity 0.35s ease';
      el.style.opacity = '0';
      setTimeout(() => el.remove(), 380);
    }, 5500);
  });
}

// ── HTMX hooks ────────────────────────────────────────────────────────────────
document.addEventListener('htmx:afterSettle', () => initFlashDismiss(document));

document.addEventListener('htmx:beforeRequest', e => {
  if (e.target.id === 'deploy-form') {
    const btn = document.getElementById('deploy-btn');
    if (btn) btn.disabled = true;
  }
});

document.addEventListener('htmx:beforeSwap', e => {
  if (e.detail.target.id === 'main-content') {
    e.detail.target.style.opacity = '0';
    e.detail.target.style.transform = 'translateY(4px)';
    e.detail.target.style.transition = 'opacity 0.15s, transform 0.15s';
  }
});

document.addEventListener('htmx:afterSwap', e => {
  if (e.detail.target.id === 'main-content') {
    requestAnimationFrame(() => {
      e.detail.target.style.opacity = '1';
      e.detail.target.style.transform = 'translateY(0)';
    });
  }
});

initFlashDismiss(document);
