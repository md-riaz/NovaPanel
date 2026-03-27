<?php

namespace App\Http\Middleware;

use App\Facades\App;
use App\Http\Request;
use App\Http\Response;
use App\Http\Session;

class PermissionMiddleware
{
    private array $permissions;

    public function __construct(string ...$permissions)
    {
        $this->permissions = $permissions;
    }

    public function handle(Request $request, callable $next): Response
    {
        Session::start();

        $userId = (int) Session::get('user_id');
        if ($userId <= 0) {
            return $this->forbidden($request);
        }

        foreach ($this->permissions as $permission) {
            if (App::roles()->hasPermission($userId, $permission)) {
                return $next($request);
            }
        }

        return $this->forbidden($request);
    }

    private function forbidden(Request $request): Response
    {
        if ($request->isHtmx()) {
            return new Response('<div class="alert alert-danger" role="alert">You are not authorized to access this area.</div>', 403);
        }

        if ($request->header('Accept') === 'application/json') {
            return (new Response())->json(['error' => 'Forbidden'], 403);
        }

        return (new Response())->html('Forbidden', 403);
    }
}
