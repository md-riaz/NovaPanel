<?php

namespace App\Http\Controllers;

use App\Facades\App;
use App\Http\Request;
use App\Http\Response;

class DashboardController extends Controller
{
    public function index(Request $request): Response
    {
        return $this->view('pages/dashboard', [
            'title' => 'Dashboard',
            'stats' => $this->getStats(),
            'certificateFailures' => $this->getCertificateFailures(),
        ]);
    }

    public function stats(Request $request): Response
    {
        $stats = $this->getStats();
        ob_start();
        include __DIR__ . '/../../../resources/views/partials/widgets/stats.php';
        $html = ob_get_clean();

        return new Response($html);
    }

    private function getStats(): array
    {
        return [
            'accounts' => count(App::users()->all()),
            'sites' => count(App::sites()->all()),
            'databases' => App::databases()->count(),
            'ftp_users' => App::ftpUsers()->count(),
        ];
    }

    private function getCertificateFailures(): array
    {
        $sites = App::sites()->findWithCertificateFailures();

        foreach ($sites as $site) {
            $user = App::users()->find($site->userId);
            $site->ownerUsername = $user ? $user->username : 'Unknown';
        }

        return $sites;
    }
}
