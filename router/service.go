package main

import (
	"path/filepath"
	"slices"
)

// Matching

const routerBinary = "pc-pult-router"

var toolFlags = []string{"-setup", "-pair", "-check", "-version", "-set-token"}

func matchesService(args []string, cfgPath string) bool {
	if len(args) < 2 || filepath.Base(args[0]) != routerBinary {
		return false
	}
	for _, a := range args[1:] {
		if slices.Contains(toolFlags, a) {
			return false
		}
	}
	for i, a := range args[1:] {
		if (a == "-config" || a == "--config") && i+2 < len(args) && args[i+2] == cfgPath {
			return true
		}
		if a == "-config="+cfgPath || a == "--config="+cfgPath {
			return true
		}
	}
	return false
}
