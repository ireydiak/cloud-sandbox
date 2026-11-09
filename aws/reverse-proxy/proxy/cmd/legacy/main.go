package main

import (
	"errors"
	"log/slog"
	"os"

	"github.com/ireydiak/cloud-sandbox/aws/reverse-proxy/internal/legacy"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	addr := os.Getenv("SERVER_ADDRESS")
	if addr == "" {
		logger.Error("missing env", "err", errors.New("Missing SERVER_ADDRESS environment variable"))
	}
	if err := legacy.StartServer(logger, addr); err != nil {
		logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}
