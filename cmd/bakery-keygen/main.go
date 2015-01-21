package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"gopkg.in/macaroon-bakery.v0/bakery"
)

var format = flag.String("f", "json", "output format (json, go)")

func main() {
	flag.Parse()
	kp, err := bakery.GenerateKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot generate key: %s\n", err)
		os.Exit(1)
	}
	switch *format {
	case "json":
		b, err := json.MarshalIndent(kp, "", "  ")
		if err != nil {
			panic(err)
		}
		fmt.Printf("%s\n", b)
	case "go":
		fmt.Printf("%#v\n", *kp)
	default:
		fmt.Fprintf(os.Stderr, "unsupported format: %s\n", format)
		os.Exit(1)
	}
}
