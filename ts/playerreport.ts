// Player ("bad actor") report modal. Opens from any element carrying a
// data-report-user attribute (event delegation, so dynamically-rendered flags
// work too) or via window.openPlayerReportModal(username). Submits to
// /api/player-reports.

declare const PR_I18N: { subtitle: string; errDetails: string; failed: string };
declare const ME_USERNAME: string | undefined;

(function () {
  const overlay = document.getElementById('pr-modal-overlay');
  if (!overlay) return; // logged out: no modal in the DOM

  const closeBtn = document.getElementById('pr-modal-close');
  const sub = document.getElementById('pr-modal-sub');
  const form = document.getElementById('pr-form') as HTMLFormElement | null;
  const reasonEl = document.getElementById('pr-reason') as HTMLSelectElement | null;
  const detailsEl = document.getElementById('pr-details') as HTMLTextAreaElement | null;
  const errEl = document.getElementById('pr-error');
  const successEl = document.getElementById('pr-success');
  const csrf =
    (document.querySelector('meta[name="csrf-token"]') as HTMLMetaElement)?.content ?? '';

  let target = '';

  function openModal(username: string): void {
    target = username;
    if (sub) sub.textContent = PR_I18N.subtitle.replace('{user}', '@' + username);
    if (form) form.style.display = '';
    if (successEl) (successEl as HTMLElement).style.display = 'none';
    if (errEl) (errEl as HTMLElement).style.display = 'none';
    if (detailsEl) detailsEl.value = '';
    overlay!.classList.add('open');
    overlay!.setAttribute('aria-hidden', 'false');
    reasonEl?.focus();
  }
  function closeModal(): void {
    overlay!.classList.remove('open');
    overlay!.setAttribute('aria-hidden', 'true');
  }
  (window as unknown as { openPlayerReportModal: (u: string) => void }).openPlayerReportModal = openModal;

  // Delegated: any [data-report-user] click opens the modal (skips self / empty).
  document.addEventListener('click', (e) => {
    const el = (e.target as HTMLElement)?.closest('[data-report-user]') as HTMLElement | null;
    if (!el) return;
    const username = el.getAttribute('data-report-user') || '';
    if (!username || username === ME_USERNAME) return;
    e.preventDefault();
    openModal(username);
  });

  closeBtn?.addEventListener('click', closeModal);
  overlay.addEventListener('click', (e) => {
    if (e.target === overlay) closeModal();
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && overlay.classList.contains('open')) closeModal();
  });

  function showError(msg: string): void {
    if (!errEl) return;
    errEl.textContent = msg;
    (errEl as HTMLElement).style.display = '';
  }

  form?.addEventListener('submit', async (e) => {
    e.preventDefault();
    const reason = reasonEl?.value ?? '';
    const details = (detailsEl?.value ?? '').trim();
    if (!details) { showError(PR_I18N.errDetails); return; }
    if (errEl) (errEl as HTMLElement).style.display = 'none';

    const submitBtn = form.querySelector('button[type="submit"]') as HTMLButtonElement | null;
    if (submitBtn) submitBtn.disabled = true;
    try {
      const res = await fetch('/api/player-reports', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
        body: JSON.stringify({ username: target, reason, details }),
      });
      const data = await res.json().catch(() => ({} as { ok?: boolean; error?: string }));
      if (res.ok && data.ok) {
        if (form) form.style.display = 'none';
        if (successEl) (successEl as HTMLElement).style.display = '';
      } else {
        showError(data.error || PR_I18N.failed);
      }
    } catch (_) {
      showError(PR_I18N.failed);
    } finally {
      if (submitBtn) submitBtn.disabled = false;
    }
  });
})();
