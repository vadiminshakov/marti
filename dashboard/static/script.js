
const pairContainer = document.getElementById('pairs');
const emptyState = document.getElementById('emptyState');
const chartCanvas = document.getElementById('globalChart');
const balanceStreamStatusEl = document.getElementById('balanceStreamStatus');
const decisionStreamStatusEl = document.getElementById('decisionStreamStatus');
const snapshotAgeStatusEl = document.getElementById('snapshotAgeStatus');
const decisionAgeStatusEl = document.getElementById('decisionAgeStatus');
const activeBotsStatusEl = document.getElementById('activeBotsStatus');
const configPathStatusEl = document.getElementById('configPathStatus');
const pairViews = new Map();
const datasetByPair = new Map();
const modelAggregates = new Map();
const modelPrimaryQuote = new Map();
const colorPalette = [
  { line: '#111111', fill: 'rgba(17,17,17,0.12)' },
  { line: '#d7263d', fill: 'rgba(215,38,61,0.15)' },
  { line: '#1b9aaa', fill: 'rgba(27,154,170,0.15)' },
  { line: '#ff7f11', fill: 'rgba(255,127,17,0.18)' },
  { line: '#3c91e6', fill: 'rgba(60,145,230,0.15)' },
  { line: '#8e3b46', fill: 'rgba(142,59,70,0.15)' },
  { line: '#2e282a', fill: 'rgba(46,40,42,0.18)' }
];
let colorIndex = 0;
let lastStatusPayload = null;

function setStatusPill(el, label, tone) {
  if (!el) {
    return;
  }
  el.textContent = label;
  el.className = `status-pill${tone ? ` status-pill--${tone}` : ''}`;
}

function formatAgeLabel(ts, emptyLabel) {
  if (!ts) {
    return emptyLabel;
  }

  const date = new Date(ts);
  if (Number.isNaN(date.getTime())) {
    return emptyLabel;
  }

  const ageMs = Date.now() - date.getTime();
  if (ageMs < 0) {
    return 'just now';
  }

  const ageSeconds = Math.floor(ageMs / 1000);
  if (ageSeconds < 10) {
    return 'just now';
  }
  if (ageSeconds < 60) {
    return `${ageSeconds}s ago`;
  }

  const ageMinutes = Math.floor(ageSeconds / 60);
  if (ageMinutes < 60) {
    return `${ageMinutes}m ago`;
  }

  const ageHours = Math.floor(ageMinutes / 60);
  return `${ageHours}h ago`;
}

function ageTone(ts) {
  if (!ts) {
    return 'muted';
  }

  const date = new Date(ts);
  if (Number.isNaN(date.getTime())) {
    return 'muted';
  }

  const ageSeconds = (Date.now() - date.getTime()) / 1000;
  if (ageSeconds <= 15) {
    return 'ok';
  }
  if (ageSeconds <= 60) {
    return 'warn';
  }
  return 'danger';
}

function renderStatusSummary() {
  const status = lastStatusPayload || {};
  const configPath = status.configPath || 'none';
  const bots = Number.isFinite(status.activeBots) ? status.activeBots : 0;

  setStatusPill(snapshotAgeStatusEl, `Snapshots: ${formatAgeLabel(status.lastBalanceSnapshotTs, 'waiting')}`, ageTone(status.lastBalanceSnapshotTs));
  setStatusPill(decisionAgeStatusEl, `Decisions: ${formatAgeLabel(status.lastDecisionTs, 'waiting')}`, ageTone(status.lastDecisionTs));
  setStatusPill(activeBotsStatusEl, `Bots: ${bots}`, bots > 0 ? 'ok' : 'muted');
  setStatusPill(configPathStatusEl, `Config: ${configPath}`, status.needsSetup ? 'warn' : 'muted');

  if (emptyState && datasetByPair.size === 0) {
    if (status.needsSetup) {
      emptyState.innerHTML = '<p>Setup is required.</p><p class="muted">Open Configuration to create the first bot config.</p>';
    } else if (bots === 0) {
      emptyState.innerHTML = '<p>No active bots.</p><p class="muted">Save a config with at least one enabled trading pair.</p>';
    }
  }
}

async function refreshStatusSummary() {
  try {
    const res = await fetch('/api/status');
    if (!res.ok) {
      throw new Error('status unavailable');
    }
    lastStatusPayload = await res.json();
    renderStatusSummary();
  } catch (_err) {
    setStatusPill(configPathStatusEl, 'Config: unavailable', 'danger');
  }
}

const normalizeModel = (value) => {
  if (typeof value !== 'string') {
    return '';
  }
  return value.trim();
};

const dcaGroupPrefix = 'DCA:';

const buildAggregateKey = (model, pair) => {
  if (model === 'DCA') {
    return `${dcaGroupPrefix}${pair || '—'}`;
  }
  return model || '—';
};

const formatAggregateLabel = (aggregateKey) => {
  if (typeof aggregateKey === 'string' && aggregateKey.startsWith(dcaGroupPrefix)) {
    return aggregateKey.slice(dcaGroupPrefix.length);
  }
  return shortenModelName(aggregateKey);
};

const extractQuoteCurrency = (pair) => {
  if (!pair || typeof pair !== 'string') { return 'UNKNOWN'; }
  const parts = pair.split('_');
  return parts.length > 1 ? parts[parts.length - 1] : 'UNKNOWN';
};

const extractBaseCurrency = (pair) => {
  if (!pair || typeof pair !== 'string') { return 'UNKNOWN'; }
  const parts = pair.split('_');
  return parts.length > 1 ? parts[0] : 'UNKNOWN';
};

const shortenModelName = (model) => {
  if (!model || typeof model !== 'string') { return '—'; }
  if (model.startsWith('gpt://')) {
    const parts = model.replace('gpt://', '').split('/');
    if (parts.length > 1) {
      return parts[1];
    }
  }
  const parts = model.split('/');
  return parts.length > 1 ? parts[parts.length - 1] : model;
};

Chart.defaults.font.family = "'Space Mono', 'JetBrains Mono', monospace";
Chart.defaults.font.size = 11;
Chart.defaults.color = '#111111';

function hexToRgb(hex) {
  const result = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex);
  return result ? {
    r: parseInt(result[1], 16),
    g: parseInt(result[2], 16),
    b: parseInt(result[3], 16)
  } : { r: 0, g: 0, b: 0 };
}

function distanceToLineSegment(px, py, x1, y1, x2, y2) {
  const dx = x2 - x1;
  const dy = y2 - y1;
  const lengthSquared = dx * dx + dy * dy;

  if (lengthSquared === 0) {
    return Math.sqrt((px - x1) ** 2 + (py - y1) ** 2);
  }

  let t = ((px - x1) * dx + (py - y1) * dy) / lengthSquared;
  t = Math.max(0, Math.min(1, t));

  const nearestX = x1 + t * dx;
  const nearestY = y1 + t * dy;

  return Math.sqrt((px - nearestX) ** 2 + (py - nearestY) ** 2);
}

