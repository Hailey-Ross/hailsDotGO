// Sound and haptics: short synthesized tones (Web Audio, no sound files) and vibration (Android;
// iPhone and iPad web apps cannot vibrate). Both follow the Appearance settings: sound on or off,
// its volume, and haptics on or off. Every call is safe when either is missing or switched off.
import { settings } from './theme';

let ac: AudioContext | null = null;
let master: GainNode | null = null;

function audio(): AudioContext | null {
  const S = settings();
  if (!S.sound || S.volume <= 0) return null;
  if (!ac) {
    const AC = window.AudioContext || (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
    if (!AC) return null;
    // "ambient" lets the tones mix with whatever else is playing instead of pausing it (iOS).
    try { (navigator as Navigator & { audioSession?: { type: string } }).audioSession!.type = 'ambient'; } catch { /* not offered */ }
    ac = new AC();
    master = ac.createGain();
    master.connect(ac.destination);
  }
  master!.gain.value = (S.volume / 100) * 0.6;
  if (ac.state === 'suspended') ac.resume();
  return ac;
}

function tone(f0: number, f1: number, dur: number, when = 0, type: OscillatorType = 'sine', gain = 0.15): void {
  const a = audio();
  if (!a || !master) return;
  const t = a.currentTime + when, o = a.createOscillator(), g = a.createGain();
  o.type = type;
  o.frequency.setValueAtTime(f0, t);
  if (f1) o.frequency.exponentialRampToValueAtTime(f1, t + dur);
  g.gain.setValueAtTime(0.0001, t);
  g.gain.exponentialRampToValueAtTime(gain, t + 0.01);
  g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
  o.connect(g);
  g.connect(master);
  o.start(t);
  o.stop(t + dur + 0.02);
}

const SFX: Record<string, (on?: boolean) => void> = {
  tap: () => tone(880, 760, 0.05, 0, 'triangle', 0.08),
  open: () => { tone(440, 660, 0.12, 0, 'sine', 0.12); tone(660, 880, 0.12, 0.07, 'sine', 0.08); },
  close: () => tone(620, 400, 0.13, 0, 'sine', 0.1),
  toggle: (on) => tone(on ? 600 : 760, on ? 900 : 520, 0.07, 0, 'triangle', 0.1),
  stamp: () => { tone(140, 70, 0.16, 0, 'sine', 0.3); tone(1046, 1046, 0.22, 0.12, 'sine', 0.09); tone(1568, 1568, 0.3, 0.2, 'sine', 0.06); },
  hop: () => tone(520, 980, 0.09, 0, 'sine', 0.07),
};

export function buzz(pattern: number | number[]): void {
  if (!settings().haptics) return;
  // Browsers refuse vibration until the visitor has really tapped the page (and log a warning when
  // asked earlier), so wait for that.
  const ua = (navigator as Navigator & { userActivation?: { hasBeenActive: boolean } }).userActivation;
  if (ua && !ua.hasBeenActive) return;
  try { navigator.vibrate?.(pattern); } catch { /* no vibration here */ }
}

export type FeelKind = 'tap' | 'open' | 'close' | 'toggle' | 'stamp' | 'hop';

/** Play the tone for an interaction and give the matching small vibration. */
export function feedback(kind: FeelKind, on?: boolean): void {
  try { SFX[kind]?.(on); } catch { /* audio unavailable */ }
  buzz(kind === 'stamp' ? [14, 40, 22] : kind === 'open' ? 12 : 8);
}
