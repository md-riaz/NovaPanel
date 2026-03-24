<?php

namespace App\Http\Controllers;

use App\Facades\App;
use App\Http\Response;
use App\Http\Session;

abstract class Controller
{
    protected function view(string $template, array $data = []): Response
    {
        $viewPath = __DIR__ . '/../../../resources/views/' . $template . '.php';

        if (!file_exists($viewPath)) {
            return new Response("View not found: $template", 404);
        }

        ob_start();
        extract($data);
        require $viewPath;
        $content = ob_get_clean();

        return (new Response())->html($content);
    }

    protected function json(array $data, int $statusCode = 200): Response
    {
        return (new Response())->json($data, $statusCode);
    }

    protected function redirect(string $url): Response
    {
        return (new Response())->redirect($url);
    }

    protected function currentUserId(): int
    {
        Session::start();
        return (int) Session::get('user_id', 0);
    }

    protected function currentUsername(): string
    {
        Session::start();
        return (string) Session::get('username', 'unknown');
    }

    protected function isAdmin(): bool
    {
        return App::roles()->getPrimaryRoleName($this->currentUserId()) === 'Admin';
    }

    protected function scopedUsers(): array
    {
        if ($this->isAdmin()) {
            return App::users()->all();
        }

        $user = App::users()->find($this->currentUserId());
        return $user ? [$user] : [];
    }

    protected function resolveOwnedUserId(?int $requestedUserId = null): int
    {
        if ($this->isAdmin()) {
            return $requestedUserId ?? $this->currentUserId();
        }

        return $this->currentUserId();
    }

    protected function authorizeOwnedUserId(int $userId): void
    {
        if ($this->isAdmin()) {
            return;
        }

        if ($userId !== $this->currentUserId()) {
            throw new \RuntimeException('You do not have access to this resource.');
        }
    }

    protected function authorizeOwnedSiteId(int $siteId): void
    {
        $site = App::sites()->find($siteId);
        if (!$site) {
            throw new \RuntimeException('Site not found.');
        }

        $this->authorizeOwnedUserId((int) $site->userId);
    }

    protected function authorizeOwnedDomainId(int $domainId): void
    {
        $domain = App::domains()->find($domainId);
        if (!$domain) {
            throw new \RuntimeException('Domain not found.');
        }

        $site = App::sites()->find((int) $domain->siteId);
        if (!$site) {
            throw new \RuntimeException('Site not found.');
        }

        $this->authorizeOwnedUserId((int) $site->userId);
    }

    protected function successAlert(string $message): string
    {
        $html = '<div class="alert alert-success alert-dismissible fade show" role="alert">';
        $html .= '<i class="bi bi-check-circle"></i> ' . htmlspecialchars($message);
        $html .= '<button type="button" class="btn-close" data-bs-dismiss="alert"></button>';
        $html .= '</div>';
        return $html;
    }

    protected function errorAlert(string $message): string
    {
        $html = '<div class="alert alert-danger alert-dismissible fade show" role="alert">';
        $html .= '<i class="bi bi-exclamation-triangle"></i> Error: ' . htmlspecialchars($message);
        $html .= '<button type="button" class="btn-close" data-bs-dismiss="alert"></button>';
        $html .= '</div>';
        return $html;
    }
}
