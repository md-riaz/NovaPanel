<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;
use App\Repositories\DatabaseRepository;
use App\Repositories\UserRepository;
use App\Services\CreateDatabaseService;
use App\Facades\DatabaseManager;
use App\Support\AuditLogger;

class DatabaseController extends Controller
{
    public function index(Request $request): Response
    {
        $dbRepo = new DatabaseRepository();
        $userRepo = new UserRepository();
        $databases = $dbRepo->all();
        
        // Load owner information for each database
        foreach ($databases as $db) {
            $user = $userRepo->find($db->userId);
            $db->ownerUsername = $user ? $user->username : 'Unknown';
        }
        
        return $this->view('pages/databases/index', [
            'title' => 'Databases',
            'databases' => $databases
        ]);
    }

    public function create(Request $request): Response
    {
        $userRepo = new UserRepository();
        $users = $userRepo->all();
        
        return $this->view('pages/databases/create', [
            'title' => 'Create Database',
            'users' => $users
        ]);
    }

    public function store(Request $request): Response
    {
        try {
            $dbName = $request->post('db_name');
            $userId = (int) $request->post('user_id');
            $dbType = $request->post('db_type', 'mysql');
            $dbUsername = $request->post('db_username');
            $dbPassword = $request->post('db_password');
            
            $service = new CreateDatabaseService(
                new DatabaseRepository(),
                new \App\Repositories\DatabaseUserRepository(),
                new UserRepository(),
                DatabaseManager::getInstance()
            );
            
            $database = $service->execute(
                userId: $userId,
                dbName: $dbName,
                dbType: $dbType,
                dbUsername: $dbUsername,
                dbPassword: $dbPassword
            );
            
            // Log audit event
            AuditLogger::logCreated('database', $dbName, [
                'user_id' => $userId,
                'db_type' => $dbType,
                'db_username' => $dbUsername
            ]);
            
            // Check if this is an HTMX request
            if ($request->isHtmx()) {
                return new Response($this->successAlert('Database created successfully! Redirecting...'));
            }
            
            return $this->redirect('/databases');
            
        } catch (\Exception $e) {
            // Check if this is an HTMX request
            if ($request->isHtmx()) {
                return new Response($this->errorAlert($e->getMessage()), 400);
            }
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }

    public function delete(Request $request, int $id): Response
    {
        try {
            $dbRepo = new DatabaseRepository();
            $database = $dbRepo->find($id);
            
            if (!$database) {
                throw new \Exception('Database not found');
            }
            
            // Log audit event before deletion
            AuditLogger::logDeleted('database', $database->name, [
                'database_id' => $id,
                'db_type' => $database->type
            ]);
            
            // Delete from infrastructure
            DatabaseManager::getInstance()->deleteDatabase($database);
            
            // Delete from panel database
            $dbRepo->delete($id);
            
            return $this->redirect('/databases');
            
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }

    /**
     * Handle phpMyAdmin SSO (Single Sign-On)
     * 
     * This method sets up the phpMyAdmin signon session and redirects to phpMyAdmin.
     * The user is automatically authenticated without entering credentials.
     */
    public function phpMyAdminSignon(Request $request): Response
    {
        // Get MySQL credentials from environment using Env facade
        $mysqlHost = \App\Support\Env::get('MYSQL_HOST', 'localhost');
        $mysqlUser = \App\Support\Env::get('MYSQL_ROOT_USER', 'root');
        $mysqlPassword = \App\Support\Env::get('MYSQL_ROOT_PASSWORD', '');
        
        // Set phpMyAdmin signon session with MySQL credentials
        $_SESSION['novapanel_pma_signon'] = [
            'user' => $mysqlUser,
            'password' => $mysqlPassword,
            'host' => $mysqlHost,
        ];
        
        // Build redirect URL with optional database parameter
        $redirectUrl = '/phpmyadmin/';
        $db = $request->get('db');
        if ($db && !empty($db)) {
            $redirectUrl .= '?db=' . urlencode($db);
        }
        
        // Redirect to phpMyAdmin
        return $this->redirect($redirectUrl);
    }
}