const modelLogos = {
  'yandex': 'yandex.webp',
  'deepseek': 'deepseek.webp',
  'qwen': 'qwen.webp',
  'moonshot': 'moonshot.webp',
  'openai': 'openai.webp',
  'google': 'google.webp',
  'anthropic': 'anthropic.webp',
  'x-ai': 'xai.webp'
};

const chartLogos = {};
const legendLogos = {};

function resizeImage(img, size) {
  const canvas = document.createElement('canvas');
  canvas.width = size;
  canvas.height = size;
  const ctx = canvas.getContext('2d');
  ctx.drawImage(img, 0, 0, size, size);
  return canvas;
}

function loadLogos() {
  Object.entries(modelLogos).forEach(([key, filename]) => {
    const img = new Image();
    img.src = filename;
    img.onload = () => {
      chartLogos[key] = img;
      legendLogos[key] = resizeImage(img, 18);

      if (datasetByPair) {
        datasetByPair.forEach((dataset) => {
          const modelKey = (dataset.modelKey || '').toLowerCase();
          if (modelKey.includes(key) && !modelKey.includes('tngtech')) {
            dataset.pointStyle = legendLogos[key];
          }
        });
      }
      if (typeof globalChart !== 'undefined' && globalChart) {
        globalChart.update('none');
      }
    };
  });
}

loadLogos();

const buildGlobalChart = (ctx) => {
  let hoveredDatasetIndex = null;
  const isMobile = window.innerWidth <= 768;
  const hoverThreshold = isMobile ? 20 : 12;

  const applyHoverStyles = (chart, newHoveredIndex) => {
    if (newHoveredIndex === hoveredDatasetIndex) {
      return;
    }

    hoveredDatasetIndex = newHoveredIndex;
    chart.hoveredDatasetIndex = hoveredDatasetIndex;

    chart.data.datasets.forEach((dataset, i) => {
      if (hoveredDatasetIndex === null) {
        dataset.borderWidth = 2;
        dataset.borderColor = dataset._originalColor || dataset.borderColor;
        dataset.backgroundColor = dataset._originalBgColor || dataset.backgroundColor;
      } else if (i === hoveredDatasetIndex) {
        if (!dataset._originalColor) {
          dataset._originalColor = dataset.borderColor;
          dataset._originalBgColor = dataset.backgroundColor;
        }
        dataset.borderWidth = 4;
        dataset.borderColor = dataset._originalColor;
        dataset.backgroundColor = dataset._originalBgColor;
      } else {
        if (!dataset._originalColor) {
          dataset._originalColor = dataset.borderColor;
          dataset._originalBgColor = dataset.backgroundColor;
        }
        dataset.borderWidth = 1.5;
        const rgb = hexToRgb(dataset._originalColor);
        dataset.borderColor = `rgba(${rgb.r}, ${rgb.g}, ${rgb.b}, 0.25)`;
        dataset.backgroundColor = 'rgba(0, 0, 0, 0)';
      }
    });

    chart.update('none');
  };

  const crosshairPlugin = {
    id: 'crosshair',
    afterDraw: (chart) => {
      if (chart.tooltip?._active?.length) {
        const ctx = chart.ctx;
        const x = chart.tooltip._active[0].element.x;
        const topY = chart.scales.y.top;
        const bottomY = chart.scales.y.bottom;

        ctx.save();
        ctx.beginPath();
        ctx.moveTo(x, topY);
        ctx.lineTo(x, bottomY);
        ctx.lineWidth = 1;
        ctx.strokeStyle = 'rgba(0, 0, 0, 0.2)';
        ctx.setLineDash([]);
        ctx.stroke();
        ctx.restore();
      }
    }
  };

  const lineGlowPlugin = {
    id: 'lineGlow',
    beforeDatasetDraw: (chart, args) => {
      const { ctx } = chart;
      const { index } = args;
      if (chart.hoveredDatasetIndex === index) {
        const dataset = chart.data.datasets[index];
        ctx.save();
        ctx.shadowColor = dataset.borderColor;
        ctx.shadowBlur = 10;
        ctx.shadowOffsetX = 0;
        ctx.shadowOffsetY = 0;
      }
    },
    afterDatasetDraw: (chart, args) => {
      const { ctx } = chart;
      const { index } = args;
      if (chart.hoveredDatasetIndex === index) {
        ctx.restore();
      }
    }
  };

  const findHoveredDataset = (chart, pos) => {
    let closestIndex = null;
    let closestDistance = Infinity;
    const datasets = chart.data.datasets;

    datasets.forEach((_, i) => {
      const meta = chart.getDatasetMeta(i);
      if (meta.hidden) { return; }
      const points = meta.data;
      for (let j = 0; j < points.length - 1; j++) {
        const p1 = points[j];
        const p2 = points[j + 1];
        if (!p1 || !p2) { continue; }
        const distance = distanceToLineSegment(
          pos.x, pos.y,
          p1.x, p1.y,
          p2.x, p2.y
        );
        if (distance < closestDistance) {
          closestDistance = distance;
          closestIndex = i;
        }
      }
    });

    if (closestDistance <= hoverThreshold) {
      return closestIndex;
    }
    return null;
  };

  const chart = new Chart(ctx, {
    type: 'line',
    data: { labels: [], datasets: [] },
    options: {
      animation: false,
      responsive: true,
      maintainAspectRatio: false,
      interaction: { intersect: false, mode: 'index' },
      layout: {
        padding: {
          right: 50
        }
      },
      onHover: (event, activeElements) => {
        const chart = event.chart;
        const canvasPosition = Chart.helpers.getRelativePosition(event, chart);

        const newHoveredIndex = findHoveredDataset(chart, canvasPosition);

        if (newHoveredIndex !== hoveredDatasetIndex) {
          applyHoverStyles(chart, newHoveredIndex);
        }
      },
      scales: {
        x: {
          type: 'time',
          time: {
            displayFormats: {
              millisecond: 'HH:mm:ss.SSS',
              second: 'HH:mm:ss',
              minute: 'HH:mm',
              hour: 'dd.MM HH:mm',
              day: 'dd.MM.yyyy'
            }
          },
          ticks: {
            color: '#888888',
            maxRotation: 0,
            autoSkip: true,
            font: { size: isMobile ? 8 : 10 }
          },
          grid: { display: false, drawBorder: false }
        },
        y: {
          ticks: {
            color: '#888888',
            font: { size: isMobile ? 8 : 10 },
            callback: function (value) {
              return value.toString().replace(/\B(?=(\d{3})+(?!\d))/g, " ");
            }
          },
          grid: { color: 'rgba(0,0,0,0.03)', borderDash: [], drawBorder: false }
        }
      },
      elements: { line: { borderCapStyle: 'round' } },
      plugins: {

        legend: { display: true, labels: { usePointStyle: true, boxWidth: isMobile ? 16 : 20, font: { size: isMobile ? 8 : 10 } } },
        tooltip: {
          enabled: false,
          external: () => { },
          backgroundColor: 'rgba(255, 255, 255, 0.95)',
          borderColor: 'rgba(0, 0, 0, 0.1)',
          borderWidth: 1,
          titleColor: '#111111',
          bodyColor: '#444444',
          displayColors: true,
          padding: 10,
          cornerRadius: 4,
          titleFont: { size: 11, weight: 'bold' },
          bodyFont: { size: 11 },
          callbacks: {
            label: function (context) {
              let label = context.dataset.label || '';
              if (label) {
                label += ': ';
              }
              if (context.parsed.y !== null) {
                label += context.parsed.y.toString().replace(/\B(?=(\d{3})+(?!\d))/g, " ");
              }
              return label;
            }
          }
        }
      }
    },
    plugins: [lineGlowPlugin, {
      id: 'logoPlugin',
      afterDraw: (chart) => {
        const ctx = chart.ctx;

        const hoveredIndex = chart.hoveredDatasetIndex;

        chart.data.datasets.forEach((dataset, i) => {
          const meta = chart.getDatasetMeta(i);
          if (meta.hidden) return;

          let lastIndex = -1;
          for (let j = dataset.data.length - 1; j >= 0; j--) {
            if (dataset.data[j] !== null && dataset.data[j] !== undefined) {
              lastIndex = j;
              break;
            }
          }

          if (lastIndex === -1) return;

          const point = meta.data[lastIndex];
          if (!point) return;

          const { x, y } = point;

          if (!isFinite(x) || !isFinite(y)) return;

          let logoImg = null;
          const modelKey = dataset.modelKey ? dataset.modelKey.toLowerCase() : '';

          for (const [key, img] of Object.entries(chartLogos)) {
            if (modelKey.includes(key)) {
              if (modelKey.includes('tngtech')) {
                continue;
              }
              logoImg = img;
              break;
            }
          }

          const size = isMobile ? 24 : 32;

          ctx.save();

          const logoX = x - size / 2;
          const logoY = y - size / 2;

          let opacity = 1.0;
          let isHovered = false;
          if (hoveredIndex !== null && hoveredIndex !== undefined) {
            opacity = i === hoveredIndex ? 1.0 : 0.3;
            isHovered = i === hoveredIndex;
          }

          if (isHovered) {
            ctx.shadowColor = dataset.borderColor;
            ctx.shadowBlur = 20;
            ctx.shadowOffsetX = 0;
            ctx.shadowOffsetY = 0;
          }

          if (logoImg && logoImg.complete && logoImg.naturalHeight !== 0) {
            ctx.globalAlpha = opacity;
            ctx.imageSmoothingEnabled = true;
            ctx.drawImage(logoImg, logoX, logoY, size, size);
          } else {
            ctx.globalAlpha = opacity;
            ctx.beginPath();
            ctx.arc(x, y, 4, 0, 2 * Math.PI);
            ctx.fillStyle = dataset.borderColor;
            ctx.fill();
          }
          ctx.restore();
        });
      }
    }, crosshairPlugin]
  });

  chart.applyHoverStyles = (newHoveredIndex) => applyHoverStyles(chart, newHoveredIndex);
  return chart;
};

