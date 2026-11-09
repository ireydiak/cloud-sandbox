# Stage 1
FROM golang:1.26 AS builder

WORKDIR /app

COPY go.mod go.sum* .

RUN go mod download

COPY . .

RUN make build-proxy

# Stage 2
FROM gcr.io/distroless/static-debian13:nonroot

ENV PROXY_ADDRESS=":18080"

COPY --from=builder /app/bin/proxy ./proxy

USER nonroot:nonroot

EXPOSE 18080

ENTRYPOINT ["./proxy"]


