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

// Minimal index page with a single total-balance chart + SSE.
const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <title>Marti Total Balance</title>
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
      width:min(960px, 96vw);
      background:var(--panel);
      border:3px solid var(--ink);
      padding:2rem;
      position:relative;
      image-rendering:pixelated;
      box-shadow:12px 12px 0 rgba(0,0,0,.15);
      display:flex;
      flex-direction:column;
      gap:1.5rem;
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
    h1 {
      font-family:'Press Start 2P','Space Mono',monospace;
      font-size:1.15rem;
      letter-spacing:.1em;
      margin:.8rem 0 0;
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
    .chart-panel {
      border:3px solid var(--ink);
      padding:1.5rem;
      background:#fff;
      box-shadow:8px 8px 0 rgba(0,0,0,.15);
      display:flex;
      flex-direction:column;
      gap:1.2rem;
    }
    .equity {
      border:3px solid var(--ink);
      padding:1.5rem;
      background:#fff;
      box-shadow:6px 6px 0 rgba(0,0,0,.12);
    }
    .equity .label {
      font-size:.68rem;
      text-transform:uppercase;
      letter-spacing:.2em;
      color:var(--ink-mid);
    }
    .equity .value {
      margin-top:.8rem;
      font-size:2.4rem;
      font-weight:700;
      letter-spacing:.08em;
      color:var(--ink);
      text-transform:uppercase;
    }
    .meta {
      display:flex;
      flex-wrap:wrap;
      gap:.5rem;
      margin-top:1.2rem;
    }
    .pill {
      font-size:.6rem;
      letter-spacing:.12em;
      text-transform:uppercase;
      padding:.4rem .75rem;
      border:2px solid var(--ink);
      background:#fefefe;
      color:var(--ink);
      box-shadow:4px 4px 0 rgba(0,0,0,.15);
    }
    .pill.muted {
      color:var(--ink-mid);
      border-color:var(--ink-mid);
    }
    canvas {
      width:100%;
      border:2px solid var(--ink);
      background:#fff;
      image-rendering:pixelated;
    }
    @media (max-width:640px) {
      body { padding:1rem; }
      #app { padding:1.2rem; }
      header { flex-direction:column; align-items:flex-start; }
      .equity .value { font-size:1.8rem; }
    }
  </style>
</head>
<body>
  <div id="app">
    <section class="chart-panel">
      <header>
        <div>
          <p class="eyebrow">marti dashboard</p>
          <h1>Total Balance</h1>
        </div>
        <div id="sse-status" class="status">Connecting…</div>
      </header>
      <canvas id="balanceChart" height="320"></canvas>
    </section>
    <section class="equity">
      <div class="label">Total funds</div>
      <div id="totalQuote" class="value">0</div>
      <div class="meta">
        <span id="pair" class="pill">—</span>
        <span id="price" class="pill muted">Price —</span>
        <span id="updated" class="pill muted">Waiting…</span>
      </div>
    </section>
  </div>
<script>
const totalEl = document.getElementById('totalQuote');
const pairEl = document.getElementById('pair');
const priceEl = document.getElementById('price');
const updatedEl = document.getElementById('updated');
const statusEl = document.getElementById('sse-status');

Chart.defaults.font.family = "'Space Mono', 'JetBrains Mono', monospace";
Chart.defaults.font.size = 11;
Chart.defaults.color = '#111111';

const ctx = document.getElementById('balanceChart').getContext('2d');
ctx.imageSmoothingEnabled = false;

const chart = new Chart(ctx, {
  type: 'line',
  data: { labels: [], datasets: [{
    label: 'Total Funds',
    data: [],
    borderColor: '#111111',
    backgroundColor: 'rgba(0,0,0,0.08)',
    borderWidth: 2,
    pointRadius: 0,
    tension: 0.15,
    fill: true
  }]},
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
      legend:{ display:false },
      tooltip:{
        backgroundColor:'#ffffff',
        borderColor:'#111111',
        borderWidth:1,
        titleColor:'#111111',
        bodyColor:'#111111',
        displayColors:false
      }
    }
  }
});

const parseNum = (value) => {
  const num = parseFloat(value);
  return Number.isFinite(num) ? num : 0;
};

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

function renderNumbers(payload, total){
  totalEl.textContent = total.display;
  pairEl.textContent = payload.pair || '—';
  priceEl.textContent = payload.price ? 'Price ' + payload.price : 'Price —';
  if(payload.ts){
    const ts = new Date(payload.ts);
    updatedEl.textContent = isNaN(ts) ? '—' : ts.toLocaleTimeString([], { hour12:false });
  } else {
    updatedEl.textContent = '—';
  }
}

function updateChart(payload, total){
  const ts = payload.ts ? new Date(payload.ts) : new Date();
  const labelSource = isNaN(ts) ? new Date() : ts;
  const label = labelSource.toLocaleTimeString([], { hour12:false });
  chart.data.labels.push(label);
  chart.data.datasets[0].data.push(total.numeric);
  if(chart.data.labels.length > 600){
    chart.data.labels.shift();
    chart.data.datasets[0].data.shift();
  }
  chart.update('none');
}

function handlePayload(payload){
  const total = deriveTotal(payload);
  renderNumbers(payload, total);
  updateChart(payload, total);
}

function connectSSE(){
  const source = new EventSource('/balance/stream');
  statusEl.textContent = 'Live: receiving data';
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
