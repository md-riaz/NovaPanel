<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;

class AccountController extends Controller
{
    public function index(Request $request): Response
    {
        return $this->view('pages/accounts/index', [
            'title' => 'Accounts'
        ]);
    }

    public function create(Request $request): Response
    {
        return $this->view('pages/accounts/create', [
            'title' => 'Create Account'
        ]);
    }

    public function store(Request $request): Response
    {
        // TODO: Implement account creation logic
        return $this->redirect('/accounts');
    }
}
