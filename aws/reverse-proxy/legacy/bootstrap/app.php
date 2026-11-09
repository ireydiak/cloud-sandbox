<?php

use App\Http\Middleware\LogRequest;
use Illuminate\Foundation\Application;
use Illuminate\Http\Middleware\HandleCors;
use Illuminate\Foundation\Configuration\Exceptions;
use Illuminate\Foundation\Configuration\Middleware;

return Application::configure(basePath: dirname(__DIR__))
    ->withRouting(
        web: __DIR__.'/../routes/web.php',
        api: __DIR__.'/../routes/api.php',
        apiPrefix: 'api',
        commands: __DIR__.'/../routes/console.php',
    )
    ->withMiddleware(function (Middleware $middleware): void {
        // Stateless app: no sessions, cookies or CSRF on the web routes.
        $middleware->group('web', []);
        $middleware->remove(HandleCors::class);
        // Log every request (including 404/405) as one JSON line.
        $middleware->prepend(LogRequest::class);
    })
    ->withExceptions(function (Exceptions $exceptions): void {
        $exceptions->shouldRenderJsonWhen(fn ($request) => $request->is('api', 'api/*'));
    })->create();
