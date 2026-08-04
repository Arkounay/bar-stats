/*
 * Application shell: setup flow, replay list, match detail, and the aggregate
 * dashboard.
 *
 * Series colour follows the entity, never its rank. With eight or fewer teams
 * plotted each holds its own categorical slot for as long as it stays
 * selected, so narrowing the selection never repaints the survivors. Past
 * eight, slots would have to be reused, so the chart switches to colouring by
 * ally team and identity moves to the roster, the tooltip and hover emphasis —
 * generating a ninth hue is not an option.
 */
(() => {
  const SERIES_SLOTS = 8;
  const seriesColor = slot => `var(--series-${(slot % SERIES_SLOTS) + 1})`;

  const state = {
    metrics: [],
    groups: [],
    replays: [],
    listItems: [],   // current filtered set backing the windowed list
    window: null,    // { first, last } rows currently in the DOM
    scrollFrame: null,
    filter: '',
    page: 'replays',
    selectedId: null,
    detail: null,
    highlightSelf: false,
    previews: false,
    viewMode: 'team',     // 'ally' | 'team' — players by default
    valueMode: 'total',   // 'total' | 'rate'
    mainMetric: 'metalProduced',
    selectedTeams: new Set(),
    teamSlots: new Map(),
    multiples: null, // { signature, node } — cached small-multiples grid
    inGameColors: localStorage.getItem('inGameColors') === '1',
    pinYou: false,
    showTable: false,
    activeChart: null,
    pollTimer: null,
    revision: null,
  };

  const $ = id => document.getElementById(id);

  async function api(path, options) {
    const res = await fetch(path, options);
    const body = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(body.error || `Request failed (${res.status})`);
    return body;
  }

  /* ---------------- Boot ---------------- */

  async function init() {
    setupTheme();
    $('theme-btn').addEventListener('click', toggleTheme);
    $('path-form').addEventListener('submit', e => { e.preventDefault(); submitPath($('path-input').value.trim()); });
    $('search').addEventListener('input', e => {
      state.filter = e.target.value.toLowerCase();
      // A narrower result set can be shorter than the current scroll offset.
      $('replay-scroll').scrollTop = 0;
      renderList();
    });

    // Redraw the window as the list scrolls, at most once per frame.
    $('replay-scroll').addEventListener('scroll', () => {
      if (state.scrollFrame) return;
      state.scrollFrame = requestAnimationFrame(() => {
        state.scrollFrame = null;
        renderListWindow();
      });
    }, { passive: true });

    // A taller pane shows more rows than the last window covered.
    new ResizeObserver(() => renderListWindow()).observe($('replay-scroll'));
    $('rescan-btn').addEventListener('click', onRescan);
    $('settings-btn').addEventListener('click', openSettings);
    $('settings-close').addEventListener('click', closeSettings);
    $('settings-modal').addEventListener('click', e => {
      if (e.target === $('settings-modal')) closeSettings();
    });
    document.addEventListener('keydown', e => {
      if (e.key === 'Escape' && !$('settings-modal').hidden) closeSettings();
    });
    $('settings-path-form').addEventListener('submit', e => {
      e.preventDefault();
      saveSettings({ path: $('settings-path-input').value.trim() });
    });
    $('player-name-save').addEventListener('click', () => {
      saveSettings({ playerName: $('player-name-input').value.trim() });
    });
    $('highlight-self-input').addEventListener('change', e => {
      state.highlightSelf = e.target.checked;
      saveSettings({ highlightSelf: e.target.checked }, false);
    });
    for (const tab of document.querySelectorAll('.nav-tab')) {
      tab.addEventListener('click', () => {
        // Returning to Replays keeps whichever replay was open.
        navigate(tab.dataset.page === 'dashboard' ? '/dashboard' : pathForReplay(state.selectedId));
      });
    }
    window.addEventListener('popstate', applyRoute);

    const s = await api('/api/state');
    if (!s.configured) {
      showSetup(s);
      return;
    }
    state.highlightSelf = !!s.highlightSelf;
    state.previews = !!s.previews;
    await startBrowsing();
  }

  async function startBrowsing() {
    $('setup').hidden = true;
    $('nav-tabs').hidden = false;
    $('rescan-btn').hidden = false;
    $('settings-btn').hidden = false;

    if (!state.metrics.length) {
      const m = await api('/api/metrics');
      state.metrics = m.metrics;
      state.groups = m.groups;
    }
    // The list has to exist before a deep-linked row can be scrolled to.
    await refreshReplays();
    await applyRoute();
    pollState();
  }

  function showPage(page) {
    state.page = page;
    $('browser').hidden = page !== 'replays';
    $('dashboard').hidden = page !== 'dashboard';
    for (const tab of document.querySelectorAll('.nav-tab')) {
      if (tab.dataset.page === page) tab.setAttribute('aria-current', 'page');
      else tab.removeAttribute('aria-current');
    }
    if (page === 'dashboard') renderDashboard();
  }

  /* ---------------- Routing ---------------- */

  /*
   * The current view lives in the address bar, so the back button, deep links
   * and a refresh all behave. The server hands the app shell back for these
   * paths; see spaFallback.
   */
  function routeFromLocation() {
    const parts = location.pathname.split('/').filter(Boolean);
    if (parts[0] === 'dashboard') return { page: 'dashboard', id: null };
    if (parts[0] === 'replays' && parts[1]) return { page: 'replays', id: parts[1] };
    return { page: 'replays', id: null };
  }

  function pathForReplay(id) { return id ? `/replays/${id}` : '/replays'; }

  /** Pushes a new URL and renders it. */
  function navigate(path) {
    if (path === location.pathname) return;
    history.pushState(null, '', path);
    applyRoute();
  }

  /** Renders whatever the address bar currently says. */
  async function applyRoute() {
    const route = routeFromLocation();
    showPage(route.page);
    if (route.page !== 'replays') return;

    if (!route.id) {
      clearSelection();
      return;
    }
    if (route.id !== state.selectedId) await selectReplay(route.id);
    scrollSelectedIntoView();
  }

  function clearSelection() {
    state.selectedId = null;
    state.detail = null;
    if (state.activeChart) { state.activeChart.destroy(); state.activeChart = null; }
    updateListSelection();
    $('detail-pane').innerHTML =
      '<div class="empty-state"><p>Select a replay to see its statistics.</p></div>';
  }

  /** Brings the selected row into view — a deep link can land far down the list. */
  function scrollSelectedIntoView() {
    const idx = state.listItems.findIndex(r => r.id === state.selectedId);
    if (idx < 0) return;
    const scroller = $('replay-scroll');
    const top = idx * ROW_HEIGHT;
    if (top >= scroller.scrollTop && top + ROW_HEIGHT <= scroller.scrollTop + scroller.clientHeight) {
      return; // already visible
    }
    scroller.scrollTop = Math.max(0, top - scroller.clientHeight / 2);
    renderListWindow();
  }

  /* ---------------- Setup screen ---------------- */

  // Only reached when the server reported no replay folder, which is exactly
  // when /api/state includes the detected suggestions.
  function showSetup(s) {
    $('browser').hidden = true;
    $('dashboard').hidden = true;
    $('setup').hidden = false;
    $('nav-tabs').hidden = true;
    $('rescan-btn').hidden = true;
    $('settings-btn').hidden = true;
    $('index-status').hidden = true;

    const suggestions = s.suggestions || [];
    $('suggestions-block').hidden = suggestions.length === 0;
    renderSuggestions($('suggestions'), suggestions, submitPath);
    if (s.demosPath) $('path-input').value = s.demosPath;
  }

  /** Renders detected replay folders as pickable rows. Shared by setup and settings. */
  function renderSuggestions(list, suggestions, onPick) {
    list.innerHTML = '';
    for (const c of suggestions) {
      const li = document.createElement('li');
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'suggestion';

      const main = document.createElement('span');
      main.className = 'suggestion-main';
      const label = document.createElement('span');
      label.className = 'suggestion-label';
      label.textContent = c.label;
      const path = document.createElement('span');
      path.className = 'suggestion-path';
      path.textContent = c.path;
      main.append(label, document.createElement('br'), path);

      const count = document.createElement('span');
      count.className = 'suggestion-count';
      count.textContent = `${c.demoCount} replay${c.demoCount === 1 ? '' : 's'}`;

      btn.append(main, count);
      btn.addEventListener('click', () => onPick(c.path));
      li.appendChild(btn);
      list.appendChild(li);
    }
  }

  async function submitPath(path) {
    const err = $('path-error');
    err.hidden = true;
    try {
      await api('/api/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path }),
      });
      state.replays = [];
      state.revision = null;
      state.selectedId = null;
      state.detail = null;
      // A replay id from the previous folder is meaningless now.
      history.replaceState(null, '', '/replays');
      const s = await api('/api/state');
      state.previews = !!s.previews;
      await startBrowsing();
    } catch (e) {
      err.textContent = e.message;
      err.hidden = false;
    }
  }

  async function onRescan() {
    await api('/api/rescan', { method: 'POST' });
    pollState();
  }

  /* ---------------- Settings dialog ---------------- */

  async function openSettings() {
    const s = await api('/api/settings');
    $('settings-current').textContent = s.demosPath;
    $('settings-path-input').value = s.demosPath || '';
    $('player-name-input').value = s.playerName || '';
    $('highlight-self-input').checked = !!s.highlightSelf;
    state.highlightSelf = !!s.highlightSelf;
    $('settings-error').hidden = true;
    renderSuggestions($('settings-suggestions'), s.suggestions || [], path => saveSettings({ path }));
    renderWatchModes(s.watchMode);
    renderNameSuggestions();

    const meta = $('settings-meta');
    meta.innerHTML = '';
    const rows = [
      ['Settings file', s.configFile],
      ['Index cache', s.cacheDir || '(disabled)'],
      ['Map previews', s.previews ? 'available from the installed game' : 'not found next to this replay folder'],
    ];
    for (const [k, v] of rows) {
      const dt = document.createElement('dt');
      dt.textContent = k;
      const dd = document.createElement('dd');
      dd.textContent = v;
      meta.append(dt, dd);
    }
    $('settings-modal').hidden = false;
  }

  function closeSettings() { $('settings-modal').hidden = true; }

  const WATCH_MODES = [
    {
      value: 'events',
      title: 'Filesystem events (recommended)',
      note: 'Reacts as soon as a match finishes. Falls back to polling automatically if the folder is somewhere events are not delivered.',
    },
    {
      value: 'poll',
      title: 'Check every 15 seconds',
      note: 'Re-lists the folder on a timer. Use this if the replay folder is on a network share or a synced drive.',
    },
    {
      value: 'off',
      title: 'Only when I press Rescan',
      note: 'No background checking at all.',
    },
  ];

  function renderWatchModes(current) {
    const box = $('watch-mode');
    box.innerHTML = '';
    for (const mode of WATCH_MODES) {
      const label = document.createElement('label');
      label.className = 'radio-option';
      const input = document.createElement('input');
      input.type = 'radio';
      input.name = 'watchMode';
      input.value = mode.value;
      input.checked = mode.value === current;
      input.addEventListener('change', () => saveSettings({ watchMode: mode.value }, false));

      const text = document.createElement('span');
      const title = document.createElement('span');
      title.className = 'radio-option-title';
      title.textContent = mode.title;
      const note = document.createElement('span');
      note.className = 'radio-option-note';
      note.textContent = mode.note;
      text.append(title, document.createElement('br'), note);

      label.append(input, text);
      box.appendChild(label);
    }
  }

  /*
   * Offers names taken from the indexed replays. Typing an in-game name from
   * memory is error-prone, and an near-miss silently yields an empty
   * dashboard, so the likely candidates are offered directly.
   */
  function renderNameSuggestions() {
    const host = $('player-name-suggest');
    host.innerHTML = '';
    const counts = new Map();
    for (const r of state.replays) {
      for (const at of r.allyTeams || []) {
        for (const n of at.names) counts.set(n, (counts.get(n) || 0) + 1);
      }
    }
    const top = [...counts.entries()]
      .filter(([n]) => !/^BARbarianAI|^\w+AI\(/.test(n))
      .sort((a, b) => b[1] - a[1])
      .slice(0, 6);
    if (!top.length) return;

    const note = document.createElement('p');
    note.className = 'field-note';
    note.textContent = 'Most frequent names in your replays:';
    host.appendChild(note);

    const row = document.createElement('div');
    row.className = 'legend';
    for (const [name, n] of top) {
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'btn btn-quiet';
      btn.textContent = `${name} (${n})`;
      btn.addEventListener('click', () => {
        $('player-name-input').value = name;
        saveSettings({ playerName: name });
      });
      row.appendChild(btn);
    }
    host.appendChild(row);
  }

  async function saveSettings(patch, close = true) {
    const err = $('settings-error');
    err.hidden = true;
    try {
      await api('/api/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(patch),
      });
      // The player name changes every row's result marker, so pull a fresh
      // list rather than patching it locally.
      if (patch.path !== undefined || patch.playerName !== undefined) {
        state.revision = null;
        await refreshReplays();
        if (state.detail) await selectReplay(state.selectedId);
        if (state.page === 'dashboard') renderDashboard();
      }
      if (close) closeSettings();
    } catch (e) {
      err.textContent = e.message;
      err.hidden = false;
    }
  }

  /* ---------------- Polling ---------------- */

  async function refreshReplays() {
    const data = await api('/api/replays');
    state.replays = data.replays || [];
    state.revision = data.revision;
    renderProgress(data.progress);
    renderList();
    if (state.page === 'dashboard') renderDashboard();
  }

  async function pollState() {
    clearTimeout(state.pollTimer);
    let phase = 'ready';
    try {
      const s = await api('/api/state');
      phase = (s.progress && s.progress.phase) || 'ready';
      renderProgress(s.progress);
      if (s.revision !== undefined && s.revision !== state.revision) await refreshReplays();
    } catch {
      // The server may be restarting; keep polling rather than giving up.
    }
    const busy = phase === 'scanning' || phase === 'enriching';
    state.pollTimer = setTimeout(pollState, busy ? 900 : 4000);
  }

  function renderProgress(p) {
    const box = $('index-status');
    if (!p || p.phase === 'idle' || p.phase === 'ready') {
      box.hidden = true;
      return;
    }
    box.hidden = false;
    const pct = p.total ? Math.round((p.done / p.total) * 100) : 0;
    $('progress-fill').style.width = `${pct}%`;
    const verb = p.phase === 'scanning' ? 'Reading replays' : 'Loading statistics';
    $('status-text').textContent = p.total ? `${verb} ${p.done}/${p.total}` : `${verb}…`;
  }

  /* ---------------- Map previews ---------------- */

  /** Builds a preview image that removes itself if the game has no thumbnail. */
  function mapThumb(mapName, sizeClass) {
    if (!state.previews || !mapName) return null;
    const img = document.createElement('img');
    img.className = `map-thumb ${sizeClass}`;
    img.loading = 'lazy';
    img.alt = ''; // decorative: the map name is already in the text
    img.src = `/api/maps/${encodeURIComponent(mapName)}/preview.png`;
    img.addEventListener('error', () => img.remove());
    return img;
  }

  /* ---------------- Replay list ---------------- */

  function filteredReplays() {
    if (!state.filter) return state.replays;
    const q = state.filter;
    return state.replays.filter(r =>
      r.map.toLowerCase().includes(q) ||
      (r.allyTeams || []).some(at => at.names.some(n => n.toLowerCase().includes(q))));
  }

  /*
   * The list is windowed: with several hundred replays, building every row
   * meant thousands of nodes rebuilt on every filter keystroke and — because
   * selecting a replay re-rendered the list to move the highlight — on every
   * click. Only the rows on screen are built; the rest is a sized spacer.
   *
   * The row pitch is owned by the stylesheet (--row-height) and read here, so
   * restyling a row cannot silently desynchronise the positioning maths.
   */
  const ROW_HEIGHT =
    parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--row-height')) || 64;
  const OVERSCAN = 6;

  /** Recomputes the filtered set, then draws the visible window. */
  function renderList() {
    state.listItems = filteredReplays();
    $('list-count').textContent = state.filter
      ? `${state.listItems.length} of ${state.replays.length} replays`
      : `${state.replays.length} replays`;
    $('replay-list').style.height = `${state.listItems.length * ROW_HEIGHT}px`;
    // The contents changed, so the rows on screen are stale even if the
    // window bounds work out the same.
    state.window = null;
    renderListWindow();
  }

  function renderListWindow() {
    const scroller = $('replay-scroll');
    const list = $('replay-list');
    const items = state.listItems;

    const first = Math.max(0, Math.floor(scroller.scrollTop / ROW_HEIGHT) - OVERSCAN);
    const last = Math.min(items.length,
      Math.ceil((scroller.scrollTop + scroller.clientHeight) / ROW_HEIGHT) + OVERSCAN);

    // Skip the rebuild when the same slice is already on screen — scrolling
    // fires far more often than the window actually moves.
    if (state.window && state.window.first === first && state.window.last === last) return;
    state.window = { first, last };

    list.innerHTML = '';
    for (let i = first; i < last; i++) {
      const r = items[i];
      const li = document.createElement('li');
      li.style.top = `${i * ROW_HEIGHT}px`;
      li.dataset.replayId = r.id;

      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'replay-item';
      if (r.id === state.selectedId) btn.setAttribute('aria-current', 'true');

      const body = document.createElement('div');
      body.className = 'replay-item-body';
      const thumb = mapThumb(r.map, 'map-thumb-list');
      if (thumb) body.appendChild(thumb);

      const text = document.createElement('div');
      text.className = 'replay-item-text';

      const top = document.createElement('div');
      top.className = 'replay-item-top';
      const map = document.createElement('span');
      map.className = 'replay-map';
      map.textContent = r.map || r.fileName;
      const dur = document.createElement('span');
      dur.className = 'replay-duration';
      dur.textContent = r.durationSeconds ? Chart.formatTime(r.durationSeconds) : '—';
      top.append(map, dur);

      const sub = document.createElement('div');
      sub.className = 'replay-item-sub';
      const played = new Date(r.played);
      const when = document.createElement('span');
      when.textContent = played.toLocaleDateString(undefined, {
        year: 'numeric', month: 'short', day: 'numeric',
      }) + ' ' + played.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
      sub.appendChild(when);

      // The match shape says more at a glance than a raw head count, and it
      // already carries "vs AI" where relevant, so no separate AI count.
      const shape = document.createElement('span');
      shape.className = 'replay-shape';
      shape.textContent = r.format || `${r.playerCount} players`;
      sub.appendChild(shape);

      // Result is shown as a dot plus a letter, never colour alone.
      if (r.youPlayed && r.decided) {
        const tag = document.createElement('span');
        tag.className = `result-tag ${r.youWon ? 'result-tag-win' : 'result-tag-loss'}`;
        const dot = document.createElement('span');
        dot.className = `result-dot ${r.youWon ? 'result-win' : 'result-loss'}`;
        tag.append(dot, document.createTextNode(r.youWon ? ' Won' : ' Lost'));
        sub.appendChild(tag);
      }
      if (r.enriched && !r.hasStats) {
        const tag = document.createElement('span');
        tag.className = 'tag tag-nostats';
        tag.textContent = 'no stats';
        tag.title = 'This recording was cut short, so the game never wrote its statistics.';
        sub.appendChild(tag);
      }

      text.append(top, sub);
      body.appendChild(text);
      btn.appendChild(body);
      btn.addEventListener('click', () => navigate(pathForReplay(r.id)));
      li.appendChild(btn);
      list.appendChild(li);
    }
  }

  /**
   * Moves the selected-row marker without rebuilding the list. Selecting a
   * replay used to re-render every row purely to shift this one attribute.
   */
  function updateListSelection() {
    for (const li of $('replay-list').children) {
      const btn = li.firstElementChild;
      if (!btn) continue;
      if (li.dataset.replayId === state.selectedId) btn.setAttribute('aria-current', 'true');
      else btn.removeAttribute('aria-current');
    }
  }

  /* ---------------- Detail ---------------- */

  async function selectReplay(id) {
    if (!id) return;
    state.selectedId = id;
    updateListSelection();
    const pane = $('detail-pane');
    pane.innerHTML = '<div class="empty-state"><p>Loading statistics…</p></div>';
    try {
      const detail = await api(`/api/replays/${id}`);
      state.detail = detail;
      resetSelection(detail);
      renderDetail();
    } catch (e) {
      pane.innerHTML = '';
      const err = document.createElement('div');
      err.className = 'notice';
      err.textContent = `Could not read this replay: ${e.message}`;
      pane.appendChild(err);
    }
  }

  /** Starts with every team plotted; colour mode follows from how many that is. */
  function resetSelection(detail) {
    state.selectedTeams = new Set(detail.teams.map(t => t.id));
    state.teamSlots = new Map();
    state.multiples = null; // cached grid belongs to the previous replay
    // Honour the saved preference, but only when the user is in this match.
    state.pinYou = state.highlightSelf && detail.teams.some(t => t.isYou);
    state.viewMode = 'team';
    refreshColorAssignment();
  }

  /*
   * Decides how selected teams are coloured, and hands out slots.
   *
   * Up to eight, every team gets its own hue and keeps it: slots are only
   * assigned to teams that lack one, so deselecting a team frees its slot
   * without disturbing anyone else. Beyond eight there are not enough
   * distinguishable hues, so the whole chart switches to ally-team colour and
   * individual lines are picked out by hovering.
   */
  function colorMode() {
    return state.selectedTeams.size > SERIES_SLOTS ? 'ally' : 'team';
  }

  function refreshColorAssignment() {
    if (colorMode() === 'ally') return;
    for (const [id] of state.teamSlots) {
      if (!state.selectedTeams.has(id)) state.teamSlots.delete(id);
    }
    for (const id of state.selectedTeams) {
      if (state.teamSlots.has(id)) continue;
      const used = new Set(state.teamSlots.values());
      for (let i = 0; i < SERIES_SLOTS; i++) {
        if (!used.has(i)) { state.teamSlots.set(id, i); break; }
      }
    }
  }

  function allyIndex(allyTeamID) {
    const idx = state.detail.allyTeams.findIndex(a => a.id === allyTeamID);
    return idx < 0 ? 0 : idx;
  }

  /** Teams by id. Built per render pass; the alternative is a scan per member. */
  function teamIndex() {
    return new Map(state.detail.teams.map(t => [t.id, t]));
  }

  /*
   * By default series take the validated categorical palette, which is checked
   * for colour-blind separation and contrast. In-game colours are opt-in: they
   * are what the player actually saw in the match, at the cost of those
   * guarantees — teammates are often near-identical and some are unreadable
   * against the chart surface. A team with no recorded colour falls back.
   */
  function colorForTeam(team) {
    if (state.inGameColors && team.color) return readableColor(team.color);
    if (colorMode() === 'ally') return seriesColor(allyIndex(team.allyTeamID));
    return seriesColor(state.teamSlots.get(team.id) ?? 0);
  }

  /*
   * Keeps an in-game colour legible against the chart surface.
   *
   * The game's palette is mostly bright, but a small share of assignments are
   * dark enough to vanish on the dark theme (and a few are pale enough to
   * vanish on the light one). Only those are moved, and only in lightness —
   * hue and saturation are untouched, so a team still looks like its colour.
   */
  function readableColor(hex) {
    const m = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(hex);
    if (!m) return hex;
    const [r, g, b] = [1, 2, 3].map(i => parseInt(m[i], 16) / 255);

    const max = Math.max(r, g, b), min = Math.min(r, g, b);
    const l = (max + min) / 2;
    const dark = isDarkTheme();
    // Leaves the great majority of the palette alone.
    const floor = 0.42, ceiling = 0.62;
    if (dark ? l >= floor : l <= ceiling) return hex;

    const d = max - min;
    let h = 0;
    if (d !== 0) {
      if (max === r) h = ((g - b) / d + (g < b ? 6 : 0)) / 6;
      else if (max === g) h = ((b - r) / d + 2) / 6;
      else h = ((r - g) / d + 4) / 6;
    }
    const s = d === 0 ? 0 : d / (1 - Math.abs(2 * l - 1));
    return hslToHex(h, s, dark ? floor : ceiling);
  }

  function hslToHex(h, s, l) {
    const c = (1 - Math.abs(2 * l - 1)) * s;
    const x = c * (1 - Math.abs(((h * 6) % 2) - 1));
    const mm = l - c / 2;
    const seg = Math.floor(h * 6) % 6;
    const rgb = [[c, x, 0], [x, c, 0], [0, c, x], [0, x, c], [x, 0, c], [c, 0, x]][seg];
    return '#' + rgb.map(v => Math.round((v + mm) * 255).toString(16).padStart(2, '0')).join('');
  }

  function isDarkTheme() {
    const set = document.documentElement.getAttribute('data-theme');
    if (set) return set === 'dark';
    return window.matchMedia('(prefers-color-scheme: dark)').matches;
  }

  function toggleTeam(teamId) {
    if (state.selectedTeams.has(teamId)) state.selectedTeams.delete(teamId);
    else state.selectedTeams.add(teamId);
    refreshColorAssignment();
  }

  /** Selects or clears a whole side at once. */
  function setTeamsSelected(teamIds, selected) {
    for (const id of teamIds) {
      if (selected) state.selectedTeams.add(id);
      else state.selectedTeams.delete(id);
    }
    refreshColorAssignment();
  }

  function metric(key) {
    return state.metrics.find(m => m.key === key) || state.metrics[0];
  }

  function toRate(xs, ys) {
    const out = new Array(ys.length).fill(0);
    for (let i = 1; i < ys.length; i++) {
      const dt = xs[i] - xs[i - 1];
      out[i] = dt > 0 ? ((ys[i] - ys[i - 1]) / dt) * 60 : 0;
    }
    if (out.length > 1) out[0] = out[1];
    return out;
  }

  function buildSeries(metricKey) {
    const d = state.detail;
    if (!d) return [];
    const apply = (xs, ys) => (state.valueMode === 'rate' ? toRate(xs, ys) : ys);

    if (state.viewMode === 'ally') {
      const byID = teamIndex();
      return d.allyTeams.map((at, i) => {
        const teams = at.teamIDs.map(id => byID.get(id)).filter(Boolean);
        const base = teams.reduce((a, b) => (b.times.length > (a ? a.times.length : 0) ? b : a), null);
        if (!base) return null;
        const xs = base.times;
        const ys = new Array(xs.length).fill(0);
        for (const t of teams) {
          const v = t.series[metricKey] || [];
          for (let k = 0; k < xs.length && k < v.length; k++) ys[k] += v[k];
        }
        return {
          id: `ally-${at.id}`,
          label: allyLabel(at),
          color: seriesColor(i), xs, ys: apply(xs, ys),
        };
      }).filter(Boolean);
    }

    return d.teams
      .filter(t => state.selectedTeams.has(t.id))
      .map(t => ({
        id: `team-${t.id}`,
        label: t.name,
        color: colorForTeam(t),
        xs: t.times,
        ys: apply(t.times, t.series[metricKey] || []),
      }));
  }

  /**
   * The series id for the configured player, if they are in this match and
   * have a line of their own.
   *
   * The team view aggregates whole sides, so there is nothing individual to
   * pin there — which is also why the toggle is not offered in that view.
   */
  function ownSeriesId() {
    const d = state.detail;
    if (!d || state.viewMode !== 'team') return null;
    const me = d.teams.find(t => t.isYou);
    if (!me) return null;
    return state.selectedTeams.has(me.id) ? `team-${me.id}` : null;
  }

  function renderDetail() {
    const d = state.detail;
    const pane = $('detail-pane');
    if (state.activeChart) { state.activeChart.destroy(); state.activeChart = null; }
    pane.innerHTML = '';

    pane.appendChild(detailHeader(d));

    if (!d.hasStats) {
      const notice = document.createElement('div');
      notice.className = 'notice';
      notice.textContent =
        'This replay has no statistics. The game writes them when a match finishes recording, ' +
        'so a match that was quit or crashed out of keeps its header but no data.';
      pane.appendChild(notice);
      pane.appendChild(rosterCard(d));
      return;
    }

    pane.appendChild(tiles(d));
    pane.appendChild(filterRow());
    pane.appendChild(mainChartCard());
    pane.appendChild(multiplesCard());
    pane.appendChild(rosterCard(d));

    if (state.pinYou) applyEmphasis(ownSeriesId());
  }

  function detailHeader(d) {
    const head = document.createElement('div');
    head.className = 'detail-header';
    const row = document.createElement('div');
    row.className = 'detail-header-row';

    const thumb = mapThumb(d.map, 'map-thumb-detail');
    if (thumb) row.appendChild(thumb);

    const text = document.createElement('div');
    const title = document.createElement('h2');
    title.className = 'detail-title';
    title.textContent = d.map || d.fileName;
    const sub = document.createElement('div');
    sub.className = 'detail-sub';
    const played = new Date(d.played).toLocaleString(undefined, {
      year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
    });
    for (const bit of [played, d.format, `Engine ${d.engine}`, d.fileName]) {
      if (!bit) continue;
      const span = document.createElement('span');
      span.textContent = bit;
      sub.appendChild(span);
    }
    text.append(title, sub);
    row.appendChild(text);
    head.appendChild(row);
    return head;
  }

  function tile(label, value, note) {
    const box = document.createElement('div');
    box.className = 'tile';
    const l = document.createElement('div');
    l.className = 'tile-label';
    l.textContent = label;
    const v = document.createElement('div');
    v.className = 'tile-value';
    v.textContent = value;
    box.append(l, v);
    if (note) {
      const n = document.createElement('div');
      n.className = 'tile-note';
      n.textContent = note;
      box.appendChild(n);
    }
    return box;
  }

  function tiles(d) {
    const wrap = document.createElement('div');
    wrap.className = 'tiles';
    const sum = key => d.teams.reduce((acc, t) => acc + (t.totals[key] || 0), 0);
    const humans = d.teams.filter(t => !t.isAI).length;
    const ais = d.teams.filter(t => t.isAI).length;
    const winners = d.allyTeams.filter(a => a.won).map(a => allyLabel(a));
    const me = d.teams.find(t => t.isYou);

    wrap.append(tile('Duration', Chart.formatTime(d.durationSeconds)));
    if (me) {
      wrap.append(tile('Your result', me.won ? 'Won' : 'Lost',
        `${me.name}${me.side ? ' · ' + me.side : ''}`));
    } else {
      wrap.append(tile('Winner', winners.length ? winners.join(', ') : 'Undecided',
        winners.length ? `${d.allyTeams.length} sides` : 'No result recorded'));
    }
    wrap.append(tile('Players', String(humans), ais ? `+ ${ais} AI` : 'no AI'));
    // Which stats get a headline tile is declared in the metric registry, not
    // here — see demo.Metric.Headline.
    for (const m of state.metrics.filter(m => m.headline)) {
      wrap.append(tile(m.label, Chart.formatNumber(sum(m.key)), 'all teams'));
    }
    return wrap;
  }

  function filterRow() {
    const row = document.createElement('div');
    row.className = 'filter-row';

    row.appendChild(segmented('View', [
      { value: 'team', label: 'Players' },
      { value: 'ally', label: 'Teams' },
    ], state.viewMode, v => { state.viewMode = v; renderDetail(); }));

    // Both toggles are per-player concepts; the team view aggregates sides.
    if (state.viewMode === 'team') {
      const group = document.createElement('div');
      group.className = 'filter-group';

      if (state.detail.teams.some(t => t.isYou)) {
        group.appendChild(checkToggle('Highlight me', state.pinYou,
          'Keep your own line emphasised. Set the default in Settings.',
          on => { state.pinYou = on; renderDetail(); }));
      }
      group.appendChild(checkToggle('In-game colours', state.inGameColors,
        'Colour each line by the player\'s in-game team colour instead of the accessible palette.',
        on => {
          state.inGameColors = on;
          localStorage.setItem('inGameColors', on ? '1' : '0');
          renderDetail();
        }));
      row.appendChild(group);
    }

    row.appendChild(segmented('Values', [
      { value: 'total', label: 'Cumulative' },
      { value: 'rate', label: 'Per minute' },
    ], state.valueMode, v => { state.valueMode = v; renderDetail(); }));

    const group = document.createElement('div');
    group.className = 'filter-group';
    const label = document.createElement('span');
    label.className = 'filter-label';
    label.textContent = 'Metric';
    const select = document.createElement('select');
    for (const g of state.groups) {
      const optGroup = document.createElement('optgroup');
      optGroup.label = g.charAt(0).toUpperCase() + g.slice(1);
      for (const m of state.metrics.filter(m => m.group === g)) {
        const opt = document.createElement('option');
        opt.value = m.key;
        opt.textContent = m.label;
        if (m.key === state.mainMetric) opt.selected = true;
        optGroup.appendChild(opt);
      }
      select.appendChild(optGroup);
    }
    select.addEventListener('change', e => { state.mainMetric = e.target.value; renderDetail(); });
    group.append(label, select);
    row.appendChild(group);

    return row;
  }

  /** A labelled checkbox that reads as on/off, for settings that persist. */
  function checkToggle(labelText, checked, title, onChange) {
    const toggle = document.createElement('label');
    toggle.className = 'toggle';
    toggle.title = title;
    const box = document.createElement('input');
    box.type = 'checkbox';
    box.checked = checked;
    const label = document.createElement('span');
    label.textContent = labelText;
    box.addEventListener('change', () => onChange(box.checked));
    toggle.append(box, label);
    return toggle;
  }

  function segmented(labelText, options, current, onChange) {
    const group = document.createElement('div');
    group.className = 'filter-group';
    const label = document.createElement('span');
    label.className = 'filter-label';
    label.textContent = labelText;
    const seg = document.createElement('div');
    seg.className = 'segmented';
    for (const opt of options) {
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.textContent = opt.label;
      btn.setAttribute('aria-pressed', String(opt.value === current));
      btn.addEventListener('click', () => { if (opt.value !== current) onChange(opt.value); });
      seg.appendChild(btn);
    }
    group.append(label, seg);
    return group;
  }

  /* ---------------- Emphasis ---------------- */

  /**
   * Emphasises one series across the chart, its legend and the roster, so
   * hovering any of the three lights up the same player.
   */
  function applyEmphasis(seriesId) {
    const target = seriesId || (state.pinYou ? ownSeriesId() : null);
    if (state.activeChart) state.activeChart.highlight(target);
    for (const node of document.querySelectorAll('[data-series-id]')) {
      node.classList.toggle('legend-item-dim',
        target !== null && node.dataset.seriesId !== target && node.classList.contains('legend-item'));
      node.classList.toggle('row-emphasis', target !== null && node.dataset.seriesId === target);
    }
  }

  function mainChartCard() {
    const m = metric(state.mainMetric);
    const series = buildSeries(state.mainMetric);
    const unitSuffix = state.valueMode === 'rate' ? ' per minute' : ' (total)';

    const card = document.createElement('div');
    card.className = 'card';

    const head = document.createElement('div');
    head.className = 'card-head';
    const title = document.createElement('h3');
    title.className = 'card-title';
    title.textContent = m.label + unitSuffix;
    const toggle = document.createElement('button');
    toggle.type = 'button';
    toggle.className = 'btn btn-quiet';
    toggle.textContent = state.showTable ? 'Show chart' : 'Show table';
    toggle.addEventListener('click', () => { state.showTable = !state.showTable; renderDetail(); });
    head.append(title, toggle);
    card.appendChild(head);

    if (state.showTable) {
      card.appendChild(seriesTable(series));
      return card;
    }

    const host = document.createElement('div');
    host.className = 'chart-host';
    card.appendChild(host);
    card.appendChild(legend(series));

    requestAnimationFrame(() => {
      state.activeChart = Chart.lineChart(host, {
        series, height: 320, yLabel: m.unit || '',
        formatValue: Chart.formatExact,
      });
      if (state.pinYou) applyEmphasis(null);
    });
    return card;
  }

  function seriesTable(series) {
    const wrap = document.createElement('div');
    wrap.className = 'table-wrap';
    const table = document.createElement('table');
    const times = series.length ? series[0].xs : [];

    const thead = document.createElement('thead');
    const hr = document.createElement('tr');
    hr.appendChild(th('Time'));
    for (const s of series) hr.appendChild(th(s.label));
    thead.appendChild(hr);
    table.appendChild(thead);

    const tbody = document.createElement('tbody');
    for (let i = 0; i < times.length; i++) {
      const tr = document.createElement('tr');
      tr.appendChild(td(Chart.formatTime(times[i])));
      for (const s of series) tr.appendChild(td(Chart.formatExact(s.ys[i] ?? 0)));
      tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    wrap.appendChild(table);
    return wrap;
  }

  function legend(series) {
    const box = document.createElement('div');
    box.className = 'legend';
    if (series.length < 2) return box;

    // Past the slot limit the lines share ally-team colours, so a per-player
    // legend would repeat the same two swatches. The ally teams are named
    // instead and the roster below carries the individuals.
    // With in-game colours every player already has a distinct swatch, so the
    // per-player legend below applies even past the slot limit.
    if (state.viewMode === 'team' && colorMode() === 'ally' && !state.inGameColors) {
      for (const at of state.detail.allyTeams) {
        const item = document.createElement('span');
        item.className = 'legend-item';
        const swatch = document.createElement('span');
        swatch.className = 'legend-swatch';
        swatch.style.background = seriesColor(allyIndex(at.id));
        const label = document.createElement('span');
        label.textContent = allyLabel(at);
        item.append(swatch, label);
        box.appendChild(item);
      }
      return box;
    }

    for (const s of series) {
      const item = document.createElement('button');
      item.type = 'button';
      item.className = 'legend-item';
      item.dataset.seriesId = s.id;
      const swatch = document.createElement('span');
      swatch.className = 'legend-swatch';
      swatch.style.background = s.color;
      const label = document.createElement('span');
      label.textContent = s.label;
      item.append(swatch, label);

      item.addEventListener('mouseenter', () => applyEmphasis(s.id));
      item.addEventListener('mouseleave', () => applyEmphasis(null));
      item.addEventListener('focus', () => applyEmphasis(s.id));
      item.addEventListener('blur', () => applyEmphasis(null));
      box.appendChild(item);
    }
    return box;
  }

  /*
   * The sparkline grid depends on the view, the value mode and the selection —
   * but not on which metric is enlarged. Rebuilding it there meant redrawing
   * 15 series across every team (roughly 100k points) to move one
   * `aria-pressed`, on a control users click constantly.
   */
  function multiplesSignature() {
    return [state.viewMode, state.valueMode, state.inGameColors,
      [...state.selectedTeams].sort().join(',')].join('|');
  }

  function multiplesCard() {
    const signature = multiplesSignature();
    if (state.multiples && state.multiples.signature === signature) {
      for (const btn of state.multiples.node.querySelectorAll('.multiple')) {
        btn.setAttribute('aria-pressed', String(btn.dataset.metric === state.mainMetric));
      }
      return state.multiples.node;
    }

    const card = document.createElement('div');
    card.className = 'card';
    const head = document.createElement('div');
    head.className = 'card-head';
    const title = document.createElement('h3');
    title.className = 'card-title';
    title.textContent = 'All metrics';
    const sub = document.createElement('span');
    sub.className = 'card-sub';
    sub.textContent = 'Select one to enlarge';
    head.append(title, sub);
    card.appendChild(head);

    const grid = document.createElement('div');
    grid.className = 'multiples';
    for (const m of state.metrics) {
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'multiple';
      btn.dataset.metric = m.key;
      btn.setAttribute('aria-pressed', String(m.key === state.mainMetric));
      const label = document.createElement('span');
      label.className = 'multiple-label';
      label.textContent = m.label;
      const host = document.createElement('div');
      btn.append(label, host);
      btn.addEventListener('click', () => { state.mainMetric = m.key; renderDetail(); });
      grid.appendChild(btn);
      Chart.sparkline(host, buildSeries(m.key));
    }
    card.appendChild(grid);
    state.multiples = { signature, node: card };
    return card;
  }

  function rosterCard(d) {
    const card = document.createElement('div');
    card.className = 'card';
    const head = document.createElement('div');
    head.className = 'card-head';
    const title = document.createElement('h3');
    title.className = 'card-title';
    title.textContent = 'Teams';
    const sub = document.createElement('span');
    sub.className = 'card-sub';
    sub.textContent = state.viewMode === 'team'
      ? 'Hover a row to pick it out; untick to remove it from the charts'
      : 'Switch to Players to plot individually';
    head.append(title, sub);
    card.appendChild(head);

    // Columns come from the metric registry, so adding a statistic to the
    // roster is a registry edit rather than four edits in this file.
    const columns = rosterMetrics();
    const wrap = document.createElement('div');
    wrap.className = 'table-wrap';
    const table = document.createElement('table');
    const thead = document.createElement('thead');
    const hr = document.createElement('tr');
    ['Team', 'Faction', ...columns.map(m => m.short || m.label), 'APM']
      .forEach(h => hr.appendChild(th(h)));
    thead.appendChild(hr);
    table.appendChild(thead);

    const span = columns.length + 3; // Team, Faction, …metrics…, APM
    const tbody = document.createElement('tbody');
    const byID = teamIndex();
    for (const at of d.allyTeams) {
      const groupRow = document.createElement('tr');
      groupRow.className = 'ally-row';
      const cell = document.createElement('td');
      cell.colSpan = span;

      const heading = document.createElement('div');
      heading.className = 'team-name';
      const members = at.teamIDs.filter(id => byID.has(id));

      // Selecting a whole side at once — the common move when comparing one
      // team against another.
      if (state.viewMode === 'team' && members.length) {
        const box = document.createElement('input');
        box.type = 'checkbox';
        const chosen = members.filter(id => state.selectedTeams.has(id)).length;
        box.checked = chosen === members.length;
        // Partial selection reads as neither on nor off.
        box.indeterminate = chosen > 0 && chosen < members.length;
        box.setAttribute('aria-label', `Plot all of ${allyLabel(at, '')}`);
        box.addEventListener('change', () => {
          setTeamsSelected(members, box.checked);
          renderDetail();
        });
        heading.appendChild(box);
      }

      const label = document.createElement('span');
      label.textContent = allyLabel(at, ' — won');
      heading.appendChild(label);
      cell.appendChild(heading);
      groupRow.appendChild(cell);
      tbody.appendChild(groupRow);

      for (const id of at.teamIDs) {
        const t = byID.get(id);
        if (t) tbody.appendChild(teamRow(t));
      }
    }
    table.appendChild(tbody);
    wrap.appendChild(table);
    card.appendChild(wrap);
    return card;
  }

  function teamRow(t) {
    const tr = document.createElement('tr');
    const seriesId = `team-${t.id}`;
    tr.dataset.seriesId = seriesId;

    // Hovering the roster emphasises that player's line — the way to find one
    // player among many when the chart is coloured by ally team.
    if (state.viewMode === 'team' && state.selectedTeams.has(t.id)) {
      tr.addEventListener('mouseenter', () => applyEmphasis(seriesId));
      tr.addEventListener('mouseleave', () => applyEmphasis(null));
    }

    const nameCell = document.createElement('td');
    const name = document.createElement('div');
    name.className = 'team-name';

    if (state.viewMode === 'team') {
      const box = document.createElement('input');
      box.type = 'checkbox';
      box.checked = state.selectedTeams.has(t.id);
      box.setAttribute('aria-label', `Plot ${t.name}`);
      box.addEventListener('change', () => { toggleTeam(t.id); renderDetail(); });
      name.appendChild(box);
    }

    if (t.color) {
      const swatch = document.createElement('span');
      swatch.className = 'team-color';
      swatch.style.background = t.color;
      swatch.title = 'In-game colour';
      name.appendChild(swatch);
    }

    const label = document.createElement('span');
    label.textContent = t.name;
    name.appendChild(label);

    // Lobby rating, when the host recorded it. Older lobbies and local
    // skirmishes leave it out, so both parts are shown only if present.
    if (t.rank >= 0) {
      const badge = document.createElement('span');
      badge.className = 'chevron-badge';
      badge.textContent = t.rank;
      badge.title = `Chevron ${t.rank}`;
      name.appendChild(badge);
    }
    if (t.skill) {
      const badge = document.createElement('span');
      badge.className = 'skill-badge';
      badge.textContent = t.skill;
      badge.title = t.skillUncertainty
        ? `Skill ${t.skill} ± ${t.skillUncertainty} (higher uncertainty means a provisional rating)`
        : `Skill ${t.skill}`;
      name.appendChild(badge);
    }

    if (t.isYou) {
      const badge = document.createElement('span');
      badge.className = 'you-badge';
      badge.textContent = 'YOU';
      name.appendChild(badge);
    }
    if (t.isAI) {
      const badge = document.createElement('span');
      badge.className = 'team-badge';
      badge.textContent = 'AI';
      name.appendChild(badge);
    }
    if (t.won) {
      const badge = document.createElement('span');
      badge.className = 'win-badge';
      badge.textContent = 'WON';
      name.appendChild(badge);
    }
    nameCell.appendChild(name);
    tr.appendChild(nameCell);

    tr.appendChild(td(t.side || '—'));
    for (const m of rosterMetrics()) {
      const v = t.totals[m.key] || 0;
      // A metric with no unit is a bare count and reads better in full.
      tr.appendChild(td(m.unit ? Chart.formatNumber(v) : String(v)));
    }
    tr.appendChild(td(t.isAI ? '—' : (t.apm ? t.apm.toFixed(0) : '—')));
    return tr;
  }

  function rosterMetrics() { return state.metrics.filter(m => m.roster); }

  /** The one place that spells a side's name, so the suffix cannot drift. */
  function allyLabel(at, wonSuffix = ' (won)') {
    return `Team ${at.id + 1}${at.won ? wonSuffix : ''}`;
  }

  /* ---------------- Dashboard ---------------- */

  async function renderDashboard() {
    const host = $('dashboard-inner');
    let stats;
    try {
      stats = await api('/api/stats');
    } catch (e) {
      host.innerHTML = '';
      const err = document.createElement('div');
      err.className = 'notice';
      err.textContent = e.message;
      host.appendChild(err);
      return;
    }

    host.innerHTML = '';
    if (!stats.playerName) {
      const notice = document.createElement('div');
      notice.className = 'notice';
      notice.textContent = 'Set your player name in Settings to see your record across these replays.';
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'btn btn-primary';
      btn.textContent = 'Open settings';
      btn.addEventListener('click', openSettings);
      host.append(notice, btn);
      return;
    }

    const title = document.createElement('h2');
    title.className = 'detail-title';
    title.textContent = `${stats.playerName} — record`;
    const sub = document.createElement('div');
    sub.className = 'detail-sub';
    const subText = document.createElement('span');
    subText.textContent = stats.skipped
      ? `${stats.decided} matches with a result · ${stats.skipped} without one`
      : `${stats.decided} matches with a result`;
    sub.appendChild(subText);
    host.append(title, sub);

    if (!stats.decided) {
      const notice = document.createElement('div');
      notice.className = 'notice';
      notice.textContent = `No decided matches found for "${stats.playerName}". The name must match your in-game name exactly — check the spelling in Settings.`;
      host.appendChild(notice);
      return;
    }

    const tileRow = document.createElement('div');
    tileRow.className = 'tiles';
    tileRow.style.marginTop = '20px';
    tileRow.append(
      tile('Win rate', `${Math.round(stats.overall.winRate * 100)}%`,
        `${stats.overall.won}W · ${stats.overall.lost}L`),
      tile('Matches', String(stats.overall.played), 'with a recorded result'),
      tile('Maps played', String(stats.maps.length)),
    );
    host.appendChild(tileRow);

    if (stats.recent.length) {
      const card = document.createElement('div');
      card.className = 'card';
      const head = document.createElement('div');
      head.className = 'card-head';
      const h = document.createElement('h3');
      h.className = 'card-title';
      h.textContent = 'Recent form';
      const note = document.createElement('span');
      note.className = 'card-sub';
      note.textContent = 'Most recent first';
      head.append(h, note);
      const guide = document.createElement('div');
      guide.className = 'form-guide';
      for (const r of stats.recent) {
        const cell = document.createElement('button');
        cell.type = 'button';
        cell.className = `form-cell ${r.won ? 'form-win' : 'form-loss'}`;
        cell.textContent = r.won ? 'W' : 'L';
        cell.title = `${r.won ? 'Won' : 'Lost'} — ${r.map} — ${new Date(r.played).toLocaleDateString()}`;
        cell.addEventListener('click', () => navigate(pathForReplay(r.id)));
        guide.appendChild(cell);
      }
      card.append(head, guide);
      host.appendChild(card);
    }

    host.appendChild(recordTable('By map', stats.maps, 'Map'));
    if (stats.factions.length) host.appendChild(recordTable('By faction', stats.factions, 'Faction'));
  }

  /** A win/loss table with an inline win-rate bar. */
  function recordTable(titleText, rows, firstHeader) {
    const card = document.createElement('div');
    card.className = 'card';
    const head = document.createElement('div');
    head.className = 'card-head';
    const h = document.createElement('h3');
    h.className = 'card-title';
    h.textContent = titleText;
    const note = document.createElement('span');
    note.className = 'card-sub';
    note.textContent = 'Most played first';
    head.append(h, note);
    card.appendChild(head);

    const wrap = document.createElement('div');
    wrap.className = 'table-wrap';
    const table = document.createElement('table');
    const thead = document.createElement('thead');
    const hr = document.createElement('tr');
    [firstHeader, 'Played', 'Won', 'Lost', 'Win rate'].forEach(t => hr.appendChild(th(t)));
    thead.appendChild(hr);
    table.appendChild(thead);

    const tbody = document.createElement('tbody');
    for (const row of rows) {
      const tr = document.createElement('tr');
      tr.appendChild(td(row.name));
      tr.appendChild(td(String(row.played)));
      tr.appendChild(td(String(row.won)));
      tr.appendChild(td(String(row.lost)));

      const rateCell = document.createElement('td');
      const bar = document.createElement('div');
      bar.className = 'bar-cell';
      const track = document.createElement('div');
      track.className = 'bar-track';
      const fill = document.createElement('div');
      fill.className = 'bar-fill';
      fill.style.width = `${Math.round(row.winRate * 100)}%`;
      track.appendChild(fill);
      const pct = document.createElement('span');
      pct.textContent = `${Math.round(row.winRate * 100)}%`;
      bar.append(track, pct);
      rateCell.appendChild(bar);
      tr.appendChild(rateCell);
      tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    wrap.appendChild(table);
    card.appendChild(wrap);
    return card;
  }

  function th(text) { const n = document.createElement('th'); n.textContent = text; return n; }
  function td(text) { const n = document.createElement('td'); n.textContent = text; return n; }

  /* ---------------- Theme ---------------- */

  function setupTheme() {
    const saved = localStorage.getItem('theme');
    if (saved) document.documentElement.setAttribute('data-theme', saved);
    updateThemeIcon();
  }

  function toggleTheme() {
    const current = document.documentElement.getAttribute('data-theme');
    const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    const effective = current || (prefersDark ? 'dark' : 'light');
    const next = effective === 'dark' ? 'light' : 'dark';
    document.documentElement.setAttribute('data-theme', next);
    localStorage.setItem('theme', next);
    updateThemeIcon();
    // Palette series are `var(--series-N)` and re-resolve on their own, but
    // in-game colours are baked to concrete hex and their legibility floor
    // depends on which surface they sit on.
    if (state.inGameColors && state.detail && state.page === 'replays') renderDetail();
  }

  function updateThemeIcon() {
    const current = document.documentElement.getAttribute('data-theme');
    const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    const dark = current ? current === 'dark' : prefersDark;
    $('theme-icon').textContent = dark ? '☾' : '☀';
  }

  init().catch(err => {
    document.body.innerHTML = `<p style="padding:24px">Could not start: ${err.message}</p>`;
  });
})();
