<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;
use App\Repositories\FtpUserRepository;
use App\Repositories\UserRepository;
use App\Services\CreateFtpUserService;
use App\Facades\Ftp;

class FtpController extends Controller
{
    public function index(Request $request): Response
    {
        $ftpRepo = new FtpUserRepository();
        $userRepo = new UserRepository();
        $ftpUsers = $ftpRepo->all();
        
        // Load owner information for each FTP user
        foreach ($ftpUsers as $ftpUser) {
            $user = $userRepo->find($ftpUser->userId);
            $ftpUser->ownerUsername = $user ? $user->username : 'Unknown';
        }
        
        return $this->view('pages/ftp/index', [
            'title' => 'FTP Users',
            'ftpUsers' => $ftpUsers
        ]);
    }

    public function create(Request $request): Response
    {
        $userRepo = new UserRepository();
        $users = $userRepo->all();
        
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
            
            $service = new CreateFtpUserService(
                new FtpUserRepository(),
                new UserRepository(),
                Ftp::getInstance()
            );
            
            $ftpUser = $service->execute(
                userId: $userId,
                ftpUsername: $ftpUsername,
                password: $password,
                homeDirectory: $homeDirectory
            );
            
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
            $ftpRepo = new FtpUserRepository();
            $ftpUser = $ftpRepo->find($id);
            
            if (!$ftpUser) {
                throw new \Exception('FTP user not found');
            }
            
            // Delete from infrastructure
            Ftp::getInstance()->deleteUser($ftpUser);
            
            // Delete from panel database
            $ftpRepo->delete($id);
            
            return $this->redirect('/ftp');
            
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }
}
