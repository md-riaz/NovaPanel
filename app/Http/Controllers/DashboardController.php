<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;
use App\Facades\App;

class DashboardController extends Controller
{
    public function index(Request $request): Response
    {
        $stats = $this->getStats();
        
        return $this->view('pages/dashboard', [
            'title' => 'Dashboard',
            'stats' => $stats
        ]);
    }

    public function stats(Request $request): Response
    {
        $stats = $this->getStats();
        
        // Return HTML fragment for HTMX using partial
        ob_start();
        include __DIR__ . '/../../../resources/views/partials/widgets/stats.php';
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
            'ftp_users' => App::ftpUsers()->count()
        ];
    }
}
