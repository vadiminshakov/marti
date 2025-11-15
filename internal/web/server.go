package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/vadiminshakov/marti/internal/entity"
)

const snapshotPollInterval = 2 * time.Second

type balanceSnapshotReader interface {
	SnapshotsAfter(index uint64) ([]entity.BalanceSnapshotRecord, error)
}

// Server exposes HTTP endpoints serving the HTML UI and an SSE stream.
type Server struct {
	Addr  string
	Store balanceSnapshotReader
}

// NewServer creates a new web server instance.
func NewServer(addr string, store balanceSnapshotReader) *Server {
	return &Server{Addr: addr, Store: store}
}

// Start runs the HTTP server (blocking) and shuts it down when ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/balance/stream", s.handleBalanceStream)

	server := &http.Server{
		Addr:              s.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, indexHTML)
}

func (s *Server) handleBalanceStream(w http.ResponseWriter, r *http.Request) {
	if s.Store == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "snapshot store not available")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// send a comment heartbeat every 30s so proxies keep connection
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	pollTicker := time.NewTicker(snapshotPollInterval)
	defer pollTicker.Stop()

	lastIndex := uint64(0)
	sendSnapshots := func() error {
		records, err := s.Store.SnapshotsAfter(lastIndex)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return nil
		}
		for _, record := range records {
			payload, err := json.Marshal(record.Snapshot)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "event: balance\n")
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
			lastIndex = record.Index
		}
		return nil
	}

	if err := sendSnapshots(); err != nil {
		http.Error(w, "failed to load snapshots", http.StatusInternalServerError)
		log.Printf("balance stream initial load: %v", err)
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case <-pollTicker.C:
			if err := sendSnapshots(); err != nil {
				log.Printf("balance stream poll err: %v", err)
			}
		}
	}
}

// Multi-pair dashboard with a chart + stats per trading pair.
const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <title>Marti</title>
  <script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
  <link href="https://fonts.googleapis.com/css2?family=Press+Start+2P&family=Space+Mono:wght@400;700&display=swap" rel="stylesheet">
  <style>
    :root {
      --bg:#ffffff;
      --ink:#111111;
      --ink-mid:#4d4d4d;
      --ink-soft:#9c9c9c;
      --panel:#f6f6f6;
      --grid:rgba(0,0,0,0.1);
    }
    * { box-sizing:border-box; }
    body {
      margin:0;
      min-height:100vh;
      display:flex;
      align-items:center;
      justify-content:center;
      padding:2rem;
      background:var(--bg);
      color:var(--ink);
      font-family:'Space Mono','JetBrains Mono',monospace;
    }
    body::before {
      content:'';
      position:fixed;
      inset:0;
      background:
        linear-gradient(90deg, rgba(0,0,0,.02) 1px, transparent 1px),
        linear-gradient(rgba(0,0,0,.02) 1px, transparent 1px);
      background-size:12px 12px;
      pointer-events:none;
    }
    #app {
      width:min(1180px, 96vw);
      background:var(--panel);
      border:3px solid var(--ink);
      padding:2rem;
      position:relative;
      image-rendering:pixelated;
      box-shadow:12px 12px 0 rgba(0,0,0,.15);
      display:flex;
      flex-direction:column;
      gap:2rem;
    }
    #app::after {
      content:'';
      position:absolute;
      inset:8px;
      border:1px dashed rgba(0,0,0,.15);
      pointer-events:none;
    }
    header { display:flex; justify-content:space-between; align-items:flex-start; gap:1rem; }
    .eyebrow {
      font-family:'Press Start 2P','Space Mono',monospace;
      font-size:.55rem;
      text-transform:uppercase;
      letter-spacing:.2em;
      margin:0;
    }
    .status {
      font-size:.65rem;
      text-transform:uppercase;
      letter-spacing:.1em;
      border:2px solid var(--ink);
      padding:.4rem .9rem;
      background:#ffffff;
      box-shadow:4px 4px 0 rgba(0,0,0,.15);
    }
    .pair-grid {
      display:grid;
      grid-template-columns:repeat(auto-fit, minmax(320px, 1fr));
      gap:1.5rem;
    }
    .overview { display:flex; flex-direction:column; gap:1rem; }
    .global-chart {
      width:100%;
      border:2px solid var(--ink);
      background:#fff;
      image-rendering:pixelated;
    }
    .pair-card {
      border:3px solid var(--ink);
      padding:1.5rem;
      background:#fff;
      box-shadow:8px 8px 0 rgba(0,0,0,.15);
      display:flex;
      flex-direction:column;
      gap:1rem;
    }
    .pair-card-header {
      display:flex;
      justify-content:space-between;
      align-items:center;
      gap:.5rem;
    }
    .pair-card-labels {
      display:flex;
      align-items:center;
      gap:.6rem;
      flex-wrap:wrap;
    }
    .pair-name {
      font-family:'Press Start 2P','Space Mono',monospace;
      font-size:.9rem;
      letter-spacing:.08em;
      margin:0;
    }
    .equity {
      border:3px solid var(--ink);
      padding:1.2rem;
      background:#fff;
      box-shadow:6px 6px 0 rgba(0,0,0,.12);
    }
    .equity .label {
      font-size:.62rem;
      text-transform:uppercase;
      letter-spacing:.2em;
      color:var(--ink-mid);
    }
    .equity .value {
      margin-top:.8rem;
      font-size:1.8rem;
      font-weight:700;
      letter-spacing:.08em;
      color:var(--ink);
      text-transform:uppercase;
    }
    .meta {
      display:flex;
      flex-wrap:wrap;
      gap:.5rem;
      margin-top:1rem;
    }
    .pill {
      font-size:.55rem;
      letter-spacing:.12em;
      text-transform:uppercase;
      padding:.35rem .7rem;
      border:2px solid var(--ink);
      background:#fefefe;
      color:var(--ink);
      box-shadow:4px 4px 0 rgba(0,0,0,.15);
    }
    .pill.muted {
      color:var(--ink-mid);
      border-color:var(--ink-mid);
    }
    .empty-state {
      border:2px dashed var(--ink-soft);
      padding:2rem;
      text-align:center;
      font-size:.8rem;
      letter-spacing:.12em;
      text-transform:uppercase;
      color:var(--ink-mid);
    }
    @media (max-width:640px) {
      body { padding:1rem; }
      #app { padding:1.2rem; }
      header { flex-direction:column; align-items:flex-start; }
      .pair-grid { grid-template-columns:1fr; }
    }
  </style>
