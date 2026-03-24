<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;
use App\Support\SiteTemplateService;

class TemplatesController extends Controller
{
    public function index(Request $request): Response
    {
        $service = new SiteTemplateService();

        return $this->view('pages/templates/index', [
            'title' => 'Application Templates',
            'templates' => $service->templates(),
        ]);
    }
}
