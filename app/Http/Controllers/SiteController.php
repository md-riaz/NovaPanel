<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;
use App\Facades\App;
use App\Support\AuditLogger;

class SiteController extends Controller
{
    public function index(Request $request): Response
    {
        $sites = App::sites()->all();
        
        // Load owner information for each site
        foreach ($sites as $site) {
            $user = App::users()->find($site->userId);
            $site->ownerUsername = $user ? $user->username : 'Unknown';
        }
        
        return $this->view('pages/sites/index', [
            'title' => 'Sites',
            'sites' => $sites
        ]);
    }

    public function create(Request $request): Response
    {
        $users = App::users()->all();
        
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
            
            // Use App facade to get service with all dependencies injected
            $service = App::createSiteService();
            
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
