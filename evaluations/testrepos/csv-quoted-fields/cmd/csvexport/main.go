// Command csvexport prints its arguments as one comma-separated record.
package main

import (
	"fmt"
	"io"
	"os"

	"example.com/csvexport/internal/csvout"
)

func run(args []string, out io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(out, "usage: csvexport FIELD...")
		return 2
	}
	fmt.Fprintln(out, csvout.Record(args))
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}
