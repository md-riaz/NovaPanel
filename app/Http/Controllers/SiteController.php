<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;
use App\Repositories\SiteRepository;
use App\Services\CreateSiteService;
use App\Facades\WebServer;
use App\Facades\PhpRuntime;
use App\Support\AuditLogger;

class SiteController extends Controller
{
    public function index(Request $request): Response
    {
        $siteRepo = new SiteRepository();
        $userRepo = new \App\Repositories\UserRepository();
        $sites = $siteRepo->all();
        
        // Load owner information for each site
        foreach ($sites as $site) {
            $user = $userRepo->find($site->userId);
            $site->ownerUsername = $user ? $user->username : 'Unknown';
        }
        
        return $this->view('pages/sites/index', [
            'title' => 'Sites',
            'sites' => $sites
        ]);
    }

    public function create(Request $request): Response
    {
        $userRepo = new \App\Repositories\UserRepository();
        $users = $userRepo->all();
        
        return $this->view('pages/sites/create', [
            'title' => 'Create Site',
            'users' => $users
        ]);
    }

    public function store(Request $request): Response
    {
        try {
            $domain = $request->post('domain');
            $userId = (int) $request->post('user_id');
            $phpVersion = $request->post('php_version', '8.2');
            $sslEnabled = (bool) $request->post('ssl_enabled');
            
            $service = new CreateSiteService(
                new SiteRepository(),
                new \App\Repositories\UserRepository(),
                WebServer::getInstance(),
                PhpRuntime::getInstance()
            );
            
            $site = $service->execute($userId, $domain, $phpVersion, $sslEnabled);
            
            // Log audit event
            AuditLogger::logCreated('site', $domain, [
                'user_id' => $userId,
                'php_version' => $phpVersion,
                'ssl_enabled' => $sslEnabled
            ]);
            
            // Check if this is an HTMX request
            if ($request->isHtmx()) {
                return new Response($this->successAlert('Site created successfully! Redirecting...'));
            }
            
            return $this->redirect('/sites');
            
        } catch (\Exception $e) {
            // Check if this is an HTMX request
            if ($request->isHtmx()) {
                return new Response($this->errorAlert($e->getMessage()), 400);
            }
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }
}