const chartCtx = chartCanvas.getContext('2d');
chartCtx.imageSmoothingEnabled = false;
const globalChart = buildGlobalChart(chartCtx);

let chartUpdateScheduled = false;

function scheduleChartUpdate() {
  if (chartUpdateScheduled) return;
  chartUpdateScheduled = true;
  requestAnimationFrame(() => {
    globalChart.update('none');
    chartUpdateScheduled = false;
  });
}

chartCanvas.addEventListener('mouseleave', () => {
  if (globalChart && typeof globalChart.applyHoverStyles === 'function') {
    globalChart.applyHoverStyles(null);
  }
});

const parseNum = (value) => {
  const num = parseFloat(value);
  return Number.isFinite(num) ? num : 0;
};

const formatTs = (ts) => {
  if (!ts) { return 'Waiting…'; }
  const date = new Date(ts);
  if (Number.isNaN(date.getTime())) { return 'Waiting…'; }
  return date.toLocaleTimeString([], { hour12: false });
};

const nextColor = () => {
  const color = colorPalette[colorIndex % colorPalette.length];
  colorIndex += 1;
  return color;
};

function createGradient(ctx, color) {
  const gradient = ctx.createLinearGradient(0, 0, 0, 640);
  gradient.addColorStop(0, color.replace('0.15)', '0.1)').replace('0.12)', '0.1)').replace('0.18)', '0.1)'));
  gradient.addColorStop(1, 'rgba(255, 255, 255, 0)');
  return gradient;
}

function ensureDataset(model) {
  const key = model || '—';
  if (datasetByPair.has(key)) {
    return datasetByPair.get(key);
  }
  const palette = nextColor();

  const dataset = {
    modelKey: key,
    label: formatAggregateLabel(key),
    data: new Array(globalChart.data.labels.length).fill(null),
    borderColor: palette.line,
    backgroundColor: createGradient(globalChart.ctx, palette.fill),
    borderWidth: 2,
    pointRadius: 0,
    pointHoverRadius: 4,
    pointBackgroundColor: '#ffffff',
    pointBorderColor: palette.line,
    pointBorderWidth: 2,
    tension: 0.3,
    fill: true,
    spanGaps: true
  };

  const safeKey = key.toLowerCase();
  for (const [logoKey, img] of Object.entries(legendLogos)) {
    if (safeKey.includes(logoKey) && !safeKey.includes('tngtech')) {
      dataset.pointStyle = img;
      break;
    }
  }

  globalChart.data.datasets.push(dataset);
  datasetByPair.set(key, dataset);
  return dataset;
}

function appendGlobalLabel(label) {
  globalChart.data.labels.push(label);
  globalChart.data.datasets.forEach((dataset) => {
    const lastIndex = dataset.data.length - 1;
    const lastValue = lastIndex >= 0 ? dataset.data[lastIndex] : null;
    dataset.data.push(lastValue);
  });
  if (globalChart.data.labels.length > 50000) {
    globalChart.data.labels.shift();
    globalChart.data.datasets.forEach((dataset) => {
      dataset.data.shift();
    });
  }
}

function updateGlobalChart(model, _quoteCurrency, totalBalance, ts) {
  const tsDate = ts ? new Date(ts) : new Date();
  const labelDate = Number.isNaN(tsDate.getTime()) ? new Date() : tsDate;
  appendGlobalLabel(labelDate);
  const dataset = ensureDataset(model);
  dataset.data[dataset.data.length - 1] = totalBalance;
  scheduleChartUpdate();
}

let emptyStateRemoveTimeout = null;

