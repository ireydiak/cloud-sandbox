package main

import (
	"log"
	"log/slog"
	"os"

	"github.com/ireydiak/cloud-sandbox/aws/reverse-proxy/internal/proxy"
)

func main() {
	proxyAddr := os.Getenv("PROXY_ADDRESS")
	remoteAddr := os.Getenv("REMOTE_ADDRESS")

	if proxyAddr == "" || remoteAddr == "" {
		log.Fatalf("can't initialize server: missing one of PROXY_ADDRESS or REMOTE_ADDRESS")
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	log.Fatal(proxy.CreateProxy(logger, proxyAddr, remoteAddr))
}
