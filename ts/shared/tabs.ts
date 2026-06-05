export interface TabDef {
  id: string;
  label: string;
  render: () => HTMLElement;
}

export function buildTabs(tabs: TabDef[], initialTab?: string): HTMLElement {
  const wrapper = document.createElement("div");

  const bar = document.createElement("div");
  bar.className = "tabs";

  const panels = new Map<string, HTMLElement>();

  for (const tab of tabs) {
    const btn = document.createElement("button");
    btn.className = "tab-btn";
    btn.dataset.tab = tab.id;
    btn.textContent = tab.label;

    const panel = document.createElement("div");
    panel.className = "tab-panel";
    panel.dataset.tab = tab.id;

    bar.appendChild(btn);
    panels.set(tab.id, panel);
    wrapper.appendChild(panel);
  }

  // Insert bar at top
  wrapper.insertBefore(bar, wrapper.firstChild);

  function activate(id: string) {
    bar.querySelectorAll(".tab-btn").forEach((b) => {
      (b as HTMLButtonElement).classList.toggle("active", (b as HTMLButtonElement).dataset.tab === id);
    });
    panels.forEach((panel, key) => {
      const active = key === id;
      panel.classList.toggle("active", active);
      if (active && panel.children.length === 0) {
        const tab = tabs.find((t) => t.id === key)!;
        const content = tab.render();
        content.classList.add("fade-up");
        panel.appendChild(content);
      }
    });
  }

  bar.addEventListener("click", (e) => {
    const btn = (e.target as HTMLElement).closest(".tab-btn") as HTMLButtonElement | null;
    if (btn?.dataset.tab) activate(btn.dataset.tab);
  });

  activate(initialTab ?? tabs[0].id);
  return wrapper;
}
