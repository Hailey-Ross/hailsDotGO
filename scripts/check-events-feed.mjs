// Asserts the upstream events feed still looks like the app expects, that every event type
// it carries is reachable from the calendar subscribe panel, and that the titles our own API
// serves came out the far side of that as readable text.
//
// Events are the one data path with no embedded fallback, by design: a compiled in events
// list would be stale the day after it shipped. The trade is that a silent upstream reshape
// has nothing to fall back to, so this script is the early warning. It fails loudly on the
// kind of change that would otherwise just make the events page emptier.
//
//   npm run check:events          fetches the live feed
//   node scripts/check-events-feed.mjs --cached    reads cache/events.json instead
//
// Deliberately NOT part of `npm run check`, matching check-shiny-dex-vs-fandom.mjs. That chain
// has to stay offline and deterministic, and a network fetch inside it would fail an unrelated
// change whenever GitHub is slow. Run this before a release, or when events look wrong.
//
// It defaults to the network because checking cache/events.json is checking our own copy of
// upstream, which cannot detect the upstream drift this script exists to find.
//
// The final section is the odd one out: it reads our own served API rather than upstream, and
// it is the only part that is allowed to skip itself, because our own site can be down or
// mid deploy while upstream is perfectly fine. See the comment above checkServedApi.
// An upstream feed we cannot read is NOT a skip. It is reported as a problem like any other,
// because "upstream went away" is one of the failures this script is here to notice.

import { readFileSync, existsSync } from "node:fs";

const FEED_URL = "https://raw.githubusercontent.com/bigfoott/ScrapedDuck/data/events.min.json";
const CACHE = "cache/events.json";
const API_URL = "https://pogo.hails.app/api/events";

// Keys the app reads. eventID, start and end are load bearing: the first is the detail lookup
// key and the calendar UID, the other two decide current versus upcoming and every DTSTART.
const REQUIRED_KEYS = ["eventID", "name", "eventType", "heading", "link", "image", "start", "end"];

// Mirrors internal/handlers/events_ics.go. An id outside this class is refused by the detail
// endpoint and by the single event calendar download, so it would render an empty modal.
const ID_PATTERN = /^[a-z0-9_-]{1,128}$/;

// Mirrors pogodata.ParseFeedTime. A trailing Z is a real instant, anything else is a floating
// wall clock. A third shape appearing upstream is exactly the drift worth catching.
const UTC_TIME = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z$/;
const FLOATING_TIME = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{3})?$/;

// Named entities like &amp; and numeric ones like &#39;, which is what a title looks like when
// it reached us as HTML and was stored without ever being decoded. Applied ONLY to our own
// API. Upstream legitimately ships these, so running it over the feed above would report
// their normal output as our bug.
const HTML_ENTITY = /&(?:[a-zA-Z][a-zA-Z0-9]{1,31}|#\d{1,7}|#[xX][0-9a-fA-F]{1,6});/;

const problems = [];
const notes = [];
const apiProblems = [];
const apiNotes = [];
let apiChecked = false;

async function loadFeed() {
  if (process.argv.includes("--cached")) {
    if (!existsSync(CACHE)) throw new Error(`${CACHE} does not exist; drop --cached to fetch upstream`);
    notes.push(`read ${CACHE}, which is our own copy and cannot show upstream drift`);
    return JSON.parse(readFileSync(CACHE, "utf8"));
  }
  const res = await fetch(FEED_URL);
  if (!res.ok) throw new Error(`feed fetch failed: HTTP ${res.status}`);
  notes.push("fetched the live feed");
  return await res.json();
}

// The curated subscribe checkboxes, read straight out of the template so the two cannot drift.
function curatedTypes() {
  const html = readFileSync("templates/events.html", "utf8");
  const covered = new Set();
  for (const m of html.matchAll(/data-type="([^"]+)"/g)) {
    for (const t of m[1].split(",")) {
      const type = t.trim();
      if (type && type !== "*") covered.add(type);
    }
  }
  return covered;
}

// Distinct from a feed whose JSON body is literally null, which is a reportable shape below.
const UNREADABLE = Symbol("upstream feed unreadable");

let feed = UNREADABLE;
try {
  feed = await loadFeed();
} catch (err) {
  // Without this the whole script dies here on a DNS failure or a dead host, printing a raw
  // stack trace instead of saying what went wrong. Routed through problems so the exit code
  // convention is unchanged: a genuine upstream problem still exits 1.
  problems.push(`could not read the upstream feed: ${err.message}`);
}

