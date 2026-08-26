package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zouyi/eco-guardian/internal/packageinfo"
)

func main() {
	output := flag.String("out", "", "generated schema output path")
	flag.Parse()
	if *output == "" {
		fmt.Fprintln(os.Stderr, "-out is required")
		os.Exit(2)
	}
	data, err := json.MarshalIndent(packageinfo.SchemaDocument(), "", "  ")
	if err == nil {
		err = os.MkdirAll(filepath.Dir(*output), 0o755)
	}
	if err == nil {
		err = os.WriteFile(*output, append(data, '\n'), 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
