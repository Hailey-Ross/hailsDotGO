// Bug report modal: opens from the nav bug button, submits a new report.
// Exposes window.openBugModal so the /reports page can reuse the same modal,
// and dispatches a 'bugreport:created' event on success so listeners can refresh.

declare const BUG_I18N: { errSubject: string; errDetails: string; failed: string };

(function () {
  const overlay = document.getElementById('bug-modal-overlay');
  if (!overlay) return;

  const closeBtn = document.getElementById('bug-modal-close');
  const form = document.getElementById('bug-form') as HTMLFormElement | null;
  const subjectEl = document.getElementById('bug-subject') as HTMLInputElement | null;
  const bodyEl = document.getElementById('bug-body') as HTMLTextAreaElement | null;
  const errEl = document.getElementById('bug-error');
  const successEl = document.getElementById('bug-success');
  const csrf =
    (document.querySelector('meta[name="csrf-token"]') as HTMLMetaElement)?.content ?? '';

  function openModal(): void {
    if (form) form.style.display = '';
    if (successEl) (successEl as HTMLElement).style.display = 'none';
    if (errEl) (errEl as HTMLElement).style.display = 'none';
    overlay!.classList.add('open');
    overlay!.setAttribute('aria-hidden', 'false');
    subjectEl?.focus();
  }
  function closeModal(): void {
    overlay!.classList.remove('open');
    overlay!.setAttribute('aria-hidden', 'true');
  }
  (window as unknown as { openBugModal: () => void }).openBugModal = openModal;

  document.getElementById('nav-bug-btn')?.addEventListener('click', openModal);
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
    const subject = (subjectEl?.value ?? '').trim();
    const body = (bodyEl?.value ?? '').trim();
    if (!subject) { showError(BUG_I18N.errSubject); return; }
    if (!body) { showError(BUG_I18N.errDetails); return; }
    if (errEl) (errEl as HTMLElement).style.display = 'none';

    const submitBtn = form.querySelector('button[type="submit"]') as HTMLButtonElement | null;
    if (submitBtn) submitBtn.disabled = true;
    try {
      const res = await fetch('/api/bug-reports', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
        body: JSON.stringify({ subject, body }),
      });
      const data = await res.json().catch(() => ({} as { ok?: boolean; id?: number; error?: string }));
      if (res.ok && data.ok) {
        if (subjectEl) subjectEl.value = '';
        if (bodyEl) bodyEl.value = '';
        if (form) form.style.display = 'none';
        if (successEl) (successEl as HTMLElement).style.display = '';
        document.dispatchEvent(new CustomEvent('bugreport:created', { detail: { id: data.id } }));
      } else {
        showError(data.error || BUG_I18N.failed);
      }
    } catch (_) {
      showError(BUG_I18N.failed);
    } finally {
      if (submitBtn) submitBtn.disabled = false;
    }
  });
})();
