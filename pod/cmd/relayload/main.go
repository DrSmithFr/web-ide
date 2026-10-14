// relayload drives a relay with synthetic traffic (#72): the tiers, the ramp and the report of
// pod/internal/relayload, on the command line.
//
//	relayload -relay https://antennae.space -tier signal -pods 20 -sessions 100
//	relayload -relay https://antennae.space -tier turn -pods 20 -sessions 200 -mb 8 -force-turn
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/relayload"
)

func main() {
	relay := flag.String("relay", "http://127.0.0.1:4430", "the relay to drive (http:// or https://)")
	tier := flag.String("tier", "signal", "static, signal or turn")
	pods := flag.Int("pods", 4, "pod links to register")
	sessions := flag.Int("sessions", 10, "sessions to bring up and hold")
	mb := flag.Int("mb", 4, "MB to pump through each tunnel (turn tier)")
	ramp := flag.Duration("ramp", 5*time.Second, "spreading the sessions up over this window")
	hold := flag.Duration("hold", 10*time.Second, "keeping them open this long once up")
	workers := flag.Int("workers", 8, "static tier: concurrent requests")
	forceTurn := flag.Bool("force-turn", false, "turn tier: relay candidates only")
	insecure := flag.Bool("insecure", false, "accept the self-signed certificate of a test relay")
	timeout := flag.Duration("timeout", 30*time.Second, "a session not up within this is a failure")
	jsonOut := flag.Bool("json", false, "the report as JSON")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rep, err := relayload.Run(ctx, relayload.Config{
		RelayURL:  strings.TrimSuffix(*relay, "/"),
		Tier:      *tier,
		Pods:      *pods,
		Sessions:  *sessions,
		MB:        *mb,
		Ramp:      *ramp,
		Hold:      *hold,
		Workers:   *workers,
		ForceTurn: *forceTurn,
		Timeout:   *timeout,
		Insecure:  *insecure,
	})
	if rep != nil && *jsonOut {
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(b))
	} else if rep != nil {
		fmt.Print(rep.String())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "relayload:", err)
		os.Exit(1)
	}
}
