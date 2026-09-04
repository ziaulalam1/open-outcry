// Command swarm drives N real WebSocket connections at the open-outcry server.
//
// It is three things at once, and the third is the one that justifies it:
//
//  1. a rehearsal surface — the room, on demand, at 2am, without thirty people;
//  2. a load test — including one client that never reads its socket, which is
//     the only honest way to make the drop counter move before a real phone does;
//  3. a stage instrument — paired with the projector's room grid, it separates
//     "the room stopped trading" from "delivery broke", by showing cells still
//     firing while the degraded pane has gone quiet.
//
// It speaks the wire protocol over a real socket. It has no access to the engine
// and no special privileges: it is indistinguishable from thirty phones.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	cfg := Config{}
	flag.StringVar(&cfg.Addr, "addr", "localhost:8080", "server host:port")
	flag.IntVar(&cfg.Clients, "n", 24, "number of simulated attendees")
	flag.IntVar(&cfg.Blackholes, "blackhole", 1, "how many clients never read their socket")
	flag.Float64Var(&cfg.Rate, "rate", 0.7, "orders per second, per client")
	flag.DurationVar(&cfg.Duration, "dur", 60*time.Second, "how long to run")
	flag.Int64Var(&cfg.Seed, "seed", 1, "random seed; the same seed replays the same room")
	flag.Int64Var(&cfg.Mid, "mid", 10250, "reference price in cents")
	flag.Parse()

	if cfg.Clients <= 0 {
		fmt.Fprintln(os.Stderr, "swarm: -n must be positive")
		os.Exit(2)
	}
	if cfg.Blackholes > cfg.Clients {
		cfg.Blackholes = cfg.Clients
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("swarm: %d clients -> %s for %s (%d blackholed, seed %d)\n",
		cfg.Clients, cfg.Addr, cfg.Duration, cfg.Blackholes, cfg.Seed)
	if cfg.Blackholes > 0 && cfg.Rate < 20 &&
		(strings.HasPrefix(cfg.Addr, "127.") || strings.HasPrefix(cfg.Addr, "localhost")) {
		fmt.Println("swarm: NOTE - on localhost at this rate the projector's \"dropped - slow phone\"")
		fmt.Println("       counter will read 0. It is not broken: the kernel auto-tunes loopback")
		fmt.Println("       buffers, so a non-reading client absorbs megabytes before anything")
		fmt.Println("       backs up. Measured: 0 drops at -rate 6, ~1340 at -rate 30 over 45s.")
		fmt.Println("       For rehearsal use -rate 30 -dur 45s. On venue wifi it moves on its own.")
	}

	// The observer connects BEFORE the load starts, so its first sample is a
	// baseline taken while the counters are still whatever the server was
	// sitting at. Every number in the summary is then a delta over this run
	// rather than a cumulative total from an arbitrary start point — which is
	// the confounding that made the first version of the rcvbuf measurement
	// produce a convincing dose-response curve out of nothing.
	obs := StartObserver(ctx, cfg.Addr)

	start := time.Now()
	stats := RunSwarm(ctx, cfg)
	elapsed := time.Since(start)

	obs.Stop()
	report(stats, elapsed, obs.Counters())
}

