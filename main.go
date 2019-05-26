package main

import (
	"os"

	"github.com/forter/guarddutybeat/cmd"

	_ "github.com/forter/guarddutybeat/include"
)

func main() {
	if err := cmd.RootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
