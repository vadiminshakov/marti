const statusEl = document.getElementById('sse-status');
const pairContainer = document.getElementById('pairs');
const emptyState = document.getElementById('emptyState');
const chartCanvas = document.getElementById('globalChart');
const pairViews = new Map();
const datasetByPair = new Map();
const modelAggregates = new Map();
const modelPrimaryQuote = new Map();
const colorPalette = [
  { line:'#111111', fill:'rgba(17,17,17,0.12)' },
  { line:'#d7263d', fill:'rgba(215,38,61,0.15)' },
  { line:'#1b9aaa', fill:'rgba(27,154,170,0.15)' },
  { line:'#ff7f11', fill:'rgba(255,127,17,0.18)' },
  { line:'#3c91e6', fill:'rgba(60,145,230,0.15)' },
  { line:'#8e3b46', fill:'rgba(142,59,70,0.15)' },
  { line:'#2e282a', fill:'rgba(46,40,42,0.18)' }
];
let colorIndex = 0;

const normalizeModel = (value) => {
  if(typeof value !== 'string'){
    return '';
  }
  return value.trim();
};

const extractQuoteCurrency = (pair) => {
  if(!pair || typeof pair !== 'string'){ return 'UNKNOWN'; }
  const parts = pair.split('_');
  return parts.length > 1 ? parts[parts.length - 1] : 'UNKNOWN';
};

const shortenModelName = (model) => {
  if(!model || typeof model !== 'string'){ return '—'; }
  const parts = model.split('/');
  return parts.length > 1 ? parts[parts.length - 1] : model;
};

Chart.defaults.font.family = "'Space Mono', 'JetBrains Mono', monospace";
Chart.defaults.font.size = 11;
Chart.defaults.color = '#111111';

const buildGlobalChart = (ctx) => new Chart(ctx, {
  type: 'line',
  data: { labels: [], datasets: [] },
  options: {
    animation: false,
    responsive: true,
    interaction: { intersect: false, mode: 'index' },
    scales: {
      x:{
        ticks:{ color:'#111111', maxRotation:0, autoSkip:true },
        grid:{ color:'rgba(0,0,0,0.08)', borderColor:'#111111' }
      },
      y:{
        ticks:{ color:'#111111' },
        grid:{ color:'rgba(0,0,0,0.08)', borderColor:'#111111' }
      }
    },
    elements:{ line:{ borderCapStyle:'square' } },
    plugins:{
      decimation:{
        enabled:true,
        algorithm:'lttb',
        samples:500
      },
      legend:{ display:true, labels:{ usePointStyle:true, boxWidth:12 } },
      tooltip:{
        backgroundColor:'#ffffff',
        borderColor:'#111111',
        borderWidth:1,
        titleColor:'#111111',
        bodyColor:'#111111',
        displayColors:true
      }
    }
  }
});

const chartCtx = chartCanvas.getContext('2d');
chartCtx.imageSmoothingEnabled = false;
const globalChart = buildGlobalChart(chartCtx);

const parseNum = (value) => {
  const num = parseFloat(value);
  return Number.isFinite(num) ? num : 0;
};

const formatTs = (ts) => {
  if(!ts){ return 'Waiting…'; }
  const date = new Date(ts);
  if(Number.isNaN(date.getTime())){ return 'Waiting…'; }
  return date.toLocaleTimeString([], { hour12:false });
};

const nextColor = () => {
  const color = colorPalette[colorIndex % colorPalette.length];
  colorIndex += 1;
  return color;
};

function ensureDataset(model, quoteCurrency){
  const key = model || '—';
  if(datasetByPair.has(key)){
    return datasetByPair.get(key);
  }
  const palette = nextColor();
  const dataset = {
    label: shortenModelName(key),
    data: new Array(globalChart.data.labels.length).fill(null),
    borderColor: palette.line,
    backgroundColor: palette.fill,
    borderWidth: 2,
    pointRadius: 0,
    tension: 0.15,
    fill: false
  };
  globalChart.data.datasets.push(dataset);
  datasetByPair.set(key, dataset);
  return dataset;
}

function appendGlobalLabel(label){
  globalChart.data.labels.push(label);
  globalChart.data.datasets.forEach((dataset) => {
    const lastIndex = dataset.data.length - 1;
    const lastValue = lastIndex >= 0 ? dataset.data[lastIndex] : null;
    dataset.data.push(lastValue);
  });
  if(globalChart.data.labels.length > 50000){
    globalChart.data.labels.shift();
    globalChart.data.datasets.forEach((dataset) => {
      dataset.data.shift();
    });
  }
}

function updateGlobalChart(model, quoteCurrency, totalBalance, ts){
  const tsDate = ts ? new Date(ts) : new Date();
  const labelDate = Number.isNaN(tsDate.getTime()) ? new Date() : tsDate;
  const tickLabel = labelDate.toLocaleTimeString([], { hour12:false });
  appendGlobalLabel(tickLabel);
  const dataset = ensureDataset(model, quoteCurrency);
  dataset.data[dataset.data.length - 1] = totalBalance;
  globalChart.update('none');
}