func report(stats []Stats, elapsed time.Duration, sc ServerCounters) {
	byProfile := map[Profile]*Stats{}
	totalSent, totalRead, totalErr, totalClosed := 0, 0, 0, 0

	for _, s := range stats {
		agg, ok := byProfile[s.Profile]
		if !ok {
			agg = &Stats{Profile: s.Profile}
			byProfile[s.Profile] = agg
		}
		agg.Sent += s.Sent
		agg.Read += s.Read
		agg.Errors += s.Errors
		agg.Closed += s.Closed
		agg.Client++ // reused as a count of clients in this profile
		totalSent += s.Sent
		totalRead += s.Read
		totalErr += s.Errors
		totalClosed += s.Closed
	}

	fmt.Printf("\nran %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("%-12s %7s %9s %10s %7s %7s\n",
		"profile", "clients", "sent", "read", "errors", "closed")
	for _, p := range []Profile{Rester, Crosser, Canceller, Blackhole} {
		a, ok := byProfile[p]
		if !ok {
			continue
		}
		fmt.Printf("%-12s %7d %9d %10d %7d %7d\n", p, a.Client, a.Sent, a.Read, a.Errors, a.Closed)
	}
	fmt.Printf("%-12s %7d %9d %10d %7d %7d\n",
		"total", len(stats), totalSent, totalRead, totalErr, totalClosed)

	// "closed" is the server hanging up, which is not a malfunction and is
	// expected exactly once per blackhole. Spell that out here so the column
	// does not need interpreting at 2am.
	if bh, ok := byProfile[Blackhole]; ok && bh.Closed > 0 {
		fmt.Printf("\n%d blackhole disconnect(s): the server timed them out because they never\n"+
			"pong (pongWait). That is the design working, not an error.\n", bh.Closed)
	}
	if totalErr > 0 {
		fmt.Printf("\nWARNING: %d genuine error(s). A clean run reports zero in this column.\n", totalErr)
	}

	// The line the whole blackhole profile exists for.
	if a, ok := byProfile[Blackhole]; ok && a.Read != 0 {
		fmt.Printf("\nWARNING: blackholed clients read %d frames; they are supposed to read none.\n", a.Read)
	}

	reportServerCounters(byProfile, sc)
}

// reportServerCounters prints what the server measured, or says plainly that
// nothing was measured. It never prints an unobserved zero, because a zero that
// means "not measured" and a zero that means "nothing was dropped" are opposite
// findings and look identical.
func reportServerCounters(byProfile map[Profile]*Stats, sc ServerCounters) {
	fmt.Printf("\nserver counters, read off the stats feed over this run:\n")
	if !sc.Observed {
		fmt.Printf("  NOT OBSERVED — the observer connection failed. No claim is made about\n" +
			"  what was dropped. This is not the same as zero.\n")
		return
	}

	bp, cd := sc.Backpressure(), sc.ChaosDropped()
	fmt.Printf("  %-24s %10d   (projector: \"dropped · slow phone\")\n", "backpressure", bp)
	fmt.Printf("  %-24s %10d   (projector: \"dropped · chaos\")\n", "chaos dropped", cd)
	fmt.Printf("  %-24s %10d   %d stats samples\n", "engine seq", sc.EngineSeq, sc.Samples)
	if sc.Split {
		fmt.Printf("  %-24s %10s   chaos armed, delay %dms\n", "split", "true", sc.ChaosDelayMS)
	} else {
		fmt.Printf("  %-24s %10s   chaos NOT armed — no degraded pane\n", "split", "false")
	}

	bh, hasBlackhole := byProfile[Blackhole]
	if !hasBlackhole || bh.Client == 0 {
		return
	}

	if bp > 0 {
		fmt.Printf("\nthe blackholed client's send buffers DID overflow: %d frames dropped and\n"+
			"counted. This is the number act two points at.\n", bp)
		return
	}

	// The known trap, stated as a measurement rather than a warning to ignore.
	fmt.Printf("\nbackpressure is 0 despite %d blackholed client(s). On loopback this is the\n"+
		"expected result at low throughput, not a fault: the kernel auto-tunes loopback\n"+
		"socket buffers into the megabytes, so a client that has stopped reading absorbs\n"+
		"an enormous amount before anything backs up.\n"+
		"Measured on this codebase, one fresh server per condition: 0 drops at -rate 6,\n"+
		"~1350-1390 at -rate 30 over 45s. Raise -rate; there is no socket-buffer flag,\n"+
		"and one was built, measured and deleted because it made no difference.\n"+
		"See docs/build-log.md entry 14 and docs/RUNBOOK.md section 5.\n", bh.Client)
}
