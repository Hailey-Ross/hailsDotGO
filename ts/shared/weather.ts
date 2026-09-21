// The in-game weather banner.
//
// Weather boost changes which types are strong, so this belongs on the page
// where that actually changes a decision: /raids, where the counter rankings
// are. It used to live only on the raid finder, which meant it was invisible
// whenever that section was switched off for maintenance, and the counters page
// never showed it at all.
//
// The server does the interpreting. /api/weather answers with a PoGo weather
// name already mapped from the WMO code and the boosted types that go with it,
// so there is no forecast logic here.

const WEATHER_EMOJI: Record<string, string> = {
  "Clear": "☀️",
  "Partly Cloudy": "⛅",
  "Overcast": "☁️",
  "Rainy": "🌧️",
  "Snow": "❄️",
  "Fog": "🌫️",
  "Windy": "💨",
  "Extreme": "⚠️",
};

function escHTML(s: unknown): string {
  return String(s).replace(/[&<>"']/g, (c) => (
    { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c] as string
  ));
}

/**
 * Fills `#weather-banner-slot` with the current in-game weather.
 *
 * Silent in every failure case on purpose: the weather is a nicety beside the
 * counters, and a missing city, a geocode that found nothing or an upstream
 * hiccup should leave the page exactly as it was rather than show an error for
 * something nobody asked for.
 *
 * @param loggedIn the endpoint needs a session, since the location comes from
 *   the trainer's own profile
 * @param boostSuffix the translated tail of "Fire, Grass types are boosted"
 */
export function loadWeatherBanner(loggedIn: boolean, boostSuffix: string): void {
  if (!loggedIn) return;
  const slot = document.getElementById("weather-banner-slot");
  if (!slot) return;

  fetch("/api/weather")
    .then((r) => (r.ok ? r.json() : null))
    .then((w) => {
      // An empty pogo_weather is the ordinary answer for a trainer with no city
      // set, not a failure. Render nothing rather than an empty banner.
      if (!w || !w.pogo_weather) return;
      const emoji = WEATHER_EMOJI[w.pogo_weather] || "🌡️";
      const types = (w.boosted_types || []).join(", ");
      let text = "<strong>" + escHTML(w.pogo_weather) + "</strong>";
      if (types) text += ": " + escHTML(types) + " " + escHTML(boostSuffix);
      slot.innerHTML =
        '<div class="weather-banner"><span class="weather-banner-icon">' + emoji +
        '</span><span class="weather-banner-text">' + text + "</span></div>";
    })
    .catch(() => {});
}
