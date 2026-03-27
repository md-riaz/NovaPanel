<?php

namespace App\Http\Controllers;

use App\Facades\App;
use App\Http\Request;
use App\Http\Response;
use App\Http\Session;

class TemplatesController extends Controller
{
    public function index(Request $request): Response
    {
        Session::start();
        $userId = (int) Session::get('user_id');
        if ($userId <= 0 || !App::roles()->hasPermission($userId, 'sites.create')) {
            return new Response('Forbidden', 403);
        }

        return $this->view('pages/templates/index', [
            'title' => 'Application Templates',
            'templates' => App::siteTemplateService()->templates(),
        ]);
    }
}
