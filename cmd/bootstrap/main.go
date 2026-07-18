package main

import (
	"fmt"
	"os"
	"strings"

	"trading-go/internal/config"
	"trading-go/internal/database"
)

func main() {
	cfg, err := config.LoadValidated()
	if err != nil {
		fatal(err)
	}
	passwords := map[string]string{}
	for _, role := range []string{"runtime", "ledger", "parity"} {
		path := strings.TrimSpace(os.Getenv("BOOTSTRAP_" + strings.ToUpper(role) + "_PASSWORD_FILE"))
		if path == "" {
			fatal(fmt.Errorf("BOOTSTRAP_%s_PASSWORD_FILE is required", strings.ToUpper(role)))
		}
		payload, readErr := os.ReadFile(path)
		if readErr != nil {
			fatal(readErr)
		}
		passwords[role] = strings.TrimSpace(string(payload))
		if passwords[role] == "" {
			fatal(fmt.Errorf("%s password file is empty", role))
		}
	}
	if err := database.BootstrapFreshDatabase(cfg, passwords); err != nil {
		fatal(err)
	}
}

func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
