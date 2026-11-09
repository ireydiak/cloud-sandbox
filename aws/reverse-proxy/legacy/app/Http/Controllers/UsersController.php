<?php

namespace App\Http\Controllers;

use App\Users;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Response;

class UsersController
{
    public function root(): Response
    {
        return response('', 200, ['Content-Type' => 'application/json']);
    }

    public function index(): JsonResponse
    {
        return response()->json(Users::fetch());
    }

    public function web(): Response
    {
        return response()
            ->view('users', ['users' => Users::fetch()])
            ->header('Content-Type', 'text/html; charset=utf-8');
    }
}
