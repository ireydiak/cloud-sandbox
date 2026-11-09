# Reverse proxy

A Golang reverse proxy hosted on AWS with Cloudfront + ALB + ECS.

Every incoming request is sent to two services:
- **Legacy service**: the system being migrated. Its response is returned to the client.
- **Shadow service**: the new system, which receives the same traffic. Its response is stored for offline comparison with the legacy service.

## Architecture