function ensureModelView(model){
  const safeModel = model || '—';
  if(pairViews.has(safeModel)){
    return pairViews.get(safeModel);
  }

  if(emptyState){
    emptyState.remove();
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

function deriveTotal(payload){
  if(payload.total_quote){
    const numeric = parseNum(payload.total_quote);
    return { numeric, display: payload.total_quote };
  }
  const price = parseNum(payload.price);
  const base = parseNum(payload.base);
  const quote = parseNum(payload.quote);
  const numeric = (price ? base * price : 0) + quote;
  return { numeric, display: numeric.toFixed(4) };
}

function renderModelNumbers(view, aggregate){
  const quoteCurrency = aggregate.quoteCurrency || '';
  view.totalEl.textContent = aggregate.totalBalance.toFixed(2) + (quoteCurrency ? ' ' + quoteCurrency : '');

  let latestTimestamp = null;
  for(const [, pairData] of aggregate.pairs){
    if(!latestTimestamp || (pairData.timestamp && new Date(pairData.timestamp) > new Date(latestTimestamp))){
      latestTimestamp = pairData.timestamp;
    }
  }
  view.updatedEl.textContent = formatTs(latestTimestamp);
}

function handlePayload(payload){
  const pairLabel = payload.pair || '—';
  const model = normalizeModel(payload.model);
  const quoteCurrency = extractQuoteCurrency(pairLabel);

  if(!modelPrimaryQuote.has(model)){
    modelPrimaryQuote.set(model, quoteCurrency);
  }

  const primaryQuote = modelPrimaryQuote.get(model);
  if(quoteCurrency !== primaryQuote){
    return;
  }

  let aggregate = modelAggregates.get(model);
  if(!aggregate){
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
  for(const [, pairData] of aggregate.pairs){
    totalBalance += pairData.totalQuote;
  }
  aggregate.totalBalance = totalBalance;

  const view = ensureModelView(model);
  renderModelNumbers(view, aggregate);
  updateGlobalChart(model, quoteCurrency, aggregate.totalBalance, payload.ts);
}

function connectSSE(){
  const source = new EventSource('/balance/stream');
  statusEl.textContent = 'Status: receiving data';
  source.addEventListener('balance', (event) => {
    try{
      const payload = JSON.parse(event.data);
      handlePayload(payload);
    }catch(err){
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

function formatTime(ts){
  if(!ts) return '';
  const date = new Date(ts);
  if(Number.isNaN(date.getTime())) return '';
  return date.toLocaleTimeString([], { hour12:false });
}

function createDecisionCard(decision){
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

  if(decision.model){
    const model = document.createElement('div');
    model.style.fontWeight = '700';
    model.style.marginBottom = '.5rem';
    model.textContent = decision.model;
    card.appendChild(model);
  }

  if(decision.position_side){
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

  if(decision.pair){
    const pair = document.createElement('div');
    pair.style.fontSize = '.65rem';
    pair.style.marginBottom = '.5rem';
    pair.style.color = 'var(--ink-mid)';
    pair.textContent = decision.pair;
    card.appendChild(pair);
  }

  const meta = document.createElement('div');
  meta.className = 'ai-decision-meta';

  if(decision.current_price){
    const price = document.createElement('span');
    price.className = 'ai-meta-pill';
    price.textContent = 'Price: ' + parseFloat(decision.current_price).toFixed(2);
    meta.appendChild(price);
  }

  if(decision.risk_percent){
    const risk = document.createElement('span');
    risk.className = 'ai-meta-pill';
    risk.textContent = 'Risk: ' + decision.risk_percent.toFixed(1) + '%';
    meta.appendChild(risk);
  }

  if(decision.take_profit_price){
    const tp = document.createElement('span');
    tp.className = 'ai-meta-pill';
    tp.textContent = 'TP: ' + decision.take_profit_price.toFixed(2);
    meta.appendChild(tp);
  }

  if(decision.stop_loss_price){
    const sl = document.createElement('span');
    sl.className = 'ai-meta-pill';
    sl.textContent = 'SL: ' + decision.stop_loss_price.toFixed(2);
    meta.appendChild(sl);
  }

  if(meta.children.length > 0){
    card.appendChild(meta);
  }

  if(decision.reasoning){
    const reasoning = document.createElement('div');
    reasoning.className = 'ai-decision-reasoning';
    reasoning.textContent = decision.reasoning;
    card.appendChild(reasoning);
  }

  return card;
}

function connectAIDecisionSSE(){
  const source = new EventSource('/ai/decisions/stream');

  source.addEventListener('ai_decision', (event) => {
    try{
      const decision = JSON.parse(event.data);
      const card = createDecisionCard(decision);
      aiDecisionsContainer.insertBefore(card, aiDecisionsContainer.firstChild);

      // Limit number of displayed decisions
      while(aiDecisionsContainer.children.length > MAX_DECISIONS){
        aiDecisionsContainer.removeChild(aiDecisionsContainer.lastChild);
      }
    }catch(err){
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