function ensureModelView(model) {
  const safeModel = model || '—';
  if (pairViews.has(safeModel)) {
    return pairViews.get(safeModel);
  }

  if (emptyState && !emptyStateRemoveTimeout) {
    emptyStateRemoveTimeout = setTimeout(() => {
      if (emptyState) {
        emptyState.remove();
      }
    }, 500);
  }

  const card = document.createElement('article');
  card.className = 'pair-card';

  const header = document.createElement('div');
  header.className = 'pair-card-header';
  const labelsWrap = document.createElement('div');
  labelsWrap.className = 'pair-card-labels';
  const title = document.createElement('h2');
  title.className = 'pair-name';
  title.textContent = formatAggregateLabel(safeModel);
  const updated = document.createElement('span');
  updated.className = 'pill muted';
  updated.textContent = 'Waiting…';
  labelsWrap.append(title);
  header.append(labelsWrap, updated);

  const equity = document.createElement('div');
  equity.className = 'equity';
  const label = document.createElement('div');
  label.className = 'label';
  label.textContent = 'Total funds';
  const totalValue = document.createElement('div');
  totalValue.className = 'value';
  totalValue.textContent = '0';
  equity.append(label, totalValue);

  // stats row
  const statsRow = document.createElement('div');
  statsRow.className = 'stats-row';

  const createStat = (label, value) => {
    const el = document.createElement('div');
    el.className = 'stat-item';
    const l = document.createElement('div');
    l.className = 'stat-label';
    l.textContent = label;
    const v = document.createElement('div');
    v.className = 'stat-value';
    v.textContent = value;
    el.append(l, v);
    return { container: el, lbl: l, val: v };
  };

  const pnl = createStat('PnL', '—');
  const base = createStat('Base', '0');
  const quote = createStat('Quote', '0');

  statsRow.append(pnl.container, base.container, quote.container);

  const pairsList = document.createElement('div');
  pairsList.className = 'pairs-list';
  pairsList.style.marginTop = '1rem';
  pairsList.style.display = 'flex';
  pairsList.style.flexDirection = 'column';
  pairsList.style.gap = '0.5rem';

  card.append(header, equity, statsRow, pairsList);
  pairContainer.appendChild(card);

  const view = {
    totalEl: totalValue,
    updatedEl: updated,
    pnlEl: pnl.val,
    baseEl: base.val,
    baseLabelEl: base.lbl,
    quoteEl: quote.val,
    quoteLabelEl: quote.lbl,
    pairsListEl: pairsList
  };
  pairViews.set(safeModel, view);
  return view;
}

function deriveTotal(payload) {
  if (payload.total_quote) {
    const numeric = parseNum(payload.total_quote);
    return { numeric, display: payload.total_quote };
  }
  const price = parseNum(payload.price);
  const base = parseNum(payload.base);
  const quote = parseNum(payload.quote);
  const numeric = (price ? base * price : 0) + quote;
  return { numeric, display: numeric.toFixed(4) };
}

function renderModelNumbers(view, aggregate) {
  const quoteCurrency = aggregate.quoteCurrency || '';
  view.totalEl.textContent = aggregate.totalBalance.toFixed(2) + (quoteCurrency ? ' ' + quoteCurrency : '');

  // render stats
  view.baseLabelEl.textContent = aggregate.baseCurrency || 'Base';
  view.baseEl.textContent = aggregate.totalBase.toFixed(4);
  view.quoteLabelEl.textContent = aggregate.quoteCurrency || 'Quote';
  view.quoteEl.textContent = aggregate.totalQuote.toFixed(2);

  if (aggregate.initialBalance) {
    const pnl = aggregate.totalBalance - aggregate.initialBalance;
    const pnlPercent = (pnl / aggregate.initialBalance) * 100;
    const sign = pnl >= 0 ? '+' : '';
    view.pnlEl.textContent = `${sign}${pnl.toFixed(2)} (${sign}${pnlPercent.toFixed(2)}%)`;
    view.pnlEl.className = 'stat-value ' + (pnl >= 0 ? 'pnl-positive' : 'pnl-negative');
  }

  let latestTimestamp = null;
  for (const [, pairData] of aggregate.pairs) {
    if (!latestTimestamp || (pairData.timestamp && new Date(pairData.timestamp) > new Date(latestTimestamp))) {
      latestTimestamp = pairData.timestamp;
    }
  }
  view.updatedEl.textContent = formatTs(latestTimestamp);

  // Update pairs list with Sell All buttons
  if (view.pairsListEl) {
    const existingPairs = new Set(Array.from(view.pairsListEl.children).map(c => c.dataset.pair));
    for (const [pairLabel] of aggregate.pairs) {
      if (!existingPairs.has(pairLabel)) {
        const btn = document.createElement('button');
        btn.className = 'sell-all-btn';
        btn.dataset.pair = pairLabel;
        btn.textContent = `Sell All ${pairLabel}`;
        btn.onclick = () => {
          if (confirm(`Are you sure you want to Sell All for ${pairLabel}?`)) {
            btn.disabled = true;
            fetch(`/sellall/${pairLabel}`, { method: 'POST' })
              .then(async r => {
                if (!r.ok) {
                  const t = await r.text();
                  throw new Error(t);
                }
                // success
              })
              .catch(err => {
                alert(`Error: ${err.message}`);
                btn.disabled = false;
              });
          }
        };
        view.pairsListEl.appendChild(btn);
      }
    }
  }
}

function handlePayload(payload) {
  const pairLabel = payload.pair || '—';
  const model = normalizeModel(payload.model);
  const aggregateKey = buildAggregateKey(model, pairLabel);
  const quoteCurrency = extractQuoteCurrency(pairLabel);

  if (!modelPrimaryQuote.has(aggregateKey)) {
    modelPrimaryQuote.set(aggregateKey, quoteCurrency);
  }

  const primaryQuote = modelPrimaryQuote.get(aggregateKey);
  if (quoteCurrency !== primaryQuote) {
    return;
  }

  const baseCurrency = extractBaseCurrency(pairLabel);
  let aggregate = modelAggregates.get(aggregateKey);
  if (!aggregate) {
    aggregate = {
      model: aggregateKey,
      quoteCurrency: quoteCurrency,
      baseCurrency: baseCurrency,
      pairs: new Map(),
      totalBalance: 0
    };
    modelAggregates.set(aggregateKey, aggregate);
  } else if (aggregate.baseCurrency !== 'Base' && aggregate.baseCurrency !== baseCurrency) {
    aggregate.baseCurrency = 'Base';
  }

  const total = deriveTotal(payload);
  aggregate.pairs.set(pairLabel, {
    totalQuote: total.numeric,
    timestamp: payload.ts,
    base: parseNum(payload.base),
    quote: parseNum(payload.quote),
    unrealizedPnL: parseNum(payload.unrealized_pnl),
    entryPrice: parseNum(payload.entry_price),
    positionAmount: parseNum(payload.position_amount),
    position: payload.position
  });

  let totalBalance = 0;
  let totalBase = 0;
  let totalQuote = 0;
  let totalUnrealizedPnL = 0;
  for (const [, pairData] of aggregate.pairs) {
    totalBalance += pairData.totalQuote;
    totalBase += (pairData.base || 0);
    totalQuote += (pairData.quote || 0);
    totalUnrealizedPnL += (pairData.unrealizedPnL || 0);
  }
  aggregate.totalBalance = totalBalance;
  aggregate.totalBase = totalBase;
  aggregate.totalQuote = totalQuote;
  aggregate.totalUnrealizedPnL = totalUnrealizedPnL;

  if (!aggregate.initialBalance && totalBalance > 0) {
    aggregate.initialBalance = totalBalance;
  }

  const view = ensureModelView(aggregateKey);
  renderModelNumbers(view, aggregate);
  updateGlobalChart(aggregateKey, quoteCurrency, aggregate.totalBalance, payload.ts);
}

