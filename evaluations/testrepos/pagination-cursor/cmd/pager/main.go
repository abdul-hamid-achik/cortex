// Command pager walks a generated list page by page and prints each page.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"example.com/pager/internal/page"
)

func run(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("pager", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	total := fs.Int("total", 10, "number of generated items")
	size := fs.Int("size", page.DefaultSize, "items per page")
	if err := fs.Parse(args); err != nil || *total < 0 {
		fmt.Fprintln(out, "usage: pager [-total N] [-size N]")
		return 2
	}
	items := make([]string, *total)
	for i := range items {
		items[i] = fmt.Sprintf("item-%d", i+1)
	}
	pages := page.Walk(items, *size)
	for i, p := range pages {
		if len(p.Items) == 0 && *total > 0 {
			fmt.Fprintf(out, "page %d: (empty)\n", i+1)
			continue
		}
		if len(p.Items) == 0 {
			continue
		}
		fmt.Fprintf(out, "page %d: %s\n", i+1, strings.Join(p.Items, " "))
	}
	fmt.Fprintf(out, "pages: %d\n", countNonEmpty(pages))
	return 0
}

func countNonEmpty(pages []page.Page) int {
	n := 0
	for _, p := range pages {
		if len(p.Items) > 0 {
			n++
		}
	}
	return n
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}
