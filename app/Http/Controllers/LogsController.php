<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;
use App\Support\LogViewerService;

class LogsController extends Controller
{
    public function index(Request $request): Response
    {
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
