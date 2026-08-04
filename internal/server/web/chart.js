/*
 * Minimal SVG chart toolkit — no dependencies, so the app stays a single
 * offline binary.
 *
 * Conventions applied throughout, rather than left to each call site:
 *   - 2px lines, hairline solid gridlines and axes (never dashed)
 *   - a crosshair + tooltip on every time-series chart, with a generous hit
 *     area rather than pinpoint targets
 *   - direct endpoint labels when the series count is small enough to place
 *     them without collision, so values are never tooltip-only
 */
const Chart = (() => {
  const NS = 'http://www.w3.org/2000/svg';

  /** Creates an SVG element with attributes applied. */
  function el(name, attrs = {}) {
    const node = document.createElementNS(NS, name);
    for (const [k, v] of Object.entries(attrs)) node.setAttribute(k, v);
    return node;
  }

  /*
   * Series colours arrive as `var(--series-N)` so the palette swaps with the
   * theme. Presentation attributes do not resolve custom properties, so paint
   * is always applied through inline style, which does.
   */
  function paint(node, { stroke, fill } = {}) {
    if (stroke) node.style.stroke = stroke;
    if (fill) node.style.fill = fill;
    return node;
  }

  /** Abbreviates a magnitude for axis ticks and compact labels. */
  function formatNumber(v) {
    const abs = Math.abs(v);
    if (abs >= 1e9) return (v / 1e9).toFixed(abs >= 1e10 ? 0 : 1) + 'B';
    if (abs >= 1e6) return (v / 1e6).toFixed(abs >= 1e7 ? 0 : 1) + 'M';
    if (abs >= 1e3) return (v / 1e3).toFixed(abs >= 1e4 ? 0 : 1) + 'k';
    if (abs >= 10) return v.toFixed(0);
    if (abs === 0) return '0';
    return v.toFixed(1);
  }

  /** Formats a full-precision value for tables and tooltips. */
  function formatExact(v) {
    return Math.round(v).toLocaleString();
  }

  /** Formats match time in seconds as m:ss, or h:mm:ss past an hour. */
  function formatTime(seconds) {
    const s = Math.max(0, Math.round(seconds));
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    const sec = s % 60;
    if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(sec).padStart(2, '0')}`;
    return `${m}:${String(sec).padStart(2, '0')}`;
  }

  /** Produces rounded tick values covering [min, max]. */
  function niceTicks(min, max, count) {
    if (!isFinite(min) || !isFinite(max) || min === max) return [min || 0];
    const raw = (max - min) / Math.max(1, count);
    const mag = Math.pow(10, Math.floor(Math.log10(raw)));
    const norm = raw / mag;
    const step = (norm >= 5 ? 10 : norm >= 2 ? 5 : norm >= 1 ? 2 : 1) * mag;
    const ticks = [];
    for (let t = Math.ceil(min / step) * step; t <= max + step * 1e-6; t += step) {
      ticks.push(Math.abs(t) < step * 1e-6 ? 0 : t);
    }
    return ticks;
  }

  /** Index of the value in sorted xs nearest to x. */
  function nearestIndex(xs, x) {
    if (!xs.length) return -1;
    let lo = 0, hi = xs.length - 1;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (xs[mid] < x) lo = mid + 1; else hi = mid;
    }
    if (lo > 0 && Math.abs(xs[lo - 1] - x) <= Math.abs(xs[lo] - x)) return lo - 1;
    return lo;
  }

  // A single tooltip node is reused across charts.
  let tooltipNode = null;
  function tooltip() {
    if (!tooltipNode) {
      tooltipNode = document.createElement('div');
      tooltipNode.className = 'tooltip';
      tooltipNode.hidden = true;
      document.body.appendChild(tooltipNode);
    }
    return tooltipNode;
  }
  function hideTooltip() { tooltip().hidden = true; }

  /**
   * Renders a multi-series time-series line chart into host.
   *
   * series: [{ id, label, color, xs: number[], ys: number[] }]
   * Returns a controller with destroy().
   */
  function lineChart(host, { series, height = 300, yLabel = '', formatValue = formatNumber }) {
    host.innerHTML = '';
    const visible = series.filter(s => s.xs.length > 0);
    if (!visible.length) {
      const empty = document.createElement('div');
      empty.className = 'chart-empty';
      empty.textContent = 'No data for this metric.';
      host.appendChild(empty);
      return { destroy() {} };
    }

    // Which series is emphasised, and the per-series nodes to restyle when
    // that changes. Held outside draw() so a resize redraw keeps the state.
    let highlighted = null;
    let painted = [];
    // Lines live in their own layer so the emphasised one can be raised
    // without disturbing the hover layer stacked above them.
    let lineLayer = null;
    let halo = null;

    let frame = null;
    // ResizeObserver fires once on observe, so without this the initial
    // render() below would redraw the chart a second time at the same width.
    let lastWidth = 0;
    const render = () => {
      const width = Math.max(320, host.clientWidth);
      if (width === lastWidth) return;
      lastWidth = width;
      draw(width);
      applyHighlight();
    };

    /*
     * Emphasis works by pushing the other series back rather than by making
     * the chosen one louder: everything else drops to a wash and thins out, so
     * the highlighted line reads clearly without the chart changing weight.
     *
     * Dimming alone is not enough when series carry arbitrary colours — a dark
     * in-game team colour on the dark theme is barely visible however faint its
     * neighbours are. The emphasised line therefore also gets a halo in the
     * surface colour and is raised above the rest, which reads regardless of
     * its own hue.
     */
    function applyHighlight() {
      // The outline does the emphasising, so the others only need to step
      // back, not disappear. Fading them harder than this washes every series
      // toward the surface colour, which matters when the colours themselves
      // are the point — as with in-game team colours.
      for (const p of painted) {
        const dimmed = highlighted !== null && p.id !== highlighted;
        const chosen = highlighted !== null && p.id === highlighted;
        if (p.line) {
          p.line.style.opacity = dimmed ? '0.45' : '1';
          p.line.style.strokeWidth = chosen ? '3' : '2';
        }
        if (p.label) p.label.style.opacity = dimmed ? '0.45' : '1';
      }

      if (!halo || !lineLayer) return;
      const chosen = highlighted === null
        ? null
        : painted.find(p => p.id === highlighted && p.line);
      if (!chosen) {
        halo.style.display = 'none';
        return;
      }
      halo.setAttribute('d', chosen.d);
      halo.style.display = '';
      lineLayer.appendChild(halo);
      lineLayer.appendChild(chosen.line);
    }

    const observer = new ResizeObserver(() => {
      if (frame) cancelAnimationFrame(frame);
      frame = requestAnimationFrame(render);
    });
    observer.observe(host);

    function draw(width) {
      host.innerHTML = '';

      // Endpoint labels need room on the right; the y-axis needs room left.
      const showEndpointLabels = visible.length <= 4;
      const margin = { top: 12, right: showEndpointLabels ? 78 : 16, bottom: 30, left: 56 };
      const plotW = Math.max(40, width - margin.left - margin.right);
      const plotH = Math.max(40, height - margin.top - margin.bottom);

      let xMin = Infinity, xMax = -Infinity, yMax = -Infinity;
      for (const s of visible) {
        xMin = Math.min(xMin, s.xs[0]);
        xMax = Math.max(xMax, s.xs[s.xs.length - 1]);
        for (const y of s.ys) if (y > yMax) yMax = y;
      }
      if (!isFinite(xMin)) xMin = 0;
      if (xMax <= xMin) xMax = xMin + 1;
      // Anchor the value axis at zero: these are production totals and rates,
      // where a truncated baseline exaggerates differences.
      const yMin = 0;
      if (!(yMax > 0)) yMax = 1;

      const x = v => margin.left + ((v - xMin) / (xMax - xMin)) * plotW;
      const y = v => margin.top + plotH - ((v - yMin) / (yMax - yMin)) * plotH;

      const svg = el('svg', {
        viewBox: `0 0 ${width} ${height}`,
        width, height, role: 'img',
        'aria-label': yLabel ? `${yLabel} over time` : 'Time series chart',
      });

      // Horizontal gridlines only — vertical ones add noise without aiding
      // comparison of levels.
      const yTicks = niceTicks(yMin, yMax, 5);
      const grid = el('g', { class: 'chart-grid' });
      for (const t of yTicks) {
        grid.appendChild(el('line', { x1: margin.left, x2: margin.left + plotW, y1: y(t), y2: y(t) }));
        const label = el('text', {
          class: 'chart-tick', x: margin.left - 8, y: y(t) + 4, 'text-anchor': 'end',
        });
        label.textContent = formatNumber(t);
        svg.appendChild(label);
      }
      svg.insertBefore(grid, svg.firstChild);

      const axis = el('g', { class: 'chart-axis' });
      axis.appendChild(el('line', {
        x1: margin.left, x2: margin.left + plotW,
        y1: margin.top + plotH, y2: margin.top + plotH,
      }));
      svg.appendChild(axis);

      for (const t of niceTicks(xMin, xMax, Math.max(2, Math.floor(plotW / 90)))) {
        if (t < xMin || t > xMax) continue;
        const label = el('text', {
          class: 'chart-tick', x: x(t), y: margin.top + plotH + 16, 'text-anchor': 'middle',
        });
        label.textContent = formatTime(t);
        svg.appendChild(label);
      }

      if (yLabel) {
        const label = el('text', { class: 'chart-axis-title', x: margin.left, y: margin.top - 2, 'text-anchor': 'start' });
        label.textContent = yLabel;
        svg.appendChild(label);
      }

      painted = visible.map(s => ({ id: s.id, d: '', line: null, label: null }));

      lineLayer = el('g', { class: 'chart-lines' });
      visible.forEach((s, i) => {
        let d = '';
        for (let k = 0; k < s.xs.length; k++) {
          d += (k === 0 ? 'M' : 'L') + x(s.xs[k]).toFixed(1) + ' ' + y(s.ys[k]).toFixed(1);
        }
        painted[i].d = d;
        const line = paint(el('path', { class: 'chart-line', d }), { stroke: s.color });
        painted[i].line = line;
        lineLayer.appendChild(line);
      });

      halo = el('path', { class: 'chart-halo' });
      halo.style.display = 'none';
      lineLayer.appendChild(halo);
      svg.appendChild(lineLayer);

      // Direct labels at the line ends: identity without a legend round trip.
      if (showEndpointLabels) {
        const placed = [];
        visible.forEach((s, i) => {
          const lastX = s.xs[s.xs.length - 1];
          const lastY = s.ys[s.ys.length - 1];
          let ly = y(lastY) + 4;
          // Nudge apart so labels never overlap each other.
          for (const p of placed) if (Math.abs(p - ly) < 13) ly = p + 13;
          placed.push(ly);
          const label = paint(el('text', {
            class: 'chart-endpoint-label', x: x(lastX) + 8, y: ly,
          }), { fill: s.color });
          label.textContent = s.label.length > 11 ? s.label.slice(0, 10) + '…' : s.label;
          painted[i].label = label;
          svg.appendChild(label);
        });
      }

      const hoverLayer = el('g');
      hoverLayer.style.display = 'none';
      const crosshair = el('line', {
        class: 'chart-crosshair', y1: margin.top, y2: margin.top + plotH,
      });
      hoverLayer.appendChild(crosshair);
      const markers = visible.map(s => {
        const dot = paint(el('circle', { class: 'chart-marker', r: 4.5 }), { fill: s.color });
        hoverLayer.appendChild(dot);
        return dot;
      });
      svg.appendChild(hoverLayer);

      // A full-plot hit rectangle: hovering anywhere in the column selects the
      // nearest sample, instead of demanding a hit on the line itself.
      const hit = el('rect', {
        class: 'chart-hit', x: margin.left, y: margin.top, width: plotW, height: plotH,
      });
      svg.appendChild(hit);

      const onMove = event => {
        const rect = svg.getBoundingClientRect();
        const scale = width / rect.width;
        const px = (event.clientX - rect.left) * scale;
        const dataX = xMin + ((px - margin.left) / plotW) * (xMax - xMin);

        const rows = [];
        let anchorX = null;
        visible.forEach((s, i) => {
          const idx = nearestIndex(s.xs, dataX);
          if (idx < 0) { markers[i].style.display = 'none'; return; }
          const vx = s.xs[idx], vy = s.ys[idx];
          if (anchorX === null) anchorX = vx;
          markers[i].style.display = '';
          markers[i].setAttribute('cx', x(vx));
          markers[i].setAttribute('cy', y(vy));
          rows.push({ label: s.label, color: s.color, value: vy });
        });
        if (anchorX === null) return;

        crosshair.setAttribute('x1', x(anchorX));
        crosshair.setAttribute('x2', x(anchorX));
        hoverLayer.style.display = '';

        rows.sort((a, b) => b.value - a.value);
        const tip = tooltip();
        tip.innerHTML = '';
        const title = document.createElement('div');
        title.className = 'tooltip-title';
        title.textContent = formatTime(anchorX);
        tip.appendChild(title);
        for (const row of rows.slice(0, 12)) {
          const line = document.createElement('div');
          line.className = 'tooltip-row';
          const swatch = document.createElement('span');
          swatch.className = 'tooltip-swatch';
          swatch.style.background = row.color;
          const name = document.createElement('span');
          name.className = 'tooltip-name';
          name.textContent = row.label;
          const value = document.createElement('span');
          value.className = 'tooltip-value';
          value.textContent = formatValue(row.value);
          line.append(swatch, name, value);
          tip.appendChild(line);
        }
        tip.hidden = false;

        // Flip the tooltip to the other side of the cursor near the viewport edge.
        const tipRect = tip.getBoundingClientRect();
        let left = event.clientX + 14;
        if (left + tipRect.width > window.innerWidth - 8) left = event.clientX - tipRect.width - 14;
        let top = event.clientY - tipRect.height / 2;
        top = Math.min(Math.max(8, top), window.innerHeight - tipRect.height - 8);
        tip.style.left = `${left}px`;
        tip.style.top = `${top}px`;
      };

      const onLeave = () => { hoverLayer.style.display = 'none'; hideTooltip(); };
      hit.addEventListener('mousemove', onMove);
      hit.addEventListener('mouseleave', onLeave);

      host.appendChild(svg);
    }

    render();
    return {
      /** Emphasises one series by id, or clears emphasis when given null. */
      highlight(id) {
        if (highlighted === id) return;
        highlighted = id;
        applyHighlight();
      },
      destroy() {
        observer.disconnect();
        if (frame) cancelAnimationFrame(frame);
        hideTooltip();
      },
    };
  }

  /**
   * Renders a compact preview line for the small-multiples grid. Axes and
   * labels are deliberately omitted — these show shape, and the main chart
   * carries the values.
   */
  function sparkline(host, series, { width = 180, height = 44 } = {}) {
    host.innerHTML = '';
    const visible = series.filter(s => s.xs.length > 1);
    if (!visible.length) return;

    let xMin = Infinity, xMax = -Infinity, yMax = -Infinity;
    for (const s of visible) {
      xMin = Math.min(xMin, s.xs[0]);
      xMax = Math.max(xMax, s.xs[s.xs.length - 1]);
      for (const v of s.ys) if (v > yMax) yMax = v;
    }
    if (xMax <= xMin) xMax = xMin + 1;
    if (!(yMax > 0)) yMax = 1;

    const pad = 3;
    const x = v => pad + ((v - xMin) / (xMax - xMin)) * (width - pad * 2);
    const y = v => height - pad - (v / yMax) * (height - pad * 2);

    const svg = el('svg', { viewBox: `0 0 ${width} ${height}`, preserveAspectRatio: 'none' });
    for (const s of visible) {
      let d = '';
      for (let i = 0; i < s.xs.length; i++) {
        d += (i === 0 ? 'M' : 'L') + x(s.xs[i]).toFixed(1) + ' ' + y(s.ys[i]).toFixed(1);
      }
      svg.appendChild(paint(el('path', { class: 'chart-line', d, 'stroke-width': 1.5 }), { stroke: s.color }));
    }
    host.appendChild(svg);
  }

  return { lineChart, sparkline, formatNumber, formatExact, formatTime };
})();
