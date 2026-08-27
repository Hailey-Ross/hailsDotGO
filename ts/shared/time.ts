declare var SITE_LANG: string;

// Feed timestamps come in two shapes and the difference is load bearing. A value
// ending in "Z" is a real instant. A value with no zone at all is a floating wall
// clock: a raid rotation runs 06:00 to 22:00 wherever the trainer is standing, so
// the same reading applies in every zone. new Date() parses the second kind as
// local, which is exactly the reading a trainer wants, and the first kind as the
// instant it is. Both are therefore correct with no branch here.
//
// The server decides membership (is this boss listed at all) on the widest reading
// of the window, UTC+14 to UTC-12, so nobody loses a boss they can still raid. What
// the page then counts down to is the viewer's own clock, which is this.
export function parseLocal(s: string | null | undefined): Date | null {
  if (!s) return null;
  const d = new Date(s);
  return isNaN(d.getTime()) ? null : d;
}

export const dateFmt = new Intl.DateTimeFormat(typeof SITE_LANG !== "undefined" ? SITE_LANG : "en", {
  weekday: "short", month: "short", day: "numeric", hour: "numeric", minute: "2-digit",
});

// relTime renders a duration as "3d 4h" or "2h 15m". Never returns "0m": a
// countdown that has all but expired should read as a minute left, not as nothing.
export function relTime(ms: number): string {
  const totalMin = Math.max(1, Math.floor(ms / 60000));
  const d = Math.floor(totalMin / 1440);
  const h = Math.floor((totalMin % 1440) / 60);
  const m = totalMin % 60;
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}
