// Dual-frame "Reports" messenger: left pane lists the user's reports, right pane
// shows the selected thread with a reply box. Reuses the global bug modal for
// "New report". Polls the open thread + list while the tab is visible.

declare const REPORTS_I18N: Record<string, string>;

interface BugLabel { id: number; name: string; color: string }
interface BugListItem {
  id: number; subject: string; status: string; last_activity_at: string;
  role: string; reporter_username: string; unread: boolean; labels: BugLabel[];
}
interface BugMessage {
  id: number; author_username: string; author_id: number; is_staff: boolean;
  is_mine: boolean; is_system: boolean; is_internal: boolean; body: string; created_at: string;
}
interface BugParticipant { username: string; role: string }
interface BugReportDetail {
  id: number; subject: string; status: string; reporter_username: string;
  role: string; is_staff: boolean; messages: BugMessage[];
  participants: BugParticipant[]; labels: BugLabel[];
  priority: string; assignee: string; rating: string; rating_comment: string; rated_at: string;
}

(function () {
  const shell = document.getElementById('reports-shell');
  if (!shell) return;

  const listBody = document.getElementById('reports-list-body')!;
  const listEmpty = document.getElementById('reports-list-empty');
  const threadEmpty = document.getElementById('reports-thread-empty')!;
  const threadInner = document.getElementById('reports-thread-inner')!;
  const subjectEl = document.getElementById('rt-subject')!;
  const statusEl = document.getElementById('rt-status')!;
  const labelsEl = document.getElementById('rt-labels')!;
  const participantsEl = document.getElementById('rt-participants')!;
  const messagesEl = document.getElementById('rt-messages')!;
  const replyForm = document.getElementById('rt-reply-form') as HTMLFormElement;
  const replyBody = document.getElementById('rt-reply-body') as HTMLTextAreaElement;
  const internalWrap = document.getElementById('rt-internal-wrap');
  const internalCb = document.getElementById('rt-internal') as HTMLInputElement | null;
  const resolveBtn = document.getElementById('rt-resolve-btn') as HTMLButtonElement | null;
  const csatEl = document.getElementById('rt-csat');
  const inviteBtn = document.getElementById('rt-invite-btn');
  const inviteRow = document.getElementById('rt-invite-row');
  const inviteInput = document.getElementById('rt-invite-input') as HTMLInputElement | null;
  const inviteList = document.getElementById('rt-invite-list');
  const inviteAdd = document.getElementById('rt-invite-add');
  const inviteError = document.getElementById('rt-invite-error');
  const backBtn = document.getElementById('rt-back');

  const csrf =
    (document.querySelector('meta[name="csrf-token"]') as HTMLMetaElement)?.content ?? '';
  const POLL_MS = 12_000;
  let currentId = 0;

  function el(tag: string, cls?: string, text?: string): HTMLElement {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }

  function statusLabel(s: string): string {
    return REPORTS_I18N['status' + s.charAt(0).toUpperCase() + s.slice(1)] || s;
  }

  function fmtTime(iso: string): string {
    const d = new Date(iso);
    return isNaN(d.getTime()) ? '' : d.toLocaleString();
  }

  function labelChip(l: BugLabel): HTMLElement {
    const chip = el('span', 'report-label-chip', l.name);
    chip.style.setProperty('--chip', l.color);
    return chip;
  }

  function buildListItem(it: BugListItem): HTMLElement {
    const btn = el('button', 'report-list-item');
    btn.setAttribute('data-id', String(it.id));
    if (it.id === currentId) btn.classList.add('active');
    if (it.unread) btn.classList.add('unread');

    const top = el('div', 'report-list-item-top');
    const subj = el('span', 'report-list-subject');
    subj.appendChild(el('span', 'report-ref', '#' + it.id + ' '));
    subj.appendChild(document.createTextNode(it.subject));
    top.appendChild(subj);
    if (it.unread) top.appendChild(el('span', 'report-unread-dot'));
    btn.appendChild(top);

    const meta = el('div', 'report-list-item-meta');
    meta.appendChild(el('span', 'report-status-chip status-' + it.status, statusLabel(it.status)));
    for (const l of it.labels) meta.appendChild(labelChip(l));
    btn.appendChild(meta);

    btn.addEventListener('click', () => openReport(it.id));
    return btn;
  }

  async function loadList(): Promise<void> {
    let items: BugListItem[] = [];
    try {
      const res = await fetch('/api/bug-reports', { headers: { 'X-CSRF-Token': csrf } });
      if (!res.ok) return;
      items = await res.json();
    } catch (_) { return; }

    listBody.querySelectorAll('.report-list-item, .reports-section-head').forEach((n) => n.remove());
    if (listEmpty) listEmpty.style.display = items.length ? 'none' : '';

    const active = items.filter((it) => it.status !== 'resolved' && it.status !== 'closed');
    const closed = items.filter((it) => it.status === 'resolved' || it.status === 'closed');

    if (active.length) {
      listBody.appendChild(el('div', 'reports-section-head', REPORTS_I18N.sectionActive));
      for (const it of active) listBody.appendChild(buildListItem(it));
    }
    if (closed.length) {
      listBody.appendChild(el('div', 'reports-section-head', REPORTS_I18N.sectionClosed));
      for (const it of closed) listBody.appendChild(buildListItem(it));
    }
  }

  function renderMessages(messages: BugMessage[]): void {
    messagesEl.textContent = '';
    for (const m of messages) {
      if (m.is_system) {
        messagesEl.appendChild(el('div', 'report-system', m.body));
        continue;
      }
      const bubble = el('div', 'report-bubble ' + (m.is_mine ? 'mine' : 'theirs'));
      if (m.is_internal) bubble.classList.add('internal');

      const head = el('div', 'report-bubble-head');
      head.appendChild(el('span', 'report-bubble-author', m.is_mine ? REPORTS_I18N.you : m.author_username));
      if (m.is_staff) head.appendChild(el('span', 'report-staff-badge', REPORTS_I18N.staff));
      if (m.is_internal) head.appendChild(el('span', 'report-internal-badge', REPORTS_I18N.internalNote));
      head.appendChild(el('span', 'report-bubble-time', fmtTime(m.created_at)));
      bubble.appendChild(head);

      bubble.appendChild(el('div', 'report-bubble-body', m.body));
      messagesEl.appendChild(bubble);
    }
    messagesEl.scrollTop = messagesEl.scrollHeight;
  }

  async function postStatus(status: string): Promise<void> {
    if (!currentId) return;
    try {
      const res = await fetch('/api/bug-reports/' + currentId + '/status', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
        body: JSON.stringify({ status }),
      });
      const data = await res.json().catch(() => ({} as { ok?: boolean }));
      if (res.ok && data.ok) openReport(currentId);
    } catch (_) { /* ignore */ }
  }

  function submitRating(rating: string, comment: string): Promise<boolean> {
    if (!currentId) return Promise.resolve(false);
    return fetch('/api/bug-reports/' + currentId + '/rating', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
      body: JSON.stringify({ rating, comment }),
    }).then((res) => res.json()).then((d: { ok?: boolean }) => !!d.ok).catch(() => false);
  }

  // Interactive CSAT widget: clicking Good/Bad submits immediately, and the
  // rating (and comment) stays editable for one week from the first rating.
  function buildCsatWidget(container: HTMLElement, d: BugReportDetail): void {
    container.appendChild(el('div', 'reports-csat-prompt', REPORTS_I18N.ratePrompt));

    let chosen = d.rating;
    const btns = el('div', 'reports-csat-btns');
    const goodBtn = el('button', 'btn-secondary reports-csat-btn', REPORTS_I18N.rateGood);
    const badBtn = el('button', 'btn-secondary reports-csat-btn', REPORTS_I18N.rateBad);
    if (chosen === 'good') goodBtn.classList.add('active');
    if (chosen === 'bad') badBtn.classList.add('active');
    btns.appendChild(goodBtn);
    btns.appendChild(badBtn);
    container.appendChild(btns);

    const comment = document.createElement('textarea');
    comment.className = 'reports-csat-comment';
    comment.placeholder = REPORTS_I18N.rateComment;
    comment.rows = 2;
    comment.value = d.rating_comment || '';
    container.appendChild(comment);

    const hint = el('div', 'reports-csat-hint');
    container.appendChild(hint);
    function showHint(): void {
      if (chosen && d.rated_at) {
        const until = new Date(new Date(d.rated_at).getTime() + 7 * 86_400_000);
        hint.textContent = REPORTS_I18N.rateChangeUntil + ' ' + until.toLocaleDateString();
      } else {
        hint.textContent = '';
      }
    }
    showHint();

    async function pick(rating: string): Promise<void> {
      const ok = await submitRating(rating, comment.value.trim());
      if (!ok) return;
      chosen = rating;
      goodBtn.classList.toggle('active', rating === 'good');
      badBtn.classList.toggle('active', rating === 'bad');
      if (!d.rated_at) d.rated_at = new Date().toISOString(); // anchor the week client-side
      showHint();
    }
    goodBtn.addEventListener('click', () => pick('good'));
    badBtn.addEventListener('click', () => pick('bad'));
    comment.addEventListener('blur', () => {
      if (chosen) submitRating(chosen, comment.value.trim());
    });
  }

  function renderReporterControls(d: BugReportDetail): void {
    const isReporter = d.role === 'reporter';
    const closed = d.status === 'resolved' || d.status === 'closed';

    if (resolveBtn) {
      if (isReporter) {
        resolveBtn.style.display = '';
        resolveBtn.textContent = closed ? REPORTS_I18N.reopen : REPORTS_I18N.markResolved;
        resolveBtn.onclick = () => postStatus(closed ? 'open' : 'resolved');
      } else {
        resolveBtn.style.display = 'none';
      }
    }

    if (csatEl) {
      csatEl.textContent = '';
      const rated = d.rating !== '';
      const within = !!d.rated_at && (Date.now() - new Date(d.rated_at).getTime() < 7 * 86_400_000);
      const editable = (!rated && closed) || (rated && within);
      if (isReporter && editable) {
        buildCsatWidget(csatEl, d);
        csatEl.style.display = '';
      } else if (isReporter && rated) {
        const label = d.rating === 'good' ? REPORTS_I18N.rateGood : REPORTS_I18N.rateBad;
        csatEl.appendChild(el('div', 'reports-csat-done', REPORTS_I18N.rateThanks + ' (' + label + ')'));
        csatEl.style.display = '';
      } else {
        csatEl.style.display = 'none';
      }
    }
  }

  async function openReport(id: number): Promise<void> {
    let d: BugReportDetail;
    try {
      const res = await fetch('/api/bug-reports/' + id, { headers: { 'X-CSRF-Token': csrf } });
      if (!res.ok) return;
      d = await res.json();
    } catch (_) { return; }

    currentId = id;
    threadEmpty.style.display = 'none';
    threadInner.style.display = '';
    shell!.classList.add('thread-open');

    subjectEl.textContent = '#' + d.id + ' ' + d.subject;
    statusEl.className = 'report-status-chip status-' + d.status;
    statusEl.textContent = statusLabel(d.status);

    labelsEl.textContent = '';
    for (const l of d.labels) labelsEl.appendChild(labelChip(l));

    participantsEl.textContent = '';
    for (const p of d.participants) {
      participantsEl.appendChild(el('span', 'report-participant role-' + p.role, '@' + p.username));
    }

    if (internalWrap) internalWrap.style.display = d.is_staff ? '' : 'none';

    renderReporterControls(d);
    renderMessages(d.messages);
    loadList(); // refresh unread state in the list

    // highlight active in list
    listBody.querySelectorAll('.report-list-item').forEach((n) => {
      n.classList.toggle('active', n.getAttribute('data-id') === String(id));
    });
  }

  replyForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const body = replyBody.value.trim();
    if (!body || !currentId) return;
    const visibility = internalCb && internalCb.checked ? 'internal' : 'public';
    const btn = replyForm.querySelector('button[type="submit"]') as HTMLButtonElement | null;
    if (btn) btn.disabled = true;
    try {
      const res = await fetch('/api/bug-reports/' + currentId + '/messages', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
        body: JSON.stringify({ body, visibility }),
      });
      const data = await res.json().catch(() => ({} as { ok?: boolean }));
      if (res.ok && data.ok) {
        replyBody.value = '';
        if (internalCb) internalCb.checked = false;
        openReport(currentId);
      }
    } catch (_) { /* ignore */ } finally {
      if (btn) btn.disabled = false;
    }
  });

  // Invite flow
  inviteBtn?.addEventListener('click', () => {
    if (!inviteRow) return;
    inviteRow.style.display = inviteRow.style.display === 'none' ? '' : 'none';
    if (inviteError) inviteError.textContent = '';
    inviteInput?.focus();
  });

  inviteInput?.addEventListener('input', async () => {
    const q = inviteInput.value.trim();
    if (!inviteList || q.length < 2) return;
    try {
      const res = await fetch('/api/users/search?q=' + encodeURIComponent(q), { headers: { 'X-CSRF-Token': csrf } });
      if (!res.ok) return;
      const users: { username: string }[] = await res.json();
      inviteList.textContent = '';
      for (const u of users) {
        const opt = document.createElement('option');
        opt.value = u.username;
        inviteList.appendChild(opt);
      }
    } catch (_) { /* ignore */ }
  });

  async function doInvite(): Promise<void> {
    if (!inviteInput || !currentId) return;
    const username = inviteInput.value.trim();
    if (!username) return;
    if (inviteError) inviteError.textContent = '';
    try {
      const res = await fetch('/api/bug-reports/' + currentId + '/invite', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
        body: JSON.stringify({ username }),
      });
      const data = await res.json().catch(() => ({} as { ok?: boolean; error?: string }));
      if (res.ok && data.ok) {
        inviteInput.value = '';
        if (inviteRow) inviteRow.style.display = 'none';
        openReport(currentId);
      } else if (inviteError) {
        inviteError.textContent = data.error || REPORTS_I18N.failed;
      }
    } catch (_) {
      if (inviteError) inviteError.textContent = REPORTS_I18N.failed;
    }
  }
  inviteAdd?.addEventListener('click', doInvite);

  backBtn?.addEventListener('click', () => {
    shell!.classList.remove('thread-open');
    currentId = 0;
  });

  document.getElementById('reports-new-btn')?.addEventListener('click', () => {
    const open = (window as unknown as { openBugModal?: () => void }).openBugModal;
    if (open) open();
  });

  document.addEventListener('bugreport:created', () => {
    loadList();
  });

  // Poll the open thread + list while the tab is visible.
  setInterval(() => {
    if (document.visibilityState === 'hidden') return;
    if (currentId) openReport(currentId); else loadList();
  }, POLL_MS);

  loadList();
})();
