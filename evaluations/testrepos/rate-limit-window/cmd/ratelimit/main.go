// Command ratelimit replays a sequence of request timestamps against a sliding
// window limiter and prints each decision.
//
// Each positional argument is a millisecond timestamp. A bare number records a
// request; a number prefixed with "?" only asks how much capacity is left.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"example.com/ratelimit/internal/clock"
	"example.com/ratelimit/internal/limit"
)

func run(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("ratelimit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	max := fs.Int("limit", 3, "requests allowed per window")
	window := fs.Int64("window", 1000, "window length in milliseconds")
	if err := fs.Parse(args); err != nil || *max < 1 || *window < 1 {
		fmt.Fprintln(out, "usage: ratelimit [-limit N] [-window MS] TIMESTAMP|?TIMESTAMP...")
		return 2
	}
	l := limit.New(*max, clock.Millis(*window))
	for _, arg := range fs.Args() {
		query := strings.HasPrefix(arg, "?")
		n, err := strconv.ParseInt(strings.TrimPrefix(arg, "?"), 10, 64)
		if err != nil || n < 0 {
			fmt.Fprintf(out, "bad timestamp %q\n", arg)
			return 2
		}
		now := clock.Millis(n)
		if query {
			fmt.Fprintf(out, "t=%d remaining=%d\n", n, l.Remaining(now))
			continue
		}
		if l.Allow(now) {
			fmt.Fprintf(out, "t=%d allow remaining=%d\n", n, l.Remaining(now))
		} else {
			fmt.Fprintf(out, "t=%d deny retry-after=%d\n", n, l.RetryAfter(now))
		}
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}
