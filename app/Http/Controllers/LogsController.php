<?php

namespace App\Http\Controllers;

use App\Facades\App;
use App\Http\Request;
use App\Http\Response;
use App\Http\Session;
use App\Support\LogViewerService;

class LogsController extends Controller
{
    public function index(Request $request): Response
    {
        Session::start();
        $userId = (int) Session::get('user_id');
        if ($userId <= 0 || !App::roles()->hasPermission($userId, 'system.logs')) {
            return new Response('Forbidden', 403);
        }

        $service = new LogViewerService();
        $selected = $request->query('source', 'nginx_access');
        $lines = (int) $request->query('lines', 200);

        return $this->view('pages/logs/index', [
            'title' => 'Log Viewer',
            'sources' => $service->sources(),
            'selected' => $selected,
            'lines' => $lines,
            'log' => $service->read($selected, $lines),
        ]);
    }
}
