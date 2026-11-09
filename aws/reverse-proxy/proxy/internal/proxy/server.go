package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/google/uuid"
)

type server struct {
	logger *slog.Logger
}

type spy struct {
	http.ResponseWriter
	status int
	size int
	body bytes.Buffer
	wroteHeader bool
}

func (s *spy) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.ResponseWriter.WriteHeader(code)
		s.wroteHeader = true
	}
}

func (s *spy) Write(p []byte) (int, error) {
	s.wroteHeader = true
	n, err := s.ResponseWriter.Write(p)
	s.size += n
	s.body.Write(p[:n])

	return n, err
}

func (s *spy) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

func (s *server) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestId := uuid.Must(uuid.NewV7()).String()
		w.Header().Set("X-Request-Id", requestId)
		r.Header.Set("X-Request-Id", requestId)

		next.ServeHTTP(w, r)
	})
}

func (s *server) withSpy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		before := time.Now()
		sp := &spy{ ResponseWriter: w, status: http.StatusOK }
		b, err := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(b))
		s.logger.Info("request",
			slog.String("method", r.Method),
			slog.Int64("content_length", r.ContentLength),
			slog.Int("received_bytes", len(b)),
			slog.String("path", r.URL.Path),
			slog.String("request_id", r.Header.Get("X-Request-Id")),
			slog.String("remote_addr", r.RemoteAddr),
			"body", string(b),
			slog.Any("headers", r.Header),
		)

		forwarded := false
		var group slog.Attr
		defer func() {
			s.logger.Info("response",
				"latency_ms", time.Since(before).Milliseconds(),
				"request_id", r.Header.Get("X-Request-Id"),
				"body", sp.body.String(),
				"status", sp.status,
				"headers", sp.Header(),
				"size", sp.size,
				"method", r.Method,
				"path", r.URL.Path,
				"forwarded", forwarded,
				group,
			)
		}()

		if err == nil {
			forwarded = true
			next.ServeHTTP(sp, r)
		} else {
			var netErr net.Error
			status := http.StatusBadRequest
			if errors.As(err, &netErr) && netErr.Timeout() {
				status = http.StatusRequestTimeout
			}
			group = slog.Group(
				"proxy",
				"error", err,
			)
			http.Error(sp, http.StatusText(status), status)
		}
	})
}

func CreateProxy(logger *slog.Logger, proxyAddr, targetAddr string) error {
	target, err := url.Parse(targetAddr)
	if err != nil {
		return err
	}
	
	s := &server{logger: logger}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.SetXForwarded()
		},
		Transport: &http.Transport{
			MaxIdleConns: 100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout: 30 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
		ErrorHandler: createProxyErrorHandler(logger),
	}

	return http.ListenAndServe(proxyAddr, s.withRequestID(s.withSpy(proxy)))
}

func createProxyErrorHandler(logger *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		status, level := classify(err)
		logger.Log(
			r.Context(),
			level,
			"proxy error",
			"err", err,
			"status", status,
			"request_id", r.Header.Get("X-Request-Id"),
			"method", r.Method,
			"path", r.URL.Path,
		)
		http.Error(w, http.StatusText(status), status)
	}
}

func classify(err error) (int, slog.Level) {
	var maxBytesErr *http.MaxBytesError
	var netErr net.Error
	switch {
		case errors.Is(err, context.Canceled):
			return http.StatusGone, slog.LevelWarn
		case errors.Is(err, context.DeadlineExceeded),
			errors.As(err, &netErr) && netErr.Timeout():
			return http.StatusGatewayTimeout, slog.LevelWarn
		case errors.As(err, &maxBytesErr):
			return http.StatusRequestEntityTooLarge, slog.LevelInfo
		default:
			return http.StatusBadGateway, slog.LevelError
	}
}
