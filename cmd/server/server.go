package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"strings"

	"github.com/Shopify/toxiproxy/v2"
	"github.com/Shopify/toxiproxy/v2/collectors"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

type cliArguments struct {
	host           string
	port           string
	config         string
	seed           int64
	printVersion   bool
	proxyMetrics   bool
	runtimeMetrics bool
}

func parseArguments() cliArguments {
	result := cliArguments{}
	flag.StringVar(&result.host, "host", "localhost",
		"Host for toxiproxy's API to listen on")
	flag.StringVar(&result.port, "port", "8474",
		"Port for toxiproxy's API to listen on")
	flag.StringVar(&result.config, "config", "",
		"JSON file containing proxies to create on startup")
	flag.Int64Var(&result.seed, "seed", time.Now().UTC().UnixNano(),
		"Seed for randomizing toxics with")
	flag.BoolVar(&result.runtimeMetrics, "runtime-metrics", false,
		`enable runtime-related prometheus metrics (default "false")`)
	flag.BoolVar(&result.proxyMetrics, "proxy-metrics", false,
		`enable toxiproxy-specific prometheus metrics (default "false")`)
	flag.BoolVar(&result.printVersion, "version", false,
		`print the version (default "false")`)
	flag.Parse()

	return result
}

func main() {
	err := run()
	if err != nil {
		fmt.Printf("error: %v", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func run() error {
	cli := parseArguments()

	if cli.printVersion {
		fmt.Printf("toxiproxy-server version %s\n", toxiproxy.Version)
		return nil
	}

	rand.New(rand.NewSource(cli.seed)) // #nosec G404 -- ignoring this rule

	logger := setupLogger()
	log.Logger = logger

	logger.
		Info().
		Str("version", toxiproxy.Version).
		Msg("Starting Toxiproxy")

	metrics := toxiproxy.NewMetricsContainer(prometheus.NewRegistry())
	server := toxiproxy.NewServer(metrics, logger)
	if cli.proxyMetrics {
		server.Metrics.ProxyMetrics = collectors.NewProxyMetricCollectors()
	}
	if cli.runtimeMetrics {
		server.Metrics.RuntimeMetrics = collectors.NewRuntimeMetricCollectors()
	}

	if len(cli.config) > 0 {
		var config io.Reader
		var s3session *s3.S3
		if strings.Contains(cli.config, "s3://") {
			s3session = setupAwsSession()
		}

		go func() {
			for {
				//This needs to be done a lot better
				if strings.Contains(cli.config, "s3://") {
					parts := strings.Split(cli.config, "s3://")
					bk := strings.Split(parts[1], "/")
					input := &s3.GetObjectInput{
						Bucket: aws.String(bk[0]),
						Key:    aws.String(bk[1]),
					}
					result, _ := s3session.GetObject(input)
					config = result.Body
				} else {
					logger := server.Logger
					file, err := os.Open(cli.config)
					config = bufio.NewReader(file)
					defer file.Close()
					if err != nil {
						logger.Err(err).Str("config", cli.config).Msg("Error reading config file")
					}
				}
				time.Sleep(1 * time.Second)
				server.PopulateConfig(config)

			}
		}()
	}

	addr := net.JoinHostPort(cli.host, cli.port)
	go func(server *toxiproxy.ApiServer, addr string) {
		err := server.Listen(addr)
		if err != nil {
			server.Logger.Err(err).Msg("Server finished with error")
		}
	}(server, addr)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals
	server.Logger.Info().Msg("Shutdown started")
	err := server.Shutdown()
	if err != nil {
		logger.Err(err).Msg("Shutdown finished with error")
	}
	return nil
}

func setupAwsSession() *s3.S3 {
	sess, _ := session.NewSession()
	svc := s3.New(sess)
	return svc
}
func setupLogger() zerolog.Logger {
	zerolog.TimestampFunc = func() time.Time {
		return time.Now().UTC()
	}

	zerolog.CallerMarshalFunc = func(pc uintptr, file string, line int) string {
		short := file
		for i := len(file) - 1; i > 0; i-- {
			if file[i] == '/' {
				short = file[i+1:]
				break
			}
		}
		file = short
		return file + ":" + strconv.Itoa(line)
	}

	logger := zerolog.New(os.Stdout).With().Caller().Timestamp().Logger()

	val, ok := os.LookupEnv("LOG_LEVEL")
	if !ok {
		return logger
	}

	lvl, err := zerolog.ParseLevel(val)
	if err == nil {
		logger = logger.Level(lvl)
	} else {
		l := &logger
		l.Err(err).Msgf("unknown LOG_LEVEL value: \"%s\"", val)
	}

	return logger
}