if (feed === UNREADABLE) {
  // Already reported above, and there is no feed to run the shape checks against.
} else if (!Array.isArray(feed)) {
  problems.push(`feed is ${feed === null ? "null" : typeof feed}, expected an array`);
} else if (feed.length === 0) {
  problems.push("feed is an empty array: the app now refuses this, but upstream should not send it");
} else {
  const seen = new Set();
  const types = new Set();
  let floating = 0;
  let utc = 0;

  for (const ev of feed) {
    // A null or non object element is exactly the malformed shape this script is
    // for, so report it rather than throwing on the `in` operator below.
    if (ev === null || typeof ev !== "object" || Array.isArray(ev)) {
      problems.push(`feed contains a ${ev === null ? "null" : typeof ev} element where an event object was expected`);
      continue;
    }
    const id = ev.eventID ?? "(missing)";
    for (const key of REQUIRED_KEYS) {
      if (!(key in ev)) problems.push(`${id}: missing key "${key}"`);
    }
    if (typeof ev.eventID !== "string" || !ID_PATTERN.test(ev.eventID)) {
      problems.push(`${id}: eventID is not in the class the detail endpoint accepts`);
    }
    if (seen.has(ev.eventID)) problems.push(`${id}: duplicate eventID`);
    seen.add(ev.eventID);
    if (ev.eventType) types.add(ev.eventType);

    const form = {};
    for (const field of ["start", "end"]) {
      const v = ev[field];
      if (v === null || v === undefined) continue;
      if (UTC_TIME.test(v)) { utc++; form[field] = "utc"; }
      else if (FLOATING_TIME.test(v)) { floating++; form[field] = "floating"; }
      else problems.push(`${id}: ${field} "${v}" matches neither the UTC nor the floating shape`);
    }
    // A floating start beside an absolute end gives the event no defined duration:
    // the client has to invent a zone for the floating half and they disagree. The
    // exporter drops DTEND in that case, so this reports it rather than failing.
    if (form.start && form.end && form.start !== form.end) {
      notes.push(`${id}: ${form.start} start with a ${form.end} end, so its calendar entry will have no DTEND`);
    }
  }

  const covered = curatedTypes();
  const uncovered = [...types].filter((t) => !covered.has(t)).sort();

  notes.push(`${feed.length} events, ${types.size} distinct types`);
  notes.push(`${floating} floating timestamps, ${utc} UTC timestamps`);
  if (uncovered.length) {
    // Not a failure: ts/events.ts generates a checkbox for any type the curated bundles miss,
    // so these are reachable. Reported so a type worth curating by hand is visible.
    notes.push(`types with no curated checkbox, rendered as generated ones: ${uncovered.join(", ")}`);
  }
}

// Our own API, not upstream. The decode happens as the feed is read in, so the only place a
// surviving entity proves anything is in what we serve. Everything that can go wrong here
// other than an entity is a skip rather than a failure: our own site can be unreachable while
// upstream is fine, and this gets run before a fix is deployed, and neither of those is the
// served data being wrong. A skip says so in the output rather than passing quietly.
// Note the asymmetry with the upstream feed above, which reports rather than skips: an
// unreachable upstream is a fact about upstream, an unreachable pogo.hails.app usually is not.
async function checkServedApi() {
  if (process.argv.includes("--cached")) {
    apiNotes.push(`skipped: --cached means work offline, and ${API_URL} can only be read over the network`);
    return;
  }
  let served;
  try {
    const res = await fetch(API_URL, { signal: AbortSignal.timeout(15000) });
    if (!res.ok) {
      apiNotes.push(`skipped: ${API_URL} answered HTTP ${res.status}, so nothing was checked`);
      return;
    }
    served = await res.json();
  } catch (err) {
    apiNotes.push(`skipped: could not read ${API_URL} (${err.message}), so nothing was checked`);
    return;
  }
  if (!Array.isArray(served)) {
    apiNotes.push(`skipped: ${API_URL} answered with ${served === null ? "null" : typeof served}, not an array, so nothing was checked`);
    return;
  }
  let counted = 0;
  for (const ev of served) {
    if (ev === null || typeof ev !== "object" || Array.isArray(ev)) continue;
    counted++;
    const id = typeof ev.eventID === "string" ? ev.eventID : "(missing)";
    for (const field of ["name", "heading"]) {
      const v = ev[field];
      if (typeof v === "string" && HTML_ENTITY.test(v)) {
        apiProblems.push(`${id}: ${field} still carries an HTML entity: "${v}"`);
      }
    }
  }
  if (counted === 0) {
    // Zero events is what a failed feed load and an unusable cache look like from out here.
    // Passing on it would print a clean bill of health for data nobody actually examined.
    apiNotes.push(`skipped: ${API_URL} served no events at all, so there was nothing to check`);
    return;
  }
  apiChecked = true;
  apiNotes.push(`read ${counted} served events and checked every name and heading for HTML entities`);
}

await checkServedApi();

console.log("upstream feed:");
for (const n of notes) console.log("  " + n);

if (problems.length) {
  console.error(`\nevents feed check FAILED with ${problems.length} problem(s):`);
  for (const p of problems.slice(0, 25)) console.error("  " + p);
  if (problems.length > 25) console.error(`  ...and ${problems.length - 25} more`);
} else {
  console.log("events feed: shape, ids, timestamps and type coverage all check out.");
}

console.log("\nour own served API, which is a separate payload from the feed above:");
for (const n of apiNotes) console.log("  " + n);

if (apiProblems.length) {
  console.error(`\nserved events check FAILED with ${apiProblems.length} problem(s):`);
  for (const p of apiProblems.slice(0, 25)) console.error("  " + p);
  if (apiProblems.length > 25) console.error(`  ...and ${apiProblems.length - 25} more`);
  console.error("  a title we serve should read as plain text, so an entity here is a decode we missed");
} else if (apiChecked) {
  console.log("served events: no HTML entity survived into any name or heading.");
}

// exitCode rather than exit(): a failed fetch leaves a DNS handle mid teardown, and calling
// process.exit() out from under it aborts the Node runtime on Windows with a libuv assertion
// and exit code 127, which would hide the very failure we just printed. Setting the code lets
// the loop drain and still exits 1.
if (problems.length || apiProblems.length) process.exitCode = 1;
