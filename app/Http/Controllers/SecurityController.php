<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;
use App\Support\SecurityManagerService;

class SecurityController extends Controller
{
    public function index(Request $request): Response
    {
        $service = new SecurityManagerService();

        return $this->view('pages/security/index', [
            'title' => 'Security',
            'overview' => $service->overview(),
            'message' => $request->query('message'),
            'error' => $request->query('error'),
        ]);
    }

    public function runAction(Request $request): Response
    {
        $service = new SecurityManagerService();

        try {
            $result = $service->runAction((string) $request->post('action'));
            return $this->redirect('/security?message=' . urlencode($result['output']));
        } catch (\Throwable $exception) {
            return $this->redirect('/security?error=' . urlencode($exception->getMessage()));
        }
    }
}
