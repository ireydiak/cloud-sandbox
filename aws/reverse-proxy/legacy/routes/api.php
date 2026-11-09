<?php

use App\Http\Controllers\UsersController;
use Illuminate\Support\Facades\Route;

// Served under the /api prefix: "/" here is GET /api.
Route::get('/', [UsersController::class, 'root']);
Route::get('/users', [UsersController::class, 'index']);
