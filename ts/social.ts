// Handles friend/block button interactions on the trainer profile page.

async function socialFetch(path: string, method: string): Promise<{ ok?: boolean; error?: string }> {
  const csrfToken = (document.querySelector('meta[name="csrf-token"]') as HTMLMetaElement)?.content ?? '';
  const res = await fetch(path, {
    method,
    headers: { 'X-CSRF-Token': csrfToken },
  });
  return res.json().catch(() => ({}));
}

function setLoading(btn: HTMLButtonElement, loading: boolean): void {
  btn.disabled = loading;
  btn.style.opacity = loading ? '0.6' : '';
}

// ── Trainer profile page ─────────────────────────────────────────
(function initTrainerSocial() {
  const socialRow = document.getElementById('social-row');
  if (!socialRow) return; // not on trainer profile page, or no social row

  const username = socialRow.dataset.username ?? '';

  const btnFriend = document.getElementById('btn-friend') as HTMLButtonElement | null;
  const btnUnfriend = document.getElementById('btn-unfriend') as HTMLButtonElement | null;
  const btnBlock = document.getElementById('btn-block') as HTMLButtonElement | null;
  const btnUnblock = document.getElementById('btn-unblock') as HTMLButtonElement | null;

  if (btnFriend) {
    btnFriend.addEventListener('click', async () => {
      setLoading(btnFriend, true);
      const d = await socialFetch(`/api/social/${encodeURIComponent(username)}/friend`, 'POST');
      if (d.ok) {
        btnFriend.style.display = 'none';
        if (btnUnfriend) btnUnfriend.style.display = '';
      } else {
        setLoading(btnFriend, false);
      }
    });
  }

  if (btnUnfriend) {
    btnUnfriend.addEventListener('click', async () => {
      setLoading(btnUnfriend, true);
      const d = await socialFetch(`/api/social/${encodeURIComponent(username)}/friend`, 'DELETE');
      if (d.ok) {
        btnUnfriend.style.display = 'none';
        if (btnFriend) { btnFriend.style.display = ''; setLoading(btnFriend, false); }
      } else {
        setLoading(btnUnfriend, false);
      }
    });
  }

  if (btnBlock) {
    btnBlock.addEventListener('click', async () => {
      if (!confirm('Block this user? They will be unfollowed in both directions and will not be able to follow you.')) return;
      setLoading(btnBlock, true);
      const d = await socialFetch(`/api/social/${encodeURIComponent(username)}/block`, 'POST');
      if (d.ok) {
        // Hide all social buttons; replace with a simple "blocked" note
        const row = document.getElementById('social-row');
        if (row) row.innerHTML = '<span style="color:var(--text-2);font-size:0.85rem">User blocked.</span> <button class="btn-secondary social-btn" id="btn-unblock-inline">Unblock</button>';
        const newUnblock = document.getElementById('btn-unblock-inline') as HTMLButtonElement | null;
        if (newUnblock) {
          newUnblock.addEventListener('click', async () => {
            setLoading(newUnblock, true);
            const dd = await socialFetch(`/api/social/${encodeURIComponent(username)}/block`, 'DELETE');
            if (dd.ok) location.reload();
            else setLoading(newUnblock, false);
          });
        }
      } else {
        setLoading(btnBlock, false);
      }
    });
  }

  if (btnUnblock) {
    btnUnblock.addEventListener('click', async () => {
      setLoading(btnUnblock, true);
      const d = await socialFetch(`/api/social/${encodeURIComponent(username)}/block`, 'DELETE');
      if (d.ok) location.reload();
      else setLoading(btnUnblock, false);
    });
  }
})();
