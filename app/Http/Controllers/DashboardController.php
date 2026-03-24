<?php

namespace App\Http\Controllers;

use App\Facades\App;
use App\Http\Request;
use App\Http\Response;
use App\Support\SystemStatusService;

class DashboardController extends Controller
{
    public function index(Request $request): Response
    {
        return $this->view('pages/dashboard', [
            'title' => 'Dashboard',
            'stats' => $this->getStats(),
            'systemStatus' => (new SystemStatusService())->snapshot(),
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

    public function systemStatus(Request $request): Response
    {
        $systemStatus = (new SystemStatusService())->snapshot();

        ob_start();
        include __DIR__ . '/../../../resources/views/partials/widgets/system-status.php';
        $html = ob_get_clean();

        return new Response($html);
    }

    private function getStats(): array
    {
        $users = App::users()->all();
        $sites = App::sites()->all();

        return [
            'accounts' => count($users),
            'sites' => count($sites),
            'databases' => App::databases()->count(),
            'ftp_users' => App::ftpUsers()->count(),
        ];
    }
}
