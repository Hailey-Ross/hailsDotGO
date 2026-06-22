// Handles feedback form submission and mod-delete on the trainer profile page.

function trainerFetch(path: string, method: string, body?: unknown): Promise<Response> {
  const csrfToken = (document.querySelector('meta[name="csrf-token"]') as HTMLMetaElement)?.content ?? '';
  const headers: Record<string, string> = { 'X-CSRF-Token': csrfToken };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  return fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
}

// ── Feedback form submit ─────────────────────────────────────────
(function initFeedbackForm() {
  const form = document.getElementById('feedback-form') as HTMLFormElement | null;
  if (!form) return;

  const select = document.getElementById('feedback-select') as HTMLSelectElement | null;
  const submitBtn = document.getElementById('feedback-submit') as HTMLButtonElement | null;
  const statusEl = document.getElementById('feedback-form-status') as HTMLElement | null;

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    if (!select || !select.value) {
      if (statusEl) { statusEl.textContent = 'Please select an option.'; statusEl.style.color = '#fc8181'; statusEl.style.display = ''; }
      return;
    }
    const optionId = parseInt(select.value, 10);
    if (submitBtn) submitBtn.disabled = true;

    const username = form.dataset.username ?? '';
    try {
      const res = await trainerFetch(`/api/feedback/${encodeURIComponent(username)}`, 'POST', { option_id: optionId });
      const d = await res.json().catch(() => ({})) as { ok?: boolean; error?: string };
      if (d.ok) {
        if (statusEl) { statusEl.textContent = 'Feedback saved! Reload to see your update.'; statusEl.style.color = 'var(--green)'; statusEl.style.display = ''; }
      } else {
        if (statusEl) { statusEl.textContent = d.error ?? 'Something went wrong.'; statusEl.style.color = '#fc8181'; statusEl.style.display = ''; }
      }
    } catch (_) {
      if (statusEl) { statusEl.textContent = 'Network error.'; statusEl.style.color = '#fc8181'; statusEl.style.display = ''; }
    }
    if (submitBtn) submitBtn.disabled = false;
  });
})();

// ── Delete own feedback ──────────────────────────────────────────
(function initDeleteOwn() {
  const btn = document.getElementById('feedback-delete-own') as HTMLButtonElement | null;
  if (!btn) return;
  btn.addEventListener('click', async () => {
    if (!confirm('Remove your feedback?')) return;
    const id = btn.dataset.id;
    if (!id) return;
    btn.disabled = true;
    const res = await trainerFetch(`/api/feedback/entry/${id}`, 'DELETE');
    const d = await res.json().catch(() => ({})) as { ok?: boolean };
    if (d.ok) {
      const card = document.getElementById(`fb-${id}`);
      if (card) card.remove();
      btn.remove();
      const form = document.getElementById('feedback-form');
      if (form) {
        const heading = form.previousElementSibling as HTMLElement | null;
        if (heading) heading.textContent = heading.textContent?.replace('Update', 'Leave') ?? '';
      }
    } else {
      btn.disabled = false;
    }
  });
})();

// ── Mod delete buttons ───────────────────────────────────────────
(function initModDelete() {
  if (!TRAINER_CTX.isMod) return;
  document.querySelectorAll<HTMLButtonElement>('.feedback-mod-delete').forEach((btn) => {
    btn.addEventListener('click', async () => {
      if (!confirm('Delete this feedback? This cannot be undone.')) return;
      const id = btn.dataset.id;
      if (!id) return;
      btn.disabled = true;
      const res = await trainerFetch(`/api/feedback/entry/${id}`, 'DELETE');
      const d = await res.json().catch(() => ({})) as { ok?: boolean };
      if (d.ok) {
        const card = document.getElementById(`fb-${id}`);
        if (card) card.remove();
      } else {
        btn.disabled = false;
      }
    });
  });
})();
