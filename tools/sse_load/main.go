package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

func main() {
	var (
		targetURL    string
		connections  int
		testDuration time.Duration
		rampUp       time.Duration
		headerAccept string
	)

	flag.StringVar(&targetURL, "url", "http://localhost:8000/balance/stream", "SSE endpoint URL")
	flag.IntVar(&connections, "conns", 1000, "number of concurrent connections to open")
	flag.DurationVar(&testDuration, "dur", 60*time.Second, "test duration (0 for until interrupted)")
	flag.DurationVar(&rampUp, "ramp", 0, "ramp-up duration (spread connection starts across this window)")
	flag.StringVar(&headerAccept, "accept", "text/event-stream", "value for Accept header")
	flag.Parse()

	if connections <= 0 {
		log.Fatalf("invalid conns: %d", connections)
	}

	if rampUp == 0 && connections > 100 {
		// default ramp-up: 1 second per 500 connections
		rampUp = time.Duration(connections/500) * time.Second
		if rampUp < 1*time.Second {
			rampUp = 1 * time.Second
		}
		log.Printf("No ramp-up specified for high connection count. Using default ramp-up: %s", rampUp)
	}

	log.Printf("starting SSE load: url=%s conns=%d duration=%s ramp=%s", targetURL, connections, testDuration, rampUp)
	runtime.GOMAXPROCS(runtime.NumCPU())

	transport := &http.Transport{
		MaxConnsPerHost:     connections + 100,
		MaxIdleConns:        connections + 100,
		MaxIdleConnsPerHost: connections + 100,
		DisableCompression:  true,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   0, // streaming
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-sigCh:
			log.Printf("caught signal: %s, shutting down...", sig)
		case <-ctx.Done():
			return
		}

		cancel()
	}()

	if testDuration > 0 {
		go func() {
			timer := time.NewTimer(testDuration)
			defer timer.Stop()
			select {
			case <-timer.C:
				log.Printf("duration reached, stopping...")
				cancel()
			case <-ctx.Done():
				return
			}
		}()
	}

	var (
		connected   int64
		connectErrs int64
		streamErrs  int64
		events      int64
	)

	var wg sync.WaitGroup

	start := time.Now()

	var interval time.Duration
	if rampUp > 0 && connections > 0 {
		interval = rampUp / time.Duration(connections)
	}

	for i := 0; i < connections; i++ {
		if ctx.Err() != nil {
			break
		}
		if i > 0 && interval > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(interval):
			}
		}
		if ctx.Err() != nil {
			break
		}

		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
			if err != nil {
				atomic.AddInt64(&connectErrs, 1)
				return
			}
			req.Header.Set("Accept", headerAccept)

			resp, err := client.Do(req)
			if err != nil {
				atomic.AddInt64(&connectErrs, 1)
				return
			}
			if resp.StatusCode != http.StatusOK {
				atomic.AddInt64(&connectErrs, 1)
				_ = resp.Body.Close()
				return
			}

			atomic.AddInt64(&connected, 1)
			reader := bufio.NewReader(resp.Body)

			for {
				select {
				case <-ctx.Done():
					_ = resp.Body.Close()
					return
				default:
					line, err := reader.ReadString('\n')
					if err != nil {
						atomic.AddInt64(&streamErrs, 1)
						_ = resp.Body.Close()
						return
					}
					// count lines that look like data/events (ignore heartbeats ":" and empty lines)
					if len(line) > 0 && line[0] != ':' && line != "\n" && line != "\r\n" {
						atomic.AddInt64(&events, 1)
					}
				}
			}
		}(i)
	}

	ticker := time.NewTicker(5 * time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				log.Printf("status: connected=%d connect_errs=%d stream_errs=%d events=%d elapsed=%s",
					atomic.LoadInt64(&connected),
					atomic.LoadInt64(&connectErrs),
					atomic.LoadInt64(&streamErrs),
					atomic.LoadInt64(&events),
					time.Since(start).Truncate(time.Second),
				)
			}
		}
	}()

	wg.Wait()
	cancel()

	elapsed := time.Since(start)
	if elapsed == 0 {
		elapsed = time.Millisecond
	}
	perSec := float64(events) / elapsed.Seconds()

	fmt.Printf("done: connected=%d connect_errs=%d stream_errs=%d events=%d elapsed=%s events/s=%.2f\n",
		atomic.LoadInt64(&connected),
		atomic.LoadInt64(&connectErrs),
		atomic.LoadInt64(&streamErrs),
		atomic.LoadInt64(&events),
		elapsed.Truncate(time.Millisecond),
		perSec,
	)
}
