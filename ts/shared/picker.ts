import { pokeSprite, pokeName } from "./gamedata";
import { typeBadge } from "./typecolors";
import type { GameData } from "./types";

export interface PickerEntry {
  key: string;            // unique, e.g. pokemon_name or "raid:MEWTWO"
  name: string;           // english pokemon_name, used for search matching
  label: string;          // localized display name
  sprite: string | null;  // null renders an aligned spacer
  types: string[];        // badges shown in the selected preview
  tag?: string;           // small pill in the dropdown row, e.g. "T5", "Mega"
  group: number;          // sort tiebreaker after match rank; lower first
  data?: unknown;         // caller payload (PokemonStat, RaidBoss)
}

export interface PickerOptions {
  entries: PickerEntry[];
  placeholder: string;
  maxResults?: number;    // default 10
  showOnFocus?: boolean;  // when true, focusing the empty input lists the first entries
  preview?: boolean;      // default true; render the selected preview block
  onSelect(entry: PickerEntry): void;
  onClear?(): void;
}

export interface Picker {
  root: HTMLElement;
  input: HTMLInputElement;
  getSelected(): PickerEntry | null;
  clear(fire?: boolean): void;
  focus(): void;
  isDropdownOpen(): boolean;
}

function spriteImg(src: string, alt: string, cls: string): HTMLElement {
  const img = document.createElement("img");
  img.src = src;
  img.alt = alt;
  img.className = cls;
  img.loading = "lazy";
  img.decoding = "async";
  img.addEventListener("error", () => { img.style.visibility = "hidden"; });
  return img;
}

const norm = (s: string) => s.toLowerCase().replace(/_/g, " ");