let balanceSource = null;

function connectSSE() {
  if (balanceSource) {
    balanceSource.close();
  }

  setStatusPill(balanceStreamStatusEl, 'Balance: connecting', 'warn');
  balanceSource = new EventSource('/balance/stream');

  balanceSource.onopen = () => {
    setStatusPill(balanceStreamStatusEl, 'Balance: connected', 'ok');
  };

  balanceSource.onerror = () => {
    setStatusPill(balanceStreamStatusEl, 'Balance: reconnecting', 'warn');
    if (balanceSource.readyState === EventSource.CLOSED) {
      setTimeout(connectSSE, 3000);
    }
  };

  balanceSource.addEventListener('no_data', () => {
    if (datasetByPair.size === 0) {
      const emptyState = document.getElementById('emptyState');
      if (emptyState) {
        emptyState.innerHTML = `
          <p>No balance data yet.</p>
          <p class="muted">
            Waiting for the first balance snapshot from an active bot.
            Check that the bot is running and the stream stays connected.
          </p>
        `;
      }
    }
  });

  balanceSource.addEventListener('balance', (event) => {
    try {
      const payload = JSON.parse(event.data);
      setStatusPill(balanceStreamStatusEl, 'Balance: connected', 'ok');
      handlePayload(payload);
      refreshStatusSummary();
    } catch (err) {
      console.error('payload parse', err);
    }
  });


}

connectSSE();

const aiDecisionsContainer = document.getElementById('aiDecisions');
const modelFiltersContainer = document.getElementById('modelFilters');
const MAX_DECISIONS = 1000;
let currentFilter = null;

const knownModels = [
  'yandex', 'deepseek', 'qwen', 'moonshot',
  'openai', 'google', 'anthropic', 'xai', 'dca'
];

function filterDecisions(model) {
  if (currentFilter === model) {
    currentFilter = null;
  } else {
    currentFilter = model;
  }

  const buttons = modelFiltersContainer.querySelectorAll('.filter-btn');
  buttons.forEach(btn => {
    if (btn.dataset.model === currentFilter) {
      btn.classList.add('active');
    } else {
      btn.classList.remove('active');
    }
  });

  const cards = aiDecisionsContainer.querySelectorAll('.ai-decision-card');
  const filterTerm = currentFilter === 'xai' ? 'x-ai' : currentFilter;
  cards.forEach(card => {
    const cardModel = card.dataset.model;
    if (!currentFilter || (cardModel && cardModel.includes(filterTerm))) {
      card.style.display = 'block';
    } else {
      card.style.display = 'none';
    }
  });
}

function renderFilterButtons() {
  if (!modelFiltersContainer) return;

  knownModels.forEach(model => {
    const btn = document.createElement('button');
    btn.className = 'filter-btn';
    btn.textContent = model;
    btn.dataset.model = model;
    btn.onclick = () => filterDecisions(model);
    modelFiltersContainer.appendChild(btn);
  });
}

renderFilterButtons();

function formatTime(ts) {
  if (!ts) return '';
  const date = new Date(ts);
  if (Number.isNaN(date.getTime())) return '';
  const datePart = date.toLocaleDateString([], { day: '2-digit', month: '2-digit', year: 'numeric' });
  const timePart = date.toLocaleTimeString([], { hour12: false });
  return `${datePart} ${timePart}`;
}

function createDecisionCard(decision) {
  const card = document.createElement('div');
  card.className = 'ai-decision-card';

  if (decision.model) {
    card.dataset.model = normalizeModel(decision.model).toLowerCase();
  }

  if (currentFilter && card.dataset.model) {
    const filterTerm = currentFilter === 'xai' ? 'x-ai' : currentFilter;
    if (!card.dataset.model.includes(filterTerm)) {
      card.style.display = 'none';
    }
  }

  const header = document.createElement('div');
  header.className = 'ai-decision-header';

  const action = document.createElement('div');
  action.className = 'ai-decision-action ' + decision.action;
  action.textContent = decision.action.replace(/_/g, ' ');

  const time = document.createElement('div');
  time.className = 'ai-decision-time';
  time.textContent = formatTime(decision.ts);

  header.append(action, time);
  card.appendChild(header);

  if (decision.model) {
    const model = document.createElement('div');
    model.style.fontWeight = '700';
    model.style.marginBottom = '.5rem';
    model.textContent = decision.model;
    card.appendChild(model);
  }

  if (decision.position_side) {
    const positionSide = document.createElement('div');
    positionSide.style.fontWeight = '700';
    positionSide.style.marginBottom = '.5rem';
    positionSide.style.fontSize = '.65rem';
    positionSide.style.textTransform = 'lowercase';
    positionSide.style.letterSpacing = '.08em';
    positionSide.style.display = 'inline-block';
    positionSide.style.padding = '.3rem .6rem';

    const positionText = decision.position_side.toLowerCase().includes('short') ? 'short' : 'long';
    const borderColor = positionText === 'short' ? '#d7263d' : '#1b9aaa';

    positionSide.textContent = positionText;
    positionSide.style.border = '2px solid ' + borderColor;
    card.appendChild(positionSide);
  }

  if (decision.pair) {
    const pair = document.createElement('div');
    pair.style.fontSize = '.65rem';
    pair.style.marginBottom = '.5rem';
    pair.style.color = 'var(--ink-mid)';
    pair.textContent = decision.pair;
    card.appendChild(pair);
  }

  const meta = document.createElement('div');
  meta.className = 'ai-decision-meta';

  if (decision.current_price) {
    const price = document.createElement('span');
    price.className = 'ai-meta-pill';
    price.textContent = 'Price: ' + parseFloat(decision.current_price).toFixed(2);
    meta.appendChild(price);
  }

  if (decision.risk_percent) {
    const risk = document.createElement('span');
    risk.className = 'ai-meta-pill';
    risk.textContent = 'Risk: ' + decision.risk_percent.toFixed(1) + '%';
    meta.appendChild(risk);
  }

  if (decision.take_profit_price) {
    const tp = document.createElement('span');
    tp.className = 'ai-meta-pill';
    tp.textContent = 'TP: ' + decision.take_profit_price.toFixed(2);
    meta.appendChild(tp);
  }

  if (decision.stop_loss_price) {
    const sl = document.createElement('span');
    sl.className = 'ai-meta-pill';
    sl.textContent = 'SL: ' + decision.stop_loss_price.toFixed(2);
    meta.appendChild(sl);
  }

  if (decision.avg_entry_price && parseFloat(decision.avg_entry_price) > 0) {
    const avgPrice = document.createElement('span');
    avgPrice.className = 'ai-meta-pill';
    avgPrice.textContent = 'Avg Entry: ' + parseFloat(decision.avg_entry_price).toFixed(2);
    meta.appendChild(avgPrice);
  }

  if (decision.trade_part) {
    const part = document.createElement('span');
    part.className = 'ai-meta-pill';
    part.textContent = 'Trade Part: ' + decision.trade_part;
    meta.appendChild(part);
  }

  if (meta.children.length > 0) {
    card.appendChild(meta);
  }

  if (decision.reasoning) {
    const reasoning = document.createElement('div');
    reasoning.className = 'ai-decision-reasoning';
    reasoning.textContent = decision.reasoning;
    card.appendChild(reasoning);
  }

  return card;
}

