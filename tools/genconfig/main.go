// Command genconfig writes a complete config.json — every engine option
// with its default value — so there is one file to look at and edit.
// config.Load() only needs the keys you want to change (anything absent
// uses the built-in default), but a full file is easier to work from.
//
//	go run ./tools/genconfig            # write ./config.json (won't overwrite)
//	go run ./tools/genconfig -f         # overwrite ./config.json
//	go run ./tools/genconfig -o path    # write somewhere else
//	go run ./tools/genconfig -stdout    # just print it
//
// See game_design.txt section 17 for what each key does.
package main

import (
	"flag"
	"fmt"
	"os"

	"twopointfive/config"
)

func main() {
	out := flag.String("o", "config.json", "path to write")
	force := flag.Bool("f", false, "overwrite an existing file")
	toStdout := flag.Bool("stdout", false, "print to stdout instead of writing a file")
	flag.Parse()

	data, err := config.DefaultJSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, "genconfig:", err)
		os.Exit(1)
	}

	if *toStdout {
		os.Stdout.Write(data)
		return
	}

	if !*force {
		if _, err := os.Stat(*out); err == nil {
			fmt.Fprintf(os.Stderr, "genconfig: %s already exists — pass -f to overwrite, or -stdout to just see it\n", *out)
			os.Exit(1)
		}
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "genconfig:", err)
		os.Exit(1)
	}
	fmt.Printf("genconfig: wrote %s\n", *out)
}
