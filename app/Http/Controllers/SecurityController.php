<?php

namespace App\Http\Controllers;

use App\Facades\App;
use App\Http\Request;
use App\Http\Response;
use App\Http\Session;
use App\Support\CSRF;
use App\Support\SecurityManagerService;

class SecurityController extends Controller
{
    public function index(Request $request): Response
    {
        if (!$this->canManageSecurity()) {
            return new Response('Forbidden', 403);
        }

        $service = new SecurityManagerService();

        return $this->view('pages/security/index', [
            'title' => 'Security',
            'overview' => $service->overview(),
            'message' => $request->query('message'),
            'error' => $request->query('error'),
            'csrfToken' => CSRF::getToken(),
        ]);
    }

    public function runAction(Request $request): Response
    {
        if (!$this->canManageSecurity()) {
            return new Response('Forbidden', 403);
        }

        try {
            if (!CSRF::verify((string) $request->post('_csrf_token'))) {
                throw new \RuntimeException('Invalid CSRF token. Please refresh and try again.');
            }

            $service = new SecurityManagerService();
            $result = $service->runAction((string) $request->post('action'));
            return $this->redirect('/security?message=' . urlencode($result['output']));
        } catch (\Throwable $exception) {
            return $this->redirect('/security?error=' . urlencode($exception->getMessage()));
        }
    }

    private function canManageSecurity(): bool
    {
        Session::start();
        $userId = (int) Session::get('user_id');

        if ($userId <= 0) {
            return false;
        }

        return App::roles()->hasPermission($userId, 'system.settings');
    }
}
