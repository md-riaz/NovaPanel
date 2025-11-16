<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;

class SiteController extends Controller
{
    public function index(Request $request): Response
    {
        return $this->view('pages/sites/index', [
            'title' => 'Sites'
        ]);
    }

    public function create(Request $request): Response
    {
        return $this->view('pages/sites/create', [
            'title' => 'Create Site'
        ]);
    }

    public function store(Request $request): Response
    {
        // TODO: Implement site creation logic
        return $this->redirect('/sites');
    }
}
