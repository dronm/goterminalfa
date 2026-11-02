package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	terminalfa "github.com/dronm/goterminalfa"
	"github.com/dronm/goterminalfa/internal/config"
	"github.com/dronm/goterminalfa/internal/httpapi"
	"github.com/dronm/goterminalfa/internal/service"
	"github.com/dronm/goterminalfa/internal/store"
	"github.com/sirupsen/logrus"
)

const version = "0.6.0"

func main() {
	if err := run(); err != nil {
		logger := logrus.New()
		logger.SetFormatter(&logrus.JSONFormatter{})
		logger.WithError(err).Error("goterminalfa stopped")
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "config.json", "path to JSON configuration")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("goterminalfa " + version)
		return nil
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	logger := logrus.New()
	level, _ := logrus.ParseLevel(cfg.Logging.Level)
	logger.SetLevel(level)
	if cfg.Logging.Format == "json" {
		logger.SetFormatter(&logrus.JSONFormatter{TimestampFormat: time.RFC3339Nano})
	} else {
		logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true, DisableColors: true})
	}
	if cfg.Logging.File != "" {
		file, err := os.OpenFile(cfg.Logging.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
		defer file.Close()
		logger.SetOutput(file)
	}
	unlock, err := store.LockProcess(cfg.Database.Path)
	if err != nil {
		return err
	}
	defer unlock()
	journal, err := store.Open(cfg.Database.Path)
	if err != nil {
		return err
	}
	defer journal.Close()
	if err := journal.RecoverInterrupted(context.Background()); err != nil {
		return err
	}
	devices := make([]*service.Device, 0, len(cfg.Devices))
	for _, d := range cfg.Devices {
		var client *terminalfa.Client
		var err error
		entry := logger.WithField("deviceId", d.ID)
		switch d.Connection.Type {
		case "serial":
			client, err = terminalfa.NewSerialClient(terminalfa.SerialOptions{
				Port: d.Connection.Port, BaudRate: d.Connection.BaudRate, ResponseTimeout: d.Connection.ResponseTimeout.Value(),
			}, entry)
		case "tcp":
			client, err = terminalfa.NewTCPClient(terminalfa.TCPOptions{
				Address: d.Connection.Address, ConnectTimeout: d.Connection.ConnectTimeout.Value(), ResponseTimeout: d.Connection.ResponseTimeout.Value(),
			}, entry)
		default:
			err = fmt.Errorf("unsupported connection type %q", d.Connection.Type)
		}
		if err != nil {
			return fmt.Errorf("configure %s: %w", d.ID, err)
		}
		device := service.NewDevice(d.ID, d.Connection.Type, d.ExpectedSerialNumber, client, d.QueueSize)
		device.ProtocolProfile = d.ProtocolProfile
		devices = append(devices, device)
	}
	app, err := service.New(devices, service.BuiltinCommands(), journal, logger)
	if err != nil {
		return err
	}
	// Listen before reporting startup success. Hardware is opened lazily by the
	// device transport so an unplugged device does not prevent server startup.
	listener, err := net.Listen("tcp", cfg.HTTP.Listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	logWriter := logger.WriterLevel(logrus.ErrorLevel)
	defer logWriter.Close()
	server := &http.Server{
		Handler:           httpapi.New(app, cfg.HTTP, logger),
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout.Value(),
		ReadTimeout:       cfg.HTTP.RequestTimeout.Value(),
		WriteTimeout:      cfg.HTTP.RequestTimeout.Value() + 10*time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16384,
		ErrorLog:          log.New(logWriter, "", 0),
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	logger.WithFields(logrus.Fields{"version": version, "listen": listener.Addr().String(), "devices": len(devices)}).Info("goterminalfa started")
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		logger.Info("shutting down HTTP server")
		shutdown, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout.Value())
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return fmt.Errorf("HTTP shutdown: %w", err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		logger.Info("goterminalfa stopped")
		return nil
	}
}