let aiDecisionSource = null;

function connectAIDecisionSSE() {
  if (aiDecisionSource) {
    aiDecisionSource.close();
  }

  setStatusPill(decisionStreamStatusEl, 'Decisions: connecting', 'warn');
  aiDecisionSource = new EventSource('/decisions/stream');

  aiDecisionSource.onopen = () => {
    setStatusPill(decisionStreamStatusEl, 'Decisions: connected', 'ok');
  };

  aiDecisionSource.onerror = () => {
    setStatusPill(decisionStreamStatusEl, 'Decisions: reconnecting', 'warn');
    if (aiDecisionSource.readyState === EventSource.CLOSED) {
      setTimeout(connectAIDecisionSSE, 3000);
    }
  };

  aiDecisionSource.addEventListener('decision', (event) => {
    try {
      const payload = JSON.parse(event.data);
      const decision = payload.data;
      const type = payload.type;

      if (type === 'dca') {
        decision.model = 'DCA';
      }

      setStatusPill(decisionStreamStatusEl, 'Decisions: connected', 'ok');
      const card = createDecisionCard(decision);
      aiDecisionsContainer.insertBefore(card, aiDecisionsContainer.firstChild);
      refreshStatusSummary();

      while (aiDecisionsContainer.children.length > MAX_DECISIONS) {
        aiDecisionsContainer.removeChild(aiDecisionsContainer.lastChild);
      }
    } catch (err) {
      console.error('Decision parse error', err);
    }
  });

  aiDecisionSource.addEventListener('error', () => {
    console.log('Decision stream reconnecting...');
  });
}

connectAIDecisionSSE();
refreshStatusSummary();
setInterval(refreshStatusSummary, 5000);

const setupButton = document.getElementById('openSetupWizard');

function createSetupField(id, label, hint, inputElement) {
  const field = document.createElement('div');
  field.className = 'setup-field';

  const labelEl = document.createElement('label');
  labelEl.className = 'setup-label';
  labelEl.htmlFor = id;
  labelEl.textContent = label;

  const hintEl = document.createElement('div');
  hintEl.className = 'setup-hint';
  hintEl.textContent = hint;

  inputElement.id = id;
  inputElement.classList.add(inputElement.tagName === 'SELECT' ? 'setup-select' : 'setup-input');

  field.append(labelEl, inputElement, hintEl);
  return field;
}

const DEFAULT_PAIR_DATA = {
  strategy: 'dca', platform: 'binance', pair: 'BTC_USDT', marketType: 'spot',
  pollInterval: '5m', amount: '10', maxDcaTrades: '15', buyThreshold: '3.5',
  sellThreshold: '0.75', apiUrl: 'https://openrouter.ai/api/v1/chat/completions',
  apiKey: '', model: 'deepseek/deepseek-v3-0324', primaryTimeframe: '3m',
  higherTimeframe: '', lookbackPeriods: '', higherLookbackPeriods: '',
  maxLeverage: '', leverage: '', llmProxyUrl: '',
  telegramBotToken: '', telegramChatID: ''
};

