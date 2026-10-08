// Command slug prints the public path of a blog post from its title.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"example.com/blogtool/internal/post"
)

func run(args []string, out io.Writer) int {
	title := strings.TrimSpace(strings.Join(args, " "))
	if title == "" {
		fmt.Fprintln(out, "usage: slug TITLE...")
		return 2
	}
	fmt.Fprintln(out, post.Permalink(title))
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}
