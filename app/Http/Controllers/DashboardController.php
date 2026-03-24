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
        if ($this->isAdmin()) {
            return [
                'accounts' => count(App::users()->all()),
                'sites' => count(App::sites()->all()),
                'databases' => App::databases()->count(),
                'ftp_users' => App::ftpUsers()->count(),
            ];
        }

        $userId = $this->currentUserId();

        return [
            'accounts' => 1,
            'sites' => count(App::sites()->findByUserId($userId)),
            'databases' => count(App::databases()->findByUserId($userId)),
            'ftp_users' => count(App::ftpUsers()->findByUserId($userId)),
        ];
    }
}