</head>
<body>
  <div id="app">
    <header>
      <div>
        <p class="eyebrow">marti dashboard</p>
      </div>
      <div id="sse-status" class="status">Connecting…</div>
    </header>
    <section class="overview">
      <canvas id="globalChart" class="global-chart" height="320"></canvas>
    </section>
    <section id="pairs" class="pair-grid">
      <div id="emptyState" class="empty-state">Waiting for balance snapshots…</div>
    </section>
  </div>
<script>
const statusEl = document.getElementById('sse-status');
const pairContainer = document.getElementById('pairs');
const emptyState = document.getElementById('emptyState');
const chartCanvas = document.getElementById('globalChart');
const pairViews = new Map();
const datasetByPair = new Map();
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

const seriesLabel = (pair, model) => {
  const safePair = pair || '—';
  const safeModel = normalizeModel(model);
  return safeModel ? safePair + ' · ' + safeModel : safePair;
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

function ensureDataset(pair, model){
  const key = seriesLabel(pair, model);
  if(datasetByPair.has(key)){
    return datasetByPair.get(key);
  }
  const palette = nextColor();
  const dataset = {
    label: key,
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
  if(globalChart.data.labels.length > 600){
    globalChart.data.labels.shift();
    globalChart.data.datasets.forEach((dataset) => {
      dataset.data.shift();
    });
  }
}

function updateGlobalChart(pair, model, total, ts){
  const tsDate = ts ? new Date(ts) : new Date();
  const labelDate = Number.isNaN(tsDate.getTime()) ? new Date() : tsDate;
  const tickLabel = labelDate.toLocaleTimeString([], { hour12:false });
  appendGlobalLabel(tickLabel);
  const dataset = ensureDataset(pair, model);
  dataset.data[dataset.data.length - 1] = total.numeric;
  globalChart.update('none');
}

function ensurePairView(pair, model){
  const safePair = pair || '—';
  const safeModel = normalizeModel(model);
  const viewKey = seriesLabel(safePair, safeModel);
  if(pairViews.has(viewKey)){
    return pairViews.get(viewKey);
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
  title.textContent = safePair;
  const modelBadge = document.createElement('span');
  modelBadge.className = safeModel ? 'pill' : 'pill muted';
  modelBadge.textContent = 'Model ' + (safeModel || '—');
  const updated = document.createElement('span');
  updated.className = 'pill muted';
  updated.textContent = 'Waiting…';
  labelsWrap.append(title, modelBadge);
  header.append(labelsWrap, updated);

  const equity = document.createElement('div');
  equity.className = 'equity';
  const label = document.createElement('div');
  label.className = 'label';
  label.textContent = 'Total funds';
  const totalValue = document.createElement('div');
  totalValue.className = 'value';
  totalValue.textContent = '0';
  const meta = document.createElement('div');
  meta.className = 'meta';
  const baseEl = document.createElement('span');
  baseEl.className = 'pill';
  baseEl.textContent = 'Base —';
  const quoteEl = document.createElement('span');
  quoteEl.className = 'pill';
  quoteEl.textContent = 'Quote —';
  const priceEl = document.createElement('span');
  priceEl.className = 'pill muted';
  priceEl.textContent = 'Price —';
  const positionEl = document.createElement('span');
  positionEl.className = 'pill';
  positionEl.style.display = 'none';
  meta.append(baseEl, quoteEl, priceEl, positionEl);
  equity.append(label, totalValue, meta);

  card.append(header, equity);
  pairContainer.appendChild(card);

  const view = {
    totalEl: totalValue,
    updatedEl: updated,
    baseEl,
    quoteEl,
    priceEl,
    positionEl,
  };
  pairViews.set(viewKey, view);
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

function renderNumbers(view, payload, total){
  view.totalEl.textContent = total.display;
  view.baseEl.textContent = payload.base ? 'Base ' + payload.base : 'Base —';
  view.quoteEl.textContent = payload.quote ? 'Quote ' + payload.quote : 'Quote —';
  view.priceEl.textContent = payload.price ? 'Price ' + payload.price : 'Price —';
  view.updatedEl.textContent = formatTs(payload.ts);
  if(view.positionEl){
    const position = typeof payload.position === 'string' ? payload.position.trim() : '';
    if(position){
      view.positionEl.textContent = position;
      view.positionEl.style.display = '';
    }else{
      view.positionEl.textContent = '';
      view.positionEl.style.display = 'none';
    }
  }
}

function handlePayload(payload){
  const pairLabel = payload.pair || '—';
  const view = ensurePairView(pairLabel, payload.model);
  const total = deriveTotal(payload);
  renderNumbers(view, payload, total);
  updateGlobalChart(pairLabel, payload.model, total, payload.ts);
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
</script>
</body>
</html>`
