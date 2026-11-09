# syntax=docker/dockerfile:1

# ---- Stage 1: PHP dependencies (cached on composer.json/lock) ----
FROM composer:2 AS vendor
WORKDIR /app
COPY composer.json composer.lock ./
RUN composer install --no-dev --no-interaction --no-progress --no-scripts \
        --optimize-autoloader

# ---- Stage 2: runtime ----
# Base image choice: dunglas/frankenphp. One process (Caddy + PHP embedded)
# serves HTTP directly: no nginx + php-fpm pair, no supervisor, no Octane
# dependency. Official multi-arch image (linux/amd64 + linux/arm64). Runs in
# classic (non-worker) mode, so each request boots Laravel like php-fpm would,
# which is what we want from a "legacy" stand-in.
FROM dunglas/frankenphp:1-php8.3
WORKDIR /app

# Caddy needs writable config/data dirs; point them at /tmp (works with a
# read-only root filesystem if /tmp is a tmpfs/volume).
ENV XDG_CONFIG_HOME=/tmp/caddy-config \
    XDG_DATA_HOME=/tmp/caddy-data \
    APP_ENV=production \
    APP_DEBUG=false \
    LOG_CHANNEL=stdout

RUN install-php-extensions opcache \
    && cp "$PHP_INI_DIR/php.ini-production" "$PHP_INI_DIR/php.ini" \
    && groupadd --gid 10001 app \
    && useradd --uid 10001 --gid app --no-create-home --shell /usr/sbin/nologin app

COPY Caddyfile /etc/frankenphp/Caddyfile
COPY --from=vendor /app/vendor ./vendor
COPY . .

# Build-time caches that do not depend on runtime env (no APP_KEY needed).
# config:cache is deliberately skipped: it would freeze env() values (APP_KEY,
# LOG_*) at build time, but they are injected at runtime by ECS.
RUN mkdir -p storage/framework/views storage/logs bootstrap/cache \
    && APP_KEY=base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA= \
       sh -c 'php artisan package:discover --ansi && php artisan route:cache && php artisan view:cache' \
    && chown -R app:app storage bootstrap/cache

# Only storage/ and bootstrap/cache are written at runtime.
USER app
EXPOSE 8080
CMD ["frankenphp", "run", "--config", "/etc/frankenphp/Caddyfile"]
