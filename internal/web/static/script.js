const statusEl = document.getElementById('sse-status');
const pairContainer = document.getElementById('pairs');
const emptyState = document.getElementById('emptyState');
const chartCanvas = document.getElementById('globalChart');
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

const normalizeModel = (value) => {
  if (typeof value !== 'string') {
    return '';
  }
  return value.trim();
};

const extractQuoteCurrency = (pair) => {
  if (!pair || typeof pair !== 'string') { return 'UNKNOWN'; }
  const parts = pair.split('_');
  return parts.length > 1 ? parts[parts.length - 1] : 'UNKNOWN';
};

const shortenModelName = (model) => {
  if (!model || typeof model !== 'string') { return '—'; }
  // Handle gpt:// URIs specifically
  if (model.startsWith('gpt://')) {
    const parts = model.replace('gpt://', '').split('/');
    // Return the part after the folder ID (e.g. yandexgpt from gpt://folder/yandexgpt/rc)
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

// Helper function to convert hex to RGB
function hexToRgb(hex) {
  const result = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex);
  return result ? {
    r: parseInt(result[1], 16),
    g: parseInt(result[2], 16),
    b: parseInt(result[3], 16)
  } : { r: 0, g: 0, b: 0 };
}

// Helper function to calculate distance from point to line segment
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

const buildGlobalChart = (ctx) => {
  let hoveredDatasetIndex = null;
  const hoverThreshold = 12;

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
        dataset.backgroundColor = `rgba(${rgb.r}, ${rgb.g}, ${rgb.b}, 0.05)`;
      }
    });

    chart.update('none');
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
      interaction: { intersect: false, mode: 'index' },
      layout: {
        padding: {
          right: 50
        }
      },
      onHover: (event, activeElements) => {
        const chart = event.chart;
        const canvasPosition = Chart.helpers.getRelativePosition(event, chart);

        // Find which dataset line is being hovered based on proximity to line segments
        const newHoveredIndex = findHoveredDataset(chart, canvasPosition);

        if (newHoveredIndex !== hoveredDatasetIndex) {
          applyHoverStyles(chart, newHoveredIndex);
        }
      },
      scales: {
        x: {
          ticks: { color: '#888888', maxRotation: 0, autoSkip: true, font: { size: 10 } },
          grid: { display: false, drawBorder: false }
        },
        y: {
          ticks: { color: '#888888', font: { size: 10 } },
          grid: { color: 'rgba(0,0,0,0.03)', borderDash: [4, 4], drawBorder: false }
        }
      },
      elements: { line: { borderCapStyle: 'round' } },
      plugins: {
        decimation: {
          enabled: true,
          algorithm: 'lttb',
          samples: 500
        },
        legend: { display: true, labels: { usePointStyle: true, boxWidth: 8, font: { size: 10 } } },
        tooltip: {
          backgroundColor: 'rgba(255, 255, 255, 0.95)',
          borderColor: 'rgba(0, 0, 0, 0.1)',
          borderWidth: 1,
          titleColor: '#111111',
          bodyColor: '#444444',
          displayColors: true,
          padding: 10,
          cornerRadius: 4,
          titleFont: { size: 11, weight: 'bold' },
          bodyFont: { size: 11 }
        }
      }
    },
    plugins: [{
      id: 'logoPlugin',
      afterDraw: (chart) => {
        const ctx = chart.ctx;
        const xAxis = chart.scales.x;
        const yAxis = chart.scales.y;

        // Map of model keywords to logo filenames
        const modelLogos = {
          'yandex': 'yandex.webp',
          'deepseek': 'deepseek.webp',
          'qwen': 'qwen.webp',
          'moonshot': 'moonshot.webp',
          'deepcogito': 'deepcogito.webp'
        };

        // Cache for preloaded images
        if (!chart.logoImages) {
          chart.logoImages = {};
          let loadedCount = 0;
          const totalLogos = Object.keys(modelLogos).length;
          Object.entries(modelLogos).forEach(([key, filename]) => {
            const img = new Image();
            img.src = filename;
            img.onload = () => {
              loadedCount++;
              console.log(`Logo loaded: ${filename} (${loadedCount}/${totalLogos})`);
              // Only update once when all logos are loaded
              if (loadedCount === totalLogos) {
                setTimeout(() => chart.update('none'), 100);
              }
            };
            img.onerror = () => {
              console.error(`Failed to load logo: ${filename}`);
            };
            chart.logoImages[key] = img;
          });
        }

        // Collect all logo positions to prevent overlap
        const logoPositions = [];

        // Get hovered dataset index from the chart instance
        const hoveredIndex = chart.hoveredDatasetIndex;

        chart.data.datasets.forEach((dataset, i) => {
          const meta = chart.getDatasetMeta(i);
          if (meta.hidden) return;

          // Find the last non-null data point
          let lastIndex = -1;
          for (let j = dataset.data.length - 1; j >= 0; j--) {
            if (dataset.data[j] !== null && dataset.data[j] !== undefined) {
              lastIndex = j;
              break;
            }
          }

          if (lastIndex === -1) return;

          // Use the right edge of the chart area instead of last point's x position
          const x = xAxis.right;
          let y = yAxis.getPixelForValue(dataset.data[lastIndex]);

          // Skip if coordinates are invalid
          if (!isFinite(x) || !isFinite(y)) return;

          // Determine which logo to use based on dataset label or internal model key
          let logoImg = null;
          const modelKey = dataset.modelKey ? dataset.modelKey.toLowerCase() : '';

          for (const [key, img] of Object.entries(chart.logoImages)) {
            if (modelKey.includes(key)) {
              logoImg = img;
              break;
            }
          }

          const size = 32;

          // Check for overlap with existing logos and adjust y position
          for (const pos of logoPositions) {
            const xDist = Math.abs(x - pos.x);
            const yDist = Math.abs(y - pos.y);

            // If logos are close, offset vertically
            if (xDist < size + 5 && yDist < size + 5) {
              y = pos.y + size + 8;
            }
          }

          logoPositions.push({ x, y });

          ctx.save();

          const logoX = x + 5;
          const logoY = y - size / 2;

          // Determine opacity based on hover state
          let opacity = 1.0;
          if (hoveredIndex !== null && hoveredIndex !== undefined) {
            opacity = i === hoveredIndex ? 1.0 : 0.3;
          }

          if (logoImg && logoImg.complete && logoImg.naturalHeight !== 0) {
            // Draw logo with appropriate opacity
            ctx.globalAlpha = opacity;
            ctx.imageSmoothingEnabled = true;
            ctx.drawImage(logoImg, logoX, logoY, size, size);

            console.log(`Drew logo for ${modelKey} at (${logoX}, ${logoY}) with opacity ${opacity}`);
          } else {
            // Draw bold dot as fallback
            ctx.globalAlpha = opacity;
            ctx.beginPath();
            ctx.arc(x, y, 4, 0, 2 * Math.PI);
            ctx.fillStyle = dataset.borderColor;
            ctx.fill();
          }
          ctx.restore();
        });
      }
    }]
  });

  chart.applyHoverStyles = (newHoveredIndex) => applyHoverStyles(chart, newHoveredIndex);
  return chart;
};

