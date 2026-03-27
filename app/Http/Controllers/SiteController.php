<?php

namespace App\Http\Controllers;

use App\Facades\App;
use App\Http\Request;
use App\Http\Response;
use App\Support\AuditLogger;
use App\Support\CSRF;

class SiteController extends Controller
{
    public function index(Request $request): Response
    {
        $sites = App::sites()->all();

        foreach ($sites as $site) {
            $user = App::users()->find($site->userId);
            $site->ownerUsername = $user ? $user->username : 'Unknown';
        }
        $sites = $this->loadSites();

        return $this->view('pages/sites/index', [
            'title' => 'Sites',
            'sites' => $sites,
        ]);
    }

    public function show(Request $request, int $id): Response
    {
        $site = App::sites()->findWithOwner($id);
        if (!$site) {
            return new Response('Site not found', 404);
        }

        return $this->view('pages/sites/show', [
            'title' => 'Manage Site',
            'site' => $site,
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
        return $this->view('pages/sites/create', [
            'title' => 'Create Site',
            'users' => App::users()->all(),
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
            $domain = $request->post('domain');
            $userId = $this->resolveOwnedUserId((int) $request->post('user_id'));
            $phpVersion = $request->post('php_version', '8.2');
            $sslEnabled = (bool) $request->post('ssl_enabled');

            $site = App::createSiteService()->execute($userId, $domain, $phpVersion, $sslEnabled);
            $csrfToken = (string) $request->post('_csrf_token');
            if (!CSRF::verify($csrfToken)) {
                throw new \RuntimeException('Invalid CSRF token. Please refresh and try again.');
            }

            $domain = trim((string) $request->post('domain'));
            $userId = (int) $request->post('user_id');
            $phpVersion = (string) $request->post('php_version', '8.2');
            $requestCertificate = (bool) $request->post('request_certificate');
            $validationMethod = (string) $request->post('certificate_validation_method', 'webroot');
            $autoRenew = $request->post('certificate_auto_renew') !== null;
            $forceHttps = $request->post('force_https') !== null;
            $provider = (string) $request->post('certificate_provider', 'letsencrypt');

            $site = App::createSiteService()->execute(
                $userId,
                $domain,
                $phpVersion,
                $requestCertificate,
                $validationMethod,
                $autoRenew,
                $forceHttps,
                $provider
            );

            AuditLogger::logCreated('site', $domain, [
                'user_id' => $userId,
                'php_version' => $phpVersion,
                'ssl_enabled' => $sslEnabled,
                'template' => $template,
            ]);

            ]);

                'request_certificate' => $requestCertificate,
                'certificate_provider' => $provider,
                'certificate_validation_method' => $validationMethod,
                'force_https' => $forceHttps,
            ]);

            $message = 'Site created successfully.';
            if ($requestCertificate && $site->lastCertificateError) {
                $message .= ' Certificate request failed and has been logged for follow-up.';
            } elseif ($requestCertificate) {
                $message .= ' Certificate request completed successfully.';
            }

            if ($request->isHtmx()) {
                return new Response($this->successAlert($message));
            }

            return $this->redirect('/sites');
        } catch (\Exception $e) {
            return $this->redirect('/sites/' . $site->id);
        } catch (\Throwable $exception) {
            if ($request->isHtmx()) {
                return new Response($this->errorAlert($exception->getMessage()), 400);
            }

            return $this->redirect('/sites');
        } catch (\Exception $e) {
            return $this->json(['error' => $exception->getMessage()], 400);
        }
    }

    public function requestCertificate(Request $request, int $id): Response
    {
        return $this->runCertificateAction($request, $id, function ($site) use ($request) {
            return App::acmeCertificates()->issue(
                $site,
                (string) $request->post('certificate_provider', $site->certificateProvider ?? 'letsencrypt'),
                (string) $request->post('certificate_validation_method', $site->certificateValidationMethod ?? 'webroot'),
                $request->post('certificate_auto_renew') !== null,
                $request->post('force_https') !== null
            );
        }, 'requested');
    }

    public function renewCertificate(Request $request, int $id): Response
    {
        return $this->runCertificateAction($request, $id, fn ($site) => App::acmeCertificates()->renew($site, true), 'renewed');
    }

    public function reinstallCertificate(Request $request, int $id): Response
    {
        return $this->runCertificateAction($request, $id, fn ($site) => App::acmeCertificates()->reinstall($site), 'reinstalled');
    }

    public function revokeCertificate(Request $request, int $id): Response
    {
        return $this->runCertificateAction($request, $id, fn ($site) => App::acmeCertificates()->revoke($site), 'revoked');
    }

    public function updateHttps(Request $request, int $id): Response
    {
        $site = App::sites()->find($id);
        if (!$site) {
            return new Response('Site not found', 404);
        }

        App::siteHttpsService()->updateForceHttps($site, $request->post('force_https') !== null);

        return $this->redirect('/sites/' . $site->id);
    }

    private function runCertificateAction(Request $request, int $id, callable $callback, string $verb): Response
    {
        $site = App::sites()->find($id);
        if (!$site) {
            return new Response('Site not found', 404);
        }

        try {
            $site = $callback($site);
            AuditLogger::logUpdated('certificate', $site->domain, [
                'site_id' => $site->id,
                'action' => $verb,
                'status' => $site->certificateStatus,
                'expires_at' => $site->certificateExpiresAt,
            ]);

            return $this->redirect('/sites/' . $site->id);
        } catch (\Throwable $exception) {
            if ($request->isHtmx()) {
                return new Response($this->errorAlert($exception->getMessage()), 400);
            }

            return $this->json(['error' => $e->getMessage()], 400);
            return $this->json(['error' => $exception->getMessage()], 400);
        }
    }

    private function loadSites(): array
    {
        return App::sites()->allWithOwners();
    }
}
