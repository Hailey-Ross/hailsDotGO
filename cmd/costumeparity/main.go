// Command costumeparity dumps the Go costume resolver's answers so that
// scripts/check-costume-parity.mjs can compare them against the TypeScript resolver.
//
// It reads a JSON array of {dex, species, label} on stdin and writes a JSON array of sprite
// URLs ("" for no match) on stdout. Not used at runtime.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"pogo.hails.cc/internal/costumes"
)

func main() {
	var cases []struct {
		Dex     int    `json:"dex"`
		Species string `json:"species"`
		Label   string `json:"label"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&cases); err != nil {
		fmt.Fprintln(os.Stderr, "read cases:", err)
		os.Exit(1)
	}

	out := make([]string, len(cases))
	for i, c := range cases {
		if url, ok := costumes.SpriteURL(c.Dex, c.Species, c.Label); ok {
			out[i] = url
		}
	}
	json.NewEncoder(os.Stdout).Encode(out)
}
