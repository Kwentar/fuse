package main

import (
	"fuse/internal/cli"
	"os"
)

func main() { os.Exit(cli.Main(os.Args[1:])) }
