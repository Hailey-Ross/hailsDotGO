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

// The pieces a calendar needs, all from Intl rather than from locale strings: they
// are already translated everywhere, and the raids page carries no localised date
// wording of its own.
const siteLang = () => (typeof SITE_LANG !== "undefined" ? SITE_LANG : "en");

// monthDayFmt labels a calendar cell that opens a new month, so "1" reads as "1 Oct".
export const monthDayFmt = new Intl.DateTimeFormat(siteLang(), { month: "short", day: "numeric" });

// weekdayFmt heads a calendar column, and also labels a cell on a phone, where the
// grid collapses to one column and the column heads are gone.
export const weekdayFmt = new Intl.DateTimeFormat(siteLang(), { weekday: "short" });

// weekStartsOn is the first column of the calendar: Sunday for English, Monday
// everywhere else. Intl.Locale.weekInfo would answer this properly and Firefox does
// not implement it, so this is the honest two case version rather than a lookup that
// silently falls back for a third of viewers.
export function weekStartsOn(): number {
  return siteLang().toLowerCase().startsWith("en") ? 0 : 1;
}

// startOfWeek is the Sunday or Monday on or before d, at local midnight.
export function startOfWeek(d: Date): Date {
  const out = new Date(d.getFullYear(), d.getMonth(), d.getDate());
  const shift = (out.getDay() - weekStartsOn() + 7) % 7;
  out.setDate(out.getDate() - shift);
  return out;
}

// dayKey identifies a local calendar day, for bucketing rotations into cells.
export function dayKey(d: Date): string {
  return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
}

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
