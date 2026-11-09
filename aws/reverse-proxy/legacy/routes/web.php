<?php

use App\Http\Controllers\UsersController;
use Illuminate\Support\Facades\Route;

Route::get('/web/users', [UsersController::class, 'web']);