const chartCtx = chartCanvas.getContext('2d');
chartCtx.imageSmoothingEnabled = false;
const globalChart = buildGlobalChart(chartCtx);

chartCanvas.addEventListener('mouseleave', () => {
  if (globalChart && typeof globalChart.applyHoverStyles === 'function') {
    // Reset highlighting when the cursor leaves the chart area
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
  gradient.addColorStop(0, color.replace('0.15)', '0.1)').replace('0.12)', '0.1)').replace('0.18)', '0.1)')); // Lighter fill
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
    label: shortenModelName(key),
    data: new Array(globalChart.data.labels.length).fill(null),
    borderColor: palette.line,
    backgroundColor: palette.line,
    borderWidth: 2,
    pointRadius: 0,
    pointHoverRadius: 4,
    pointBackgroundColor: '#ffffff',
    pointBorderColor: palette.line,
    pointBorderWidth: 2,
    tension: 0.3,
    fill: false
  };
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
  const tickLabel = labelDate.toLocaleTimeString([], { hour12: false });
  appendGlobalLabel(tickLabel);
  const dataset = ensureDataset(model);
  dataset.data[dataset.data.length - 1] = totalBalance;
  globalChart.update('none');
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
  title.textContent = shortenModelName(safeModel);
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

  card.append(header, equity);
  pairContainer.appendChild(card);

  const view = {
    totalEl: totalValue,
    updatedEl: updated
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

  let latestTimestamp = null;
  for (const [, pairData] of aggregate.pairs) {
    if (!latestTimestamp || (pairData.timestamp && new Date(pairData.timestamp) > new Date(latestTimestamp))) {
      latestTimestamp = pairData.timestamp;
    }
  }
  view.updatedEl.textContent = formatTs(latestTimestamp);
}

function handlePayload(payload) {
  const pairLabel = payload.pair || '—';
  const model = normalizeModel(payload.model);
  const quoteCurrency = extractQuoteCurrency(pairLabel);

  if (!modelPrimaryQuote.has(model)) {
    modelPrimaryQuote.set(model, quoteCurrency);
  }

  const primaryQuote = modelPrimaryQuote.get(model);
  if (quoteCurrency !== primaryQuote) {
    return;
  }

  let aggregate = modelAggregates.get(model);
  if (!aggregate) {
    aggregate = {
      model: model,
      quoteCurrency: quoteCurrency,
      pairs: new Map(),
      totalBalance: 0
    };
    modelAggregates.set(model, aggregate);
  }

  const total = deriveTotal(payload);
  aggregate.pairs.set(pairLabel, {
    totalQuote: total.numeric,
    timestamp: payload.ts
  });

  let totalBalance = 0;
  for (const [, pairData] of aggregate.pairs) {
    totalBalance += pairData.totalQuote;
  }
  aggregate.totalBalance = totalBalance;

  const view = ensureModelView(model);
  renderModelNumbers(view, aggregate);
  updateGlobalChart(model, quoteCurrency, aggregate.totalBalance, payload.ts);
}

function connectSSE() {
  const source = new EventSource('/balance/stream');
  statusEl.textContent = 'Status: receiving data';

  source.addEventListener('no_data', () => {
    if (datasetByPair.size === 0) {
      const emptyState = document.getElementById('emptyState');
      if (emptyState) {
        emptyState.innerHTML = `
          <p>No balance data yet.</p>
          <p class="muted">
            The bot is running but hasn't recorded its first balance snapshot.
            This should happen within a few seconds.
          </p>
        `;
      }
    }
  });

  source.addEventListener('balance', (event) => {
    try {
      const payload = JSON.parse(event.data);
      handlePayload(payload);
    } catch (err) {
      console.error('payload parse', err);
    }
  });
  source.addEventListener('error', () => {
    statusEl.textContent = 'Reconnecting…';
    source.close();
    setTimeout(connectSSE, 2000);
  });
}

connectSSE();

// AI Decisions Stream
const aiDecisionsContainer = document.getElementById('aiDecisions');
const MAX_DECISIONS = 50;

function formatTime(ts) {
  if (!ts) return '';
  const date = new Date(ts);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleTimeString([], { hour12: false });
}

function createDecisionCard(decision) {
  const card = document.createElement('div');
  card.className = 'ai-decision-card';

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

    // Normalize position text and set border color
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

function connectAIDecisionSSE() {
  const source = new EventSource('/ai/decisions/stream');

  source.addEventListener('ai_decision', (event) => {
    try {
      const decision = JSON.parse(event.data);
      const card = createDecisionCard(decision);
      aiDecisionsContainer.insertBefore(card, aiDecisionsContainer.firstChild);

      // Limit number of displayed decisions
      while (aiDecisionsContainer.children.length > MAX_DECISIONS) {
        aiDecisionsContainer.removeChild(aiDecisionsContainer.lastChild);
      }
    } catch (err) {
      console.error('AI decision parse error', err);
    }
  });

  source.addEventListener('error', () => {
    console.log('AI decision stream reconnecting...');
    source.close();
    setTimeout(connectAIDecisionSSE, 2000);
  });
}

connectAIDecisionSSE();
