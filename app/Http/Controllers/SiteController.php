<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;
use App\Repositories\SiteRepository;
use App\Repositories\AccountRepository;
use App\Services\CreateSiteService;
use App\Facades\WebServer;
use App\Facades\PhpRuntime;

class SiteController extends Controller
{
    public function index(Request $request): Response
    {
        $siteRepo = new SiteRepository();
        $sites = $siteRepo->all();
        
        return $this->view('pages/sites/index', [
            'title' => 'Sites',
            'sites' => $sites
        ]);
    }

    public function create(Request $request): Response
    {
        $accountRepo = new AccountRepository();
        $accounts = $accountRepo->all();
        
        return $this->view('pages/sites/create', [
            'title' => 'Create Site',
            'accounts' => $accounts
        ]);
    }

    public function store(Request $request): Response
    {
        try {
            $domain = $request->post('domain');
            $accountId = (int) $request->post('account_id');
            $phpVersion = $request->post('php_version', '8.2');
            $sslEnabled = (bool) $request->post('ssl_enabled');
            
            $service = new CreateSiteService(
                new SiteRepository(),
                new AccountRepository(),
                WebServer::getInstance(),
                PhpRuntime::getInstance()
            );
            
            $site = $service->execute($accountId, $domain, $phpVersion, $sslEnabled);
            
            return $this->redirect('/sites');
            
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }
}
