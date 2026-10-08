// Command fetchd prints the effective upstream settings from a config file.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"example.com/fetchd/internal/client"
	"example.com/fetchd/internal/config"
)

func run(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("fetchd", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("config", "", "path to the config file")
	if err := fs.Parse(args); err != nil || *path == "" {
		fmt.Fprintln(out, "usage: fetchd -config FILE")
		return 2
	}
	f, err := os.Open(*path)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	defer f.Close()
	cfg, err := config.Load(f)
	if err != nil {
		fmt.Fprintln(out, "error:", err)
		return 1
	}
	c := client.New(cfg)
	fmt.Fprintf(out, "addr: %s\n", cfg.Addr)
	fmt.Fprintf(out, "timeout: %s\n", cfg.Timeout)
	fmt.Fprintf(out, "attempts: %d\n", c.Attempts())
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}