function createPairCard(index, data, onRemove, onChange) {
  const card = document.createElement('div');
  card.className = 'setup-pair-card';
  card.dataset.index = index;

  const cardHeader = document.createElement('div');
  cardHeader.className = 'setup-pair-card-header';
  const cardTitle = document.createElement('span');
  cardTitle.className = 'setup-pair-card-title';
  cardTitle.textContent = data.pair || 'New pair';
  const removeBtn = document.createElement('button');
  removeBtn.type = 'button';
  removeBtn.className = 'setup-pair-remove-btn';
  removeBtn.textContent = 'Remove';
  removeBtn.onclick = () => onRemove(card);
  cardHeader.append(cardTitle, removeBtn);
  card.appendChild(cardHeader);

  const listen = (el) => {
    el.addEventListener('input', onChange);
    el.addEventListener('change', onChange);
    return el;
  };

  // Strategy & exchange
  const group1Title = document.createElement('h3');
  group1Title.className = 'setup-group-title';
  group1Title.textContent = 'Strategy & exchange';

  const row1 = document.createElement('div');
  row1.className = 'setup-row';

  const strategySelect = listen(document.createElement('select'));
  [['dca', 'DCA \u2014 rule-based averaging'], ['ai', 'AI \u2014 LLM-based trading']].forEach(([v, t]) => {
    const opt = document.createElement('option');
    opt.value = v; opt.textContent = t;
    strategySelect.appendChild(opt);
  });
  strategySelect.value = data.strategy || 'dca';

  const platformSelect = listen(document.createElement('select'));
  ['binance', 'bybit', 'hyperliquid', 'simulate'].forEach((v) => {
    const opt = document.createElement('option');
    opt.value = v; opt.textContent = v.charAt(0).toUpperCase() + v.slice(1);
    platformSelect.appendChild(opt);
  });
  platformSelect.value = data.platform || 'binance';

  row1.append(
    createSetupField('setupStrategy_' + index, 'Strategy', 'Choose between DCA or AI strategy.', strategySelect),
    createSetupField('setupPlatform_' + index, 'Exchange', 'Binance, Bybit, Hyperliquid or simulation.', platformSelect)
  );

  // Pair, market, interval
  const group2Title = document.createElement('h3');
  group2Title.className = 'setup-group-title';
  group2Title.textContent = 'Pair, market and interval';

  const row2 = document.createElement('div');
  row2.className = 'setup-row';
  const pairInput = listen(document.createElement('input'));
  pairInput.type = 'text';
  pairInput.value = data.pair || 'BTC_USDT';
  pairInput.addEventListener('input', () => {
    cardTitle.textContent = pairInput.value.trim() || 'New pair';
  });

  const marketSelect = listen(document.createElement('select'));
  [['spot', 'Spot'], ['margin', 'Margin']].forEach(([v, t]) => {
    const opt = document.createElement('option');
    opt.value = v; opt.textContent = t;
    marketSelect.appendChild(opt);
  });
  marketSelect.value = data.marketType || 'spot';

  row2.append(
    createSetupField('setupPair_' + index, 'Trading pair', 'Format: BASE_QUOTE (e.g. BTC_USDT).', pairInput),
    createSetupField('setupMarketType_' + index, 'Market type', 'Spot or margin.', marketSelect)
  );

  const row2b = document.createElement('div');
  row2b.className = 'setup-row';
  const pollInput = listen(document.createElement('input'));
  pollInput.type = 'text';
  pollInput.value = data.pollInterval || '5m';
  row2b.append(
    createSetupField('setupPollInterval_' + index, 'Price check interval', 'How often to fetch price (e.g. 30s, 1m, 5m).', pollInput)
  );

  // DCA settings
  const dcaSection = document.createElement('div');
  const group3Title = document.createElement('h3');
  group3Title.className = 'setup-group-title';
  group3Title.textContent = 'DCA settings';
  const dcaRow1 = document.createElement('div');
  dcaRow1.className = 'setup-row';
  const amountInput = listen(document.createElement('input'));
  amountInput.type = 'text';
  amountInput.value = data.amount || '10';
  const maxTradesInput = listen(document.createElement('input'));
  maxTradesInput.type = 'text';
  maxTradesInput.value = data.maxDcaTrades || '15';
  dcaRow1.append(
    createSetupField('setupAmount_' + index, 'Trade amount (%)', 'Percentage of quote balance to allocate (1\u2013100).', amountInput),
    createSetupField('setupMaxDcaTrades_' + index, 'Max DCA orders', 'Maximum additional safety orders per series.', maxTradesInput)
  );
  const dcaRow2 = document.createElement('div');
  dcaRow2.className = 'setup-row';
  const buyThrInput = listen(document.createElement('input'));
  buyThrInput.type = 'text';
  buyThrInput.value = data.buyThreshold || '3.5';
  const sellThrInput = listen(document.createElement('input'));
  sellThrInput.type = 'text';
  sellThrInput.value = data.sellThreshold || '0.75';
  dcaRow2.append(
    createSetupField('setupBuyThreshold_' + index, 'Price drop to buy, %', 'New buy when price drops by this percent.', buyThrInput),
    createSetupField('setupSellThreshold_' + index, 'Take-profit %', 'Close position when price rises by this percent.', sellThrInput)
  );
  dcaSection.append(group3Title, dcaRow1, dcaRow2);

  // AI settings
  const aiSection = document.createElement('div');
  const group4Title = document.createElement('h3');
  group4Title.className = 'setup-group-title';
  group4Title.textContent = 'AI settings';
  const aiRow1 = document.createElement('div');
  aiRow1.className = 'setup-row';
  const apiUrlInput = listen(document.createElement('input'));
  apiUrlInput.type = 'text';
  apiUrlInput.value = data.apiUrl || 'https://openrouter.ai/api/v1/chat/completions';
  const apiKeyInput = listen(document.createElement('input'));
  apiKeyInput.type = 'password';
  apiKeyInput.autocomplete = 'off';
  apiKeyInput.value = data.apiKey || '';
  const modelInput = listen(document.createElement('input'));
  modelInput.type = 'text';
  modelInput.value = data.model || 'deepseek/deepseek-v3-0324';
  const primaryTfSelect = listen(document.createElement('select'));
  ['1m', '3m', '5m', '15m', '1h'].forEach((v) => {
    const opt = document.createElement('option');
    opt.value = v; opt.textContent = v;
    primaryTfSelect.appendChild(opt);
  });
  primaryTfSelect.value = data.primaryTimeframe || '3m';
  aiRow1.append(
    createSetupField('setupApiUrl_' + index, 'LLM API URL', 'OpenAI-compatible endpoint.', apiUrlInput),
    createSetupField('setupApiKey_' + index, 'LLM API key', 'Stored only in generated config file.', apiKeyInput)
  );
  const aiRow2 = document.createElement('div');
  aiRow2.className = 'setup-row';
  aiRow2.append(
    createSetupField('setupModel_' + index, 'Model', 'LLM model identifier (e.g. deepseek/deepseek-v3-0324).', modelInput),
    createSetupField('setupPrimaryTimeframe_' + index, 'Primary timeframe', 'Main timeframe to analyse.', primaryTfSelect)
  );
  aiSection.append(group4Title, aiRow1, aiRow2);

  // Telegram notifications
  const tgSection = document.createElement('div');
  const tgTitle = document.createElement('div');
  tgTitle.className = 'setup-group-title';
  tgTitle.textContent = 'Telegram Notifications (optional)';
  const tgToggleRow = document.createElement('div');
  tgToggleRow.className = 'setup-row';
  const tgToggle = listen(document.createElement('input'));
  tgToggle.type = 'checkbox';
  tgToggle.id = 'setupTelegramEnable_' + index;
  const hasTg = !!(data.telegramBotToken && data.telegramChatID);
  tgToggle.checked = hasTg;
  const tgToggleLabel = document.createElement('label');
  tgToggleLabel.htmlFor = 'setupTelegramEnable_' + index;
  tgToggleLabel.textContent = 'Send a message when a buy or sell decision is made';
  tgToggleLabel.style.cursor = 'pointer';
  tgToggle.style.marginRight = '0.4rem';
  tgToggleRow.append(tgToggle, tgToggleLabel);

  const tgFields = document.createElement('div');
  tgFields.style.display = hasTg ? 'block' : 'none';
  const tgRow = document.createElement('div');
  tgRow.className = 'setup-row';
  const tgTokenInput = listen(document.createElement('input'));
  tgTokenInput.type = 'password';
  tgTokenInput.placeholder = '123456789:AABBcc...';
  tgTokenInput.value = data.telegramBotToken || '';
  const tgChatInput = listen(document.createElement('input'));
  tgChatInput.type = 'text';
  tgChatInput.placeholder = '-100123456789';
  tgChatInput.value = data.telegramChatID || '';
  tgRow.append(
    createSetupField('setupTgToken_' + index, 'Bot token', 'From @BotFather.', tgTokenInput),
    createSetupField('setupTgChat_' + index, 'Chat ID', 'Find via @userinfobot.', tgChatInput)
  );
  tgFields.appendChild(tgRow);
  tgToggle.addEventListener('change', () => {
    tgFields.style.display = tgToggle.checked ? 'block' : 'none';
  });
  tgSection.append(tgTitle, tgToggleRow, tgFields);

  // Strategy toggle
  const toggleSections = () => {
    const useAi = strategySelect.value === 'ai';
    dcaSection.style.display = useAi ? 'none' : 'block';
    aiSection.style.display = useAi ? 'block' : 'none';
  };
  strategySelect.addEventListener('change', toggleSections);
  toggleSections();

  card.append(group1Title, row1, group2Title, row2, row2b, dcaSection, aiSection, tgSection);

  card.collectPayload = () => ({
    strategy: strategySelect.value,
    platform: platformSelect.value,
    pair: pairInput.value.trim(),
    marketType: marketSelect.value,
    pollInterval: pollInput.value.trim(),
    amount: amountInput.value.trim(),
    maxDcaTrades: maxTradesInput.value.trim(),
    buyThreshold: buyThrInput.value.trim(),
    sellThreshold: sellThrInput.value.trim(),
    apiUrl: apiUrlInput.value.trim(),
    apiKey: apiKeyInput.value,
    model: modelInput.value.trim(),
    primaryTimeframe: primaryTfSelect.value,
    higherTimeframe: '',
    lookbackPeriods: '',
    higherLookbackPeriods: '',
    maxLeverage: '',
    leverage: '',
    llmProxyUrl: '',
    telegramBotToken: tgToggle.checked ? tgTokenInput.value : '',
    telegramChatID: tgToggle.checked ? tgChatInput.value.trim() : ''
  });

  return card;
}

