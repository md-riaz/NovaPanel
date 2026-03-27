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
        $sites = App::sites()->all();

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
        $users = App::users()->all();
        $templateService = App::siteTemplateService();

        return $this->view('pages/sites/create', [
            'title' => 'Create Site',
            'users' => $users,
            'templates' => $templateService->templates(),
            'selectedTemplate' => $request->query('template', 'basic_php'),
        ]);
    }

    public function store(Request $request): Response
    {
        try {
            $domain = (string) $request->post('domain');
            $userId = (int) $request->post('user_id');
            $phpVersion = (string) $request->post('php_version', '8.2');
            $sslEnabled = (bool) $request->post('ssl_enabled');
            $template = (string) $request->post('template', 'basic_php');

            $service = App::createSiteService();
            $service->execute($userId, $domain, $phpVersion, $sslEnabled, $template);

            AuditLogger::logCreated('site', $domain, [
                'user_id' => $userId,
                'php_version' => $phpVersion,
                'ssl_enabled' => $sslEnabled,
                'template' => $template,
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
