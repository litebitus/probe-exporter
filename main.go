package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/litebitus/probe-exporter/exporterapp"
	"github.com/litebitus/probe-exporter/rdapprober"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	reloadSignals := make(chan os.Signal, 1)
	signal.Notify(reloadSignals, syscall.SIGHUP)
	err := exporterapp.Run(ctx, os.Args[1:], exporterapp.ProgramSettings{
		Version:          version,
		Output:           os.Stderr,
		ReloadSignals:    reloadSignals,
		RDAPBootstrapURL: rdapprober.IANABootstrapURL,
		HTTPClient:       http.DefaultClient,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
