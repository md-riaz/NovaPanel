<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;

class DashboardController extends Controller
{
    public function index(Request $request): Response
    {
        return $this->view('pages/dashboard', [
            'title' => 'Dashboard'
        ]);
    }
}
