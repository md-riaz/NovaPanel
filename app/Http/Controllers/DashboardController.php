<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;
use App\Repositories\SiteRepository;
use App\Repositories\UserRepository;

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
        $userRepo = new UserRepository();
        $siteRepo = new SiteRepository();
        
        $users = $userRepo->all();
        $sites = $siteRepo->all();
        
        return [
            'accounts' => count($users),
            'sites' => count($sites),
            'databases' => 0, // TODO: Implement database count
            'ftp_users' => 0  // TODO: Implement FTP user count
        ];
    }
}
