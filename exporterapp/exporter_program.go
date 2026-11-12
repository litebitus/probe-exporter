// Package exporterapp is the exporter's program: its flags, its configuration's loading and reloading, and its HTTP endpoints.
package exporterapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/alecthomas/kingpin/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/promslog"
	promslogflag "github.com/prometheus/common/promslog/flag"
	"github.com/prometheus/exporter-toolkit/web"

	"github.com/litebitus/probe-exporter/exporterconfig"
	"github.com/litebitus/probe-exporter/proberesults"
	"github.com/litebitus/probe-exporter/probeschedule"
	"github.com/litebitus/probe-exporter/rdapprober"
)

const shutdownTimeout = 5 * time.Second

// ProgramSettings is what the program runs with beyond its command line: where it writes, and where its reload signals and RDAP bootstrap come from.
type ProgramSettings struct {
	Version          string
	Output           io.Writer
	ReloadSignals    <-chan os.Signal
	RDAPBootstrapURL string
	HTTPClient       *http.Client
}

// Run runs the exporter with its command-line arguments until the context ends.
func Run(ctx context.Context, arguments []string, settings ProgramSettings) error {
	application := kingpin.New("probe-exporter", "An OpenMetrics exporter that owns its target lists and runs probes.")
	application.Version(settings.Version)
	application.HelpFlag.Short('h')
	exitRequested := false
	application.Terminate(func(int) { exitRequested = true })
	application.UsageWriter(settings.Output)
	application.ErrorWriter(settings.Output)
	configurationFile := application.Flag("config.file", "The configuration file.").Required().String()
	listenAddresses := application.Flag("web.listen-address", "An address to listen on; repeatable.").Required().Strings()
	webConfigFile := application.Flag("web.config.file", "The exporter-toolkit web configuration, for TLS and authentication.").Default("").String()
	logConfig := &promslog.Config{Writer: settings.Output}
	promslogflag.AddFlags(application, logConfig)
	_, err := application.Parse(arguments)
	if exitRequested {
		return nil
	}
	if err != nil {
		return err
	}
	logger := promslog.New(logConfig)

	program := &exporterProgram{
		logger:            logger,
		configurationFile: *configurationFile,
		probers:           newProberSet(rdapprober.NewBootstrap(settings.RDAPBootstrapURL, settings.HTTPClient), settings.HTTPClient),
		store:             proberesults.NewResultStore(),
		ownRegistry:       prometheus.NewRegistry(),
	}
	program.schedule = probeschedule.NewSchedule(program.store, logger)
	defer program.schedule.Stop()
	program.registerOwnSeries()
	if err := program.load(); err != nil {
		return err
	}

	server := &http.Server{Handler: program.endpoints(), ReadHeaderTimeout: 10 * time.Second}
	served := make(chan error, 1)
	go func() {
		systemdSocket := false
		served <- web.ListenAndServe(server, &web.FlagConfig{
			WebListenAddresses: listenAddresses,
			WebSystemdSocket:   &systemdSocket,
			WebConfigFile:      webConfigFile,
		}, logger)
	}()
	for {
		select {
		case err := <-served:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-settings.ReloadSignals:
			if err := program.load(); err != nil {
				logger.Error("failed to reload the configuration; the running one is kept", "err", err)
			}
		case <-ctx.Done():
			shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			return server.Shutdown(shutdownContext)
		}
	}
}

type exporterProgram struct {
	logger            *slog.Logger
	configurationFile string
	probers           *proberSet
	store             *proberesults.ResultStore
	schedule          *probeschedule.Schedule
	ownRegistry       *prometheus.Registry
	reloadSuccessful  prometheus.Gauge
	reloadSucceededAt prometheus.Gauge
}

func (program *exporterProgram) registerOwnSeries() {
	program.reloadSuccessful = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "probe_exporter_config_last_reload_successful",
		Help: "Whether the last load of the configuration succeeded",
	})
	program.reloadSucceededAt = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "probe_exporter_config_last_reload_success_timestamp_seconds",
		Help: "When a configuration last loaded",
	})
	program.ownRegistry.MustRegister(
		program.reloadSuccessful,
		program.reloadSucceededAt,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
}

func (program *exporterProgram) load() error {
	configuration, err := exporterconfig.LoadFile(program.configurationFile)
	if err == nil {
		var probes []probeschedule.Probe
		probes, err = program.probers.probes(configuration)
		if err == nil {
			program.schedule.Apply(probes, configuration.MaxConcurrentProbes)
		}
	}
	if err != nil {
		program.reloadSuccessful.Set(0)
		return err
	}
	program.reloadSuccessful.Set(1)
	program.reloadSucceededAt.SetToCurrentTime()
	program.logger.Info("loaded the configuration", "file", program.configurationFile)
	return nil
}

func (program *exporterProgram) endpoints() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(prometheus.Gatherers{program.ownRegistry, program.store}, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
		ErrorHandling:     promhttp.ContinueOnError,
		ErrorLog:          slog.NewLogLogger(program.logger.Handler(), slog.LevelError),
	}))
	mux.Handle("/results", program.store.ResultsPage())
	mux.HandleFunc("/-/healthy", func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(writer, "healthy")
	})
	mux.HandleFunc("/-/ready", func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(writer, "ready")
	})
	return mux
}
