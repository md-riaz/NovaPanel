<?php

namespace App\Http\Controllers;

use App\Facades\App;
use App\Facades\DatabaseManager;
use App\Http\Request;
use App\Http\Response;
use App\Support\AuditLogger;

class DatabaseController extends Controller
{
    public function index(Request $request): Response
    {
        $databases = $this->isAdmin()
            ? App::databases()->all()
            : App::databases()->findByUserId($this->currentUserId());

        foreach ($databases as $database) {
            $user = App::users()->find($database->userId);
            $database->ownerUsername = $user ? $user->username : 'Unknown';
        }

        return $this->view('pages/databases/index', [
            'title' => 'Databases',
            'databases' => $databases,
        ]);
    }

    public function create(Request $request): Response
    {
        return $this->view('pages/databases/create', [
            'title' => 'Create Database',
            'users' => $this->scopedUsers(),
        ]);
    }

    public function store(Request $request): Response
    {
        try {
            $dbName = $request->post('db_name');
            $userId = $this->resolveOwnedUserId((int) $request->post('user_id'));
            $dbType = $request->post('db_type', 'mysql');
            $dbUsername = $request->post('db_username');
            $dbPassword = $request->post('db_password');

            App::createDatabaseService()->execute(
                userId: $userId,
                dbName: $dbName,
                dbType: $dbType,
                dbUsername: $dbUsername,
                dbPassword: $dbPassword
            );

            AuditLogger::logCreated('database', $dbName, [
                'user_id' => $userId,
                'db_type' => $dbType,
                'db_username' => $dbUsername,
            ]);

            if ($request->isHtmx()) {
                return new Response($this->successAlert('Database created successfully! Redirecting...'));
            }

            return $this->redirect('/databases');
        } catch (\Exception $e) {
            if ($request->isHtmx()) {
                return new Response($this->errorAlert($e->getMessage()), 400);
            }

            return $this->json(['error' => $e->getMessage()], 400);
        }
    }

    public function delete(Request $request, int $id): Response
    {
        try {
            $database = App::databases()->find($id);
            if (!$database) {
                throw new \Exception('Database not found');
            }

            $this->authorizeOwnedUserId((int) $database->userId);

            AuditLogger::logDeleted('database', $database->name, [
                'database_id' => $id,
                'db_type' => $database->type,
            ]);

            DatabaseManager::getInstance()->deleteDatabase($database);
            App::databases()->delete($id);

            return $this->redirect('/databases');
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }

    public function phpMyAdminSignon(Request $request): Response
    {
        $dbName = $request->query('db');
        if (!$this->isAdmin() && $dbName) {
            $database = App::databases()->findByName($dbName);
            if (!$database) {
                return $this->json(['error' => 'Database not found'], 404);
            }

            try {
                $this->authorizeOwnedUserId((int) $database->userId);
            } catch (\RuntimeException $e) {
                return $this->json(['error' => $e->getMessage()], 403);
            }
        }

        $_SESSION['novapanel_pma_signon'] = [
            'user' => \App\Support\Env::get('MYSQL_ROOT_USER', 'root'),
            'password' => \App\Support\Env::get('MYSQL_ROOT_PASSWORD', ''),
            'host' => \App\Support\Env::get('MYSQL_HOST', 'localhost'),
        ];

        $redirectUrl = '/phpmyadmin/';
        if ($dbName && !empty($dbName)) {
            $redirectUrl .= '?db=' . urlencode($dbName);
        }

        return $this->redirect($redirectUrl);
    }
}
