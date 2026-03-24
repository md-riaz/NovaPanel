<?php

namespace App\Http\Controllers;

use App\Facades\App;
use App\Http\Request;
use App\Http\Response;
use App\Support\AuditLogger;

class SiteController extends Controller
{
    public function index(Request $request): Response
    {
        $sites = $this->isAdmin()
            ? App::sites()->all()
            : App::sites()->findByUserId($this->currentUserId());

        foreach ($sites as $site) {
            $user = App::users()->find($site->userId);
            $site->ownerUsername = $user ? $user->username : 'Unknown';
        }

        return $this->view('pages/sites/index', [
            'title' => 'Sites',
            'sites' => $sites,
        ]);
    }

    public function create(Request $request): Response
    {
        return $this->view('pages/sites/create', [
            'title' => 'Create Site',
            'users' => $this->scopedUsers(),
        ]);
    }

    public function store(Request $request): Response
    {
        try {
            $domain = $request->post('domain');
            $userId = $this->resolveOwnedUserId((int) $request->post('user_id'));
            $phpVersion = $request->post('php_version', '8.2');
            $sslEnabled = (bool) $request->post('ssl_enabled');

            $site = App::createSiteService()->execute($userId, $domain, $phpVersion, $sslEnabled);

            AuditLogger::logCreated('site', $domain, [
                'user_id' => $userId,
                'php_version' => $phpVersion,
                'ssl_enabled' => $sslEnabled,
            ]);

            if ($request->isHtmx()) {
                return new Response($this->successAlert('Site created successfully! Redirecting...'));
            }

            return $this->redirect('/sites');
        } catch (\Exception $e) {
            if ($request->isHtmx()) {
                return new Response($this->errorAlert($e->getMessage()), 400);
            }

            return $this->json(['error' => $e->getMessage()], 400);
        }
    }
}
