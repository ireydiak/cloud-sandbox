# legacy

Minimal Laravel 13 app standing in for a "legacy" application (mirror of the Go
stand-in in `../proxy/internal/legacy`). Stateless: no database, sessions or auth.

| Route            | Response                                                        |
|------------------|-----------------------------------------------------------------|
| `GET /api`       | 200, `application/json`, empty body (ALB health probe)         |
| `GET /api/users` | 200, `application/json`, `[{"id":"1","name":"John"},{"id":"2","name":"Marya"}]` |
| `GET /web/users` | 200, `text/html; charset=utf-8`, Blade-rendered user list       |

Every request is logged as one JSON line on stdout (`"message":"request"`, with
`method`, `path`, `request_id` from `X-Request-Id`, `remote_addr`, `headers`).
Request IDs are set by the upstream proxy, never generated here.

## Run locally

```sh
docker build -t legacy-laravel:dev .
docker run --rm -p 18080:8080 \
  -e APP_KEY=base64:$(openssl rand -base64 32) \
  -e APP_ENV=production -e APP_DEBUG=false \
  legacy-laravel:dev
curl -i localhost:18080/api/users
```

The image runs FrankenPHP (Caddy + PHP in one process) on port 8080 as a
non-root user and works with `--read-only --tmpfs /tmp`.

## Environment variables

No `.env` file is needed; see `.env.example`.

| Variable      | Default      | Notes                                      |
|---------------|--------------|--------------------------------------------|
| `APP_KEY`     | (none)       | Required. `base64:` + 32 random bytes.     |
| `APP_ENV`     | `production` |                                            |
| `APP_DEBUG`   | `false`      |                                            |
| `LOG_CHANNEL` | `stdout`     | JSON lines on stdout.                      |
| `LOG_LEVEL`   | `info`       | Must be `info` or lower to log requests.   |
| `CACHE_STORE` | `array`      |                                            |
