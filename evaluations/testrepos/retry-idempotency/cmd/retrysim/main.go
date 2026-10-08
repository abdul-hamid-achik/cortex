// Command retrysim replays a scripted server against the retry wrapper and
// prints every attempt. No network is involved and no real time passes.
//
// The script is a comma-separated list of outcomes: a status code, optionally
// followed by "/" and the Retry-After header value (503/3), or "err" for a
// transport failure.
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"example.com/retrysim/internal/faketransport"
	"example.com/retrysim/internal/retry"
)

func run(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("retrysim", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	method := fs.String("method", "GET", "HTTP method of the request")
	script := fs.String("script", "200", "scripted server outcomes")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(out, "usage: retrysim [-method M] [-script S]")
		return 2
	}
	steps, err := faketransport.Parse(*script)
	if err != nil {
		fmt.Fprintln(out, err)
		return 2
	}
	req, err := http.NewRequest(*method, "http://example.invalid/resource", nil)
	if err != nil {
		fmt.Fprintln(out, err)
		return 2
	}
	tr := faketransport.New(steps...)
	sleep := func(d time.Duration) { fmt.Fprintf(out, "wait %dms\n", d.Milliseconds()) }
	res := retry.Do(tr, req, retry.DefaultPolicy(), sleep)
	final := "error"
	if res.Err == nil {
		final = fmt.Sprint(res.Response.StatusCode)
	}
	fmt.Fprintf(out, "final: %s attempts=%d waited=%dms\n", final, res.Attempts, res.Waited.Milliseconds())
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}
