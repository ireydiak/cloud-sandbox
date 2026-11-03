# ws-lab: WebSocket behaviour on ALB+ECS vs API Gateway+Lambda

A small, disposable AWS lab that tests, with evidence, how WebSocket connections behave in:

- **Stack A:** ALB -> ECS Fargate (Go server)
- **Stack B:** API Gateway WebSocket API -> Lambda (Go)

Everything is Go, OpenTofu (local state), arm64. WebSocket library: `github.com/coder/websocket`
(context-aware API, concurrent-safe writes, explicit close codes/handshake, which this lab inspects closely).

## Prerequisites

`tofu`, `go` (1.24+), `docker`, `aws` CLI, and working credentials (`aws sts get-caller-identity`).
`make` checks all of this up front. Region/profile: `make deploy AWS_REGION=us-east-1 AWS_PROFILE=default`.

## Commands

| Command | What it does |
|---|---|
| `make build` | Cross-compiles all binaries (arm64) and builds the `FROM scratch` image |
| `make plan` | Builds, then shows the full `tofu plan` (no changes) |
| `make deploy` | `tofu init`, targeted ECR apply, build + push image, full apply, wait for ECS stable. Idempotent |
| `make test` / `test-a` / `test-b` | Run the tests and print the report (`B5=1` adds the 10-minute idle test) |
| `make logs` | Tail ECS + Lambda CloudWatch logs |
| `make destroy` | `tofu destroy`, delete local artifacts, then `verify-clean` |
| `make verify-clean` | Fails and lists anything left (tagging API, log groups, ECR, ECS, ALB, API GW, DynamoDB, Lambda); retries up to 5 min |

`make test` takes roughly 20-30 minutes (A1 alone is 3 minutes; A7 does two rolling deployments).

## Cost

While up, roughly **$0.09/hour (~$2/day)**: ALB ~$0.03 (hourly + LCU), 3 Fargate ARM tasks (0.25 vCPU / 0.5 GB)
~$0.03, five public IPv4 addresses (3 tasks + 2 ALB) ~$0.025. Lambda, API Gateway, DynamoDB on-demand and
logs are cents for a test run. No NAT gateway. Prices are approximate us-east-1 list prices.
After `make destroy` + `make verify-clean` passes, nothing bills.

The ALB listens on plain HTTP:80 and `/broadcast` is unauthenticated; pass `-var allowed_cidr=<your-ip>/32`
(edit the Makefile `TF_VARS`) if you want to restrict it while it is up.

## What each test proves

Stack A (A1/A3/A4 share one 20-socket, 3-minute run):

- **A1** each socket sees exactly one task ID for its lifetime; sockets spread over >1 task (no stickiness).
- **A2** 100 fresh HTTP requests to `/whoami` hit >1 task.
- **A3** the server-side `RemoteAddr` is constant per socket and distinct across sockets (dedicated ALB->task connection per client).
- **A4** a per-connection in-memory counter increments by exactly 1 per message.
- **A5** `POST /broadcast` reaches only the sockets held by the task that handled the request.
- **A6** with the ALB idle timeout at 20s, a silent socket (`?ping=off`) is closed at 20s +/- 5s; one with 10s server pings survives 60s.
- **A7** during `update-service --force-new-deployment` (deregistration delay 30s, `stopTimeout` 45s), run twice: graceful close off (expect the ALB to cut) and on (expect the server to close with 1001). Prints each socket's close code, time since deployment trigger, and time since the server's SIGTERM notice (the server pushes a `sigterm` frame when it receives the signal). Reconnects after the deployment must land on new tasks.

Stack B (B1-B3 share one 30-connection run):

- **B1** one connection sees >1 Lambda execution environment ID (bursts with a 400 ms handler delay force concurrency).
- **B2** `connectionId` is constant per connection.
- **B3** the DynamoDB-stored name is always correct while the in-memory counter is not a clean +1 sequence.
- **B4** a route-less Lambda invoked with `aws lambda invoke` pushes to a live connection, then returns `GoneException` (410) after close.
- **B5** (off by default, `B5=1`) a silent connection is closed at ~10 minutes.

Note: accounts with a low Lambda concurrency limit can throttle the B1 bursts; throttled messages appear as
"error/unparsed frames" in the B1 summary rather than hiding.

## Reading the report

Each test prints `[PASS]`/`[FAIL]`, a one-line summary and per-connection detail. The raw evidence (every frame
received with task IDs, remote addresses, environment IDs, log streams, timestamps, and close codes) is written to
`build/evidence-<timestamp>.jsonl`. A FAIL is not massaged away: it means either a lab bug or evidence against the
hypothesis, and the summary says what actually happened.
