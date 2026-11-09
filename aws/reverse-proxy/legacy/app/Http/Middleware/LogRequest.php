<?php

namespace App\Http\Middleware;

use Closure;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Log;
use Symfony\Component\HttpFoundation\Response;

class LogRequest
{
    public function handle(Request $request, Closure $next): Response
    {
        // The request ID is set by the upstream proxy; never generated here.
        Log::info('request', [
            'method' => $request->method(),
            'path' => '/'.ltrim($request->path(), '/'),
            'request_id' => (string) $request->header('X-Request-Id', ''),
            'remote_addr' => $request->server('REMOTE_ADDR'),
            'headers' => $request->headers->all(),
        ]);

        return $next($request);
    }
}