function openSetupWizard() {
  if (document.getElementById('setupOverlay')) {
    return;
  }

  const overlay = document.createElement('div');
  overlay.id = 'setupOverlay';
  overlay.className = 'setup-overlay';

  const panel = document.createElement('div');
  panel.className = 'setup-panel setup-panel--multi';

  const header = document.createElement('div');
  header.className = 'setup-panel-header';
  const titleWrap = document.createElement('div');
  const title = document.createElement('h2');
  title.className = 'setup-panel-title';
  title.textContent = 'Configuration';
  const subtitle = document.createElement('p');
  subtitle.className = 'setup-panel-subtitle';
  subtitle.textContent = 'Manage trading pairs and settings.';
  titleWrap.append(title, subtitle);
  const closeBtn = document.createElement('button');
  closeBtn.type = 'button';
  closeBtn.className = 'setup-close-btn';
  closeBtn.textContent = '\u2715';
  closeBtn.onclick = () => overlay.remove();
  header.append(titleWrap, closeBtn);

  const body = document.createElement('div');
  body.className = 'setup-body';

  const cardsContainer = document.createElement('div');
  cardsContainer.className = 'setup-cards-container';

  const errorEl = document.createElement('div');
  errorEl.className = 'setup-error';
  errorEl.style.display = 'none';

  const footer = document.createElement('div');
  footer.className = 'setup-footer';
  const leftFooter = document.createElement('div');
  const statusEl = document.createElement('div');
  statusEl.className = 'setup-status';
  statusEl.textContent = 'Loading configuration...';
  leftFooter.appendChild(statusEl);
  const rightFooter = document.createElement('div');
  rightFooter.style.display = 'flex';
  rightFooter.style.gap = '0.5rem';
  const cancelBtn = document.createElement('button');
  cancelBtn.type = 'button';
  cancelBtn.className = 'setup-secondary-btn';
  cancelBtn.textContent = 'Cancel';
  cancelBtn.onclick = () => overlay.remove();
  const submitBtn = document.createElement('button');
  submitBtn.type = 'button';
  submitBtn.className = 'setup-primary-btn';
  submitBtn.textContent = 'Save config';
  submitBtn.disabled = true;
  rightFooter.append(cancelBtn, submitBtn);
  footer.append(leftFooter, rightFooter);

  let initialState = '';
  let cardIndex = 0;

  function collectAllPayloads() {
    const cards = cardsContainer.querySelectorAll('.setup-pair-card');
    const payloads = [];
    cards.forEach((c) => {
      if (c.collectPayload) payloads.push(c.collectPayload());
    });
    return payloads;
  }

  function checkChanges() {
    const current = JSON.stringify(collectAllPayloads());
    submitBtn.disabled = current === initialState;
  }

  function updateRemoveButtons() {
    const cards = cardsContainer.querySelectorAll('.setup-pair-card');
    const removeBtns = cardsContainer.querySelectorAll('.setup-pair-remove-btn');
    removeBtns.forEach((btn) => {
      btn.disabled = cards.length <= 1;
    });
  }

  function addCard(data) {
    const idx = cardIndex++;
    const card = createPairCard(idx, data, (cardEl) => {
      cardEl.remove();
      updateRemoveButtons();
      checkChanges();
    }, checkChanges);
    cardsContainer.appendChild(card);
    updateRemoveButtons();
    checkChanges();
  }

  const addPairBtn = document.createElement('button');
  addPairBtn.type = 'button';
  addPairBtn.className = 'setup-add-pair-btn';
  addPairBtn.textContent = '+ Add pair';
  addPairBtn.onclick = () => addCard({ ...DEFAULT_PAIR_DATA });

  body.append(cardsContainer, addPairBtn, errorEl);
  panel.append(header, body, footer);
  overlay.appendChild(panel);
  overlay.addEventListener('click', (e) => {
    if (e.target === overlay) overlay.remove();
  });
  document.body.appendChild(overlay);

  // Load existing config
  fetch('/api/setup/config')
    .then((res) => res.ok ? res.json() : [])
    .then((entries) => {
      if (!Array.isArray(entries) || entries.length === 0) {
        entries = [{ ...DEFAULT_PAIR_DATA }];
      }
      entries.forEach((entry) => addCard(entry));
      initialState = JSON.stringify(collectAllPayloads());
      submitBtn.disabled = true;
      statusEl.textContent = 'Config will be saved to config.gen.yaml';
    })
    .catch(() => {
      addCard({ ...DEFAULT_PAIR_DATA });
      initialState = JSON.stringify(collectAllPayloads());
      submitBtn.disabled = true;
      statusEl.textContent = 'Config will be saved to config.gen.yaml';
    });

  // Save handler
  submitBtn.addEventListener('click', () => {
    errorEl.style.display = 'none';
    errorEl.textContent = '';
    const payloads = collectAllPayloads();

    submitBtn.disabled = true;
    cancelBtn.disabled = true;
    statusEl.textContent = 'Saving config...';

    fetch('/api/setup/config', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payloads)
    })
      .then(async (res) => {
        if (!res.ok) {
          const text = await res.text();
          throw new Error(text || 'failed to save config');
        }
        return res.json().catch(() => ({}));
      })
      .then(() => {
        initialState = JSON.stringify(payloads);
        statusEl.textContent = 'Config saved. Bots restarting...';
        refreshStatusSummary();
        submitBtn.disabled = true;
        cancelBtn.disabled = false;
        submitBtn.classList.add('saved');
        setTimeout(() => overlay.remove(), 800);
      })
      .catch((err) => {
        errorEl.textContent = (err && err.message) || 'failed to save config';
        errorEl.style.display = 'block';
        statusEl.textContent = 'Something went wrong';
        submitBtn.disabled = false;
        cancelBtn.disabled = false;
      });
  });
}

if (setupButton) {
  setupButton.addEventListener('click', openSetupWizard);
}

fetch('/api/setup/status')
  .then((res) => {
    if (!res.ok) {
      return null;
    }
    return res.json();
  })
  .then((data) => {
    if (data && data.needsSetup) {
      openSetupWizard();
    }
  })
  .catch(() => {
  });