export function createPicker(opts: PickerOptions): Picker {
  const maxResults = opts.maxResults ?? 10;
  const showPreview = opts.preview !== false;
  let selected: PickerEntry | null = null;

  const root = document.createElement("div");
  root.className = "pokemon-picker picker-inline";

  const searchRow = document.createElement("div");
  searchRow.className = "picker-search-row";

  const input = document.createElement("input");
  input.type = "text";
  input.className = "search-input";
  input.placeholder = opts.placeholder;
  input.setAttribute("autocomplete", "off");

  const clearBtn = document.createElement("button");
  clearBtn.className = "picker-clear-btn";
  clearBtn.textContent = "×";
  clearBtn.hidden = true;
  clearBtn.setAttribute("aria-label", "Clear selection");

  searchRow.appendChild(input);
  searchRow.appendChild(clearBtn);
  root.appendChild(searchRow);

  const dropdown = document.createElement("div");
  dropdown.className = "picker-dropdown";
  dropdown.hidden = true;
  root.appendChild(dropdown);

  const previewEl = document.createElement("div");
  previewEl.className = "picker-selected";
  previewEl.hidden = true;
  root.appendChild(previewEl);

  function renderPreview() {
    previewEl.innerHTML = "";
    if (!showPreview || !selected) { previewEl.hidden = true; return; }
    if (selected.sprite) {
      previewEl.appendChild(spriteImg(selected.sprite, selected.label, "picker-selected-sprite"));
    }
    const info = document.createElement("div");
    info.className = "picker-selected-info";
    const nameEl = document.createElement("span");
    nameEl.className = "picker-selected-name";
    nameEl.textContent = selected.label;
    info.appendChild(nameEl);
    if (selected.types.length) {
      const typesRow = document.createElement("div");
      typesRow.className = "picker-selected-types";
      selected.types.forEach((t) => typesRow.appendChild(typeBadge(t)));
      info.appendChild(typesRow);
    }
    previewEl.appendChild(info);
    previewEl.hidden = false;
  }

  function select(entry: PickerEntry) {
    selected = entry;
    input.value = entry.label;
    dropdown.hidden = true;
    clearBtn.hidden = false;
    renderPreview();
    opts.onSelect(entry);
  }

  function clear(fire = true) {
    selected = null;
    input.value = "";
    clearBtn.hidden = true;
    dropdown.hidden = true;
    previewEl.hidden = true;
    previewEl.innerHTML = "";
    if (fire) opts.onClear?.();
  }

  function buildDropdown(query: string) {
    dropdown.innerHTML = "";
    const q = norm(query);

    let matches: PickerEntry[];
    if (!q) {
      if (!opts.showOnFocus) { dropdown.hidden = true; return; }
      matches = opts.entries.slice(0, maxResults);
    } else {
      const rank = (e: PickerEntry) => {
        const n = norm(e.name), l = norm(e.label);
        if (n === q || l === q) return 0;
        if (n.startsWith(q) || l.startsWith(q)) return 1;
        if (n.includes(q) || l.includes(q)) return 2;
        return 3;
      };
      matches = opts.entries
        .map((e, i) => ({ e, i, r: rank(e) }))
        .filter((m) => m.r < 3)
        .sort((a, b) => a.r - b.r || a.e.group - b.e.group || a.i - b.i)
        .slice(0, maxResults)
        .map((m) => m.e);
    }

    if (!matches.length) {
      const none = document.createElement("button");
      none.className = "picker-option no-match";
      none.textContent = "No Pokémon found";
      none.disabled = true;
      dropdown.appendChild(none);
    } else {
      for (const e of matches) {
        const opt = document.createElement("button");
        opt.className = "picker-option";
        if (e.sprite) {
          opt.appendChild(spriteImg(e.sprite, e.label, "picker-option-sprite"));
        } else {
          const spacer = document.createElement("span");
          spacer.className = "picker-option-sprite";
          opt.appendChild(spacer);
        }
        const nameEl = document.createElement("span");
        nameEl.className = "picker-option-name";
        nameEl.textContent = e.label;
        opt.appendChild(nameEl);
        if (e.tag) {
          const tagEl = document.createElement("span");
          tagEl.className = "picker-option-tag";
          tagEl.textContent = e.tag;
          opt.appendChild(tagEl);
        }
        opt.addEventListener("mousedown", (ev) => {
          ev.preventDefault();
          select(e);
        });
        dropdown.appendChild(opt);
      }
    }
    dropdown.hidden = false;
  }

  input.addEventListener("input", () => {
    if (selected) {
      selected = null;
      clearBtn.hidden = true;
      previewEl.hidden = true;
      previewEl.innerHTML = "";
      opts.onClear?.();
    }
    buildDropdown(input.value.trim());
  });

  input.addEventListener("focus", () => {
    if (opts.showOnFocus && !selected && !input.value.trim()) buildDropdown("");
  });

  input.addEventListener("blur", () => {
    setTimeout(() => { dropdown.hidden = true; }, 150);
  });

  input.addEventListener("keydown", (e) => {
    if (e.key === "Escape") dropdown.hidden = true;
    if (e.key === "Enter" && !dropdown.hidden) {
      const first = dropdown.querySelector<HTMLButtonElement>(".picker-option:not(.no-match)");
      if (first) first.dispatchEvent(new MouseEvent("mousedown"));
    }
    if (e.key === "ArrowDown") {
      e.preventDefault();
      const first = dropdown.querySelector<HTMLButtonElement>(".picker-option");
      if (first) first.focus();
    }
  });

  clearBtn.addEventListener("click", () => {
    clear();
    input.focus();
  });

  document.addEventListener("click", (e) => {
    if (!root.contains(e.target as Node)) dropdown.hidden = true;
  });

  return {
    root,
    input,
    getSelected: () => selected,
    clear,
    focus: () => input.focus(),
    isDropdownOpen: () => !dropdown.hidden,
  };
}

// Standard entry list covering every Pokemon in the game data, deduped by name.
export function pokemonEntries(data: GameData): PickerEntry[] {
  const out: PickerEntry[] = [];
  const seen = new Set<string>();
  for (const p of data.pokemon ?? []) {
    if (seen.has(p.pokemon_name)) continue;
    seen.add(p.pokemon_name);
    out.push({
      key: p.pokemon_name,
      name: p.pokemon_name,
      label: pokeName(data, p.pokemon_name),
      sprite: pokeSprite(p.pokemon_id),
      types: data.pokemonTypes?.[p.pokemon_name] ?? [],
      group: 1,
      data: p,
    });
  }
  return out;
}
