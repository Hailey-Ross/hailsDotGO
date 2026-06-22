// Polls /api/notifications for active raids from friends, shows toasts, updates nav badge.

interface FriendRaidNotif {
  lobby_id: number;
  boss_name: string;
  boss_tier: number;
  state: string;
  host_username: string;
  host_trainer_name: string;
  created_at: string;
}

const NOTIF_POLL_MS = 30_000;
const NOTIF_TOAST_MS = 7_000;
const NOTIF_SEEN_KEY = 'notif_seen_lobbies';
const CSRF_TOKEN_NOTIF =
  (document.querySelector('meta[name="csrf-token"]') as HTMLMetaElement)?.content ?? '';

function getSeenSet(): Set<number> {
  try {
    const raw = localStorage.getItem(NOTIF_SEEN_KEY);
    if (raw) return new Set(JSON.parse(raw) as number[]);
  } catch (_) { /* ignore */ }
  return new Set();
}

function addSeen(id: number): void {
  const s = getSeenSet();
  s.add(id);
  // keep at most 200 entries to avoid unbounded growth
  const arr = Array.from(s).slice(-200);
  try { localStorage.setItem(NOTIF_SEEN_KEY, JSON.stringify(arr)); } catch (_) { /* ignore */ }
}

function updateBadge(count: number): void {
  const badge = document.getElementById('nav-notif-badge');
  if (!badge) return;
  if (count > 0) {
    badge.textContent = String(count);
    badge.style.display = '';
  } else {
    badge.style.display = 'none';
  }
}

function playDing(): void {
  const raw = localStorage.getItem('notif_sound_volume');
  const vol = raw !== null ? parseInt(raw, 10) : 30;
  if (vol <= 0) return;
  try {
    const ctx = new AudioContext();
    const osc = ctx.createOscillator();
    const gain = ctx.createGain();
    osc.connect(gain); gain.connect(ctx.destination);
    osc.type = 'sine'; osc.frequency.value = 880;
    gain.gain.setValueAtTime(0, ctx.currentTime);
    gain.gain.linearRampToValueAtTime(Math.min(vol / 100, 0.5), ctx.currentTime + 0.005);
    gain.gain.exponentialRampToValueAtTime(0.001, ctx.currentTime + 0.4);
    osc.start(ctx.currentTime); osc.stop(ctx.currentTime + 0.4);
    osc.onended = () => ctx.close();
  } catch (_) { /* AudioContext not available */ }
}

function showToast(notif: FriendRaidNotif): void {
  const container = document.getElementById('notif-toast-container');
  if (!container) return;

  const hostDisplay = notif.host_trainer_name
    ? `${notif.host_trainer_name} (@${notif.host_username})`
    : notif.host_username;

  const toast = document.createElement('div');
  toast.className = 'notif-toast';
  toast.innerHTML =
    `<span class="notif-toast-icon">🏟</span>` +
    `<span class="notif-toast-text"><strong>${esc(hostDisplay)}</strong> is hosting a ${esc(notif.boss_name)} raid!</span>` +
    `<a href="/raidfinder" class="notif-toast-link">Join</a>` +
    `<button class="notif-toast-close" aria-label="Dismiss">&times;</button>`;

  toast.querySelector('.notif-toast-close')?.addEventListener('click', () => dismissToast(toast));
  playDing();
  container.appendChild(toast);

  // trigger animation
  requestAnimationFrame(() => toast.classList.add('notif-toast-visible'));

  setTimeout(() => dismissToast(toast), NOTIF_TOAST_MS);
}

function dismissToast(toast: HTMLElement): void {
  toast.classList.remove('notif-toast-visible');
  toast.classList.add('notif-toast-hiding');
  setTimeout(() => toast.remove(), 350);
}

function esc(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

async function pollNotifications(): Promise<void> {
  if (document.visibilityState === 'hidden') return;

  let notifs: FriendRaidNotif[] = [];
  try {
    const res = await fetch('/api/notifications', {
      headers: { 'X-CSRF-Token': CSRF_TOKEN_NOTIF },
    });
    if (!res.ok) return;
    notifs = await res.json();
  } catch (_) {
    return;
  }

  const seen = getSeenSet();
  updateBadge(notifs.filter(n => !seen.has(n.lobby_id)).length);

  for (const n of notifs) {
    if (!seen.has(n.lobby_id)) {
      showToast(n);
      addSeen(n.lobby_id);
    }
  }
}

let notifTimer: ReturnType<typeof setInterval> | null = null;

function startPolling(): void {
  if (notifTimer !== null) return;
  pollNotifications();
  notifTimer = setInterval(pollNotifications, NOTIF_POLL_MS);
}

document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible') pollNotifications();
});

startPolling();
