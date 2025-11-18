<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;
use App\Facades\App;
use App\Facades\Ftp;
use App\Support\AuditLogger;

class FtpController extends Controller
{
    public function index(Request $request): Response
    {
        $ftpUsers = App::ftpUsers()->all();
        
        // Load owner information for each FTP user
        foreach ($ftpUsers as $ftpUser) {
            $user = App::users()->find($ftpUser->userId);
            $ftpUser->ownerUsername = $user ? $user->username : 'Unknown';
        }
        
        return $this->view('pages/ftp/index', [
            'title' => 'FTP Users',
            'ftpUsers' => $ftpUsers
        ]);
    }

    public function create(Request $request): Response
    {
        $users = App::users()->all();
        
        return $this->view('pages/ftp/create', [
            'title' => 'Create FTP User',
            'users' => $users
        ]);
    }

    public function store(Request $request): Response
    {
        try {
            $ftpUsername = $request->post('ftp_username');
            $userId = (int) $request->post('user_id');
            $password = $request->post('password');
            $homeDirectory = $request->post('home_directory');
            
            // Use App facade to get service with all dependencies injected
            $service = App::createFtpUserService();
            
            $ftpUser = $service->execute(
                userId: $userId,
                ftpUsername: $ftpUsername,
                password: $password,
                homeDirectory: $homeDirectory
            );
            
            // Log audit event
            AuditLogger::logCreated('ftp_user', $ftpUsername, [
                'user_id' => $userId,
                'home_directory' => $homeDirectory
            ]);
            
            // Check if this is an HTMX request
            if ($request->isHtmx()) {
                return new Response($this->successAlert('FTP user created successfully! Redirecting...'));
            }
            
            return $this->redirect('/ftp');
            
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
            $ftpUser = App::ftpUsers()->find($id);
            
            if (!$ftpUser) {
                throw new \Exception('FTP user not found');
            }
            
            // Log audit event before deletion
            AuditLogger::logDeleted('ftp_user', $ftpUser->username, [
                'ftp_user_id' => $id,
                'home_directory' => $ftpUser->homeDirectory
            ]);
            
            // Delete from infrastructure
            Ftp::getInstance()->deleteUser($ftpUser);
            
            // Delete from panel database
            App::ftpUsers()->delete($id);
            
            return $this->redirect('/ftp');
            
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }
}
