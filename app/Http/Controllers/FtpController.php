<?php

namespace App\Http\Controllers;

use App\Facades\App;
use App\Facades\Ftp;
use App\Http\Request;
use App\Http\Response;
use App\Support\ForbiddenException;
use App\Support\AuditLogger;

class FtpController extends Controller
{
    public function index(Request $request): Response
    {
        $ftpUsers = $this->isAdmin()
            ? App::ftpUsers()->all()
            : App::ftpUsers()->findByUserId($this->currentUserId());

        foreach ($ftpUsers as $ftpUser) {
            $user = App::users()->find($ftpUser->userId);
            $ftpUser->ownerUsername = $user ? $user->username : 'Unknown';
        }

        return $this->view('pages/ftp/index', [
            'title' => 'FTP Users',
            'ftpUsers' => $ftpUsers,
        ]);
    }

    public function create(Request $request): Response
    {
        return $this->view('pages/ftp/create', [
            'title' => 'Create FTP User',
            'users' => $this->scopedUsers(),
        ]);
    }

    public function store(Request $request): Response
    {
        try {
            $ftpUsername = $request->post('ftp_username');
            $userId = $this->resolveOwnedUserId((int) $request->post('user_id'));
            $password = $request->post('password');
            $homeDirectory = $request->post('home_directory');

            App::createFtpUserService()->execute(
                userId: $userId,
                ftpUsername: $ftpUsername,
                password: $password,
                homeDirectory: $homeDirectory
            );

            AuditLogger::logCreated('ftp_user', $ftpUsername, [
                'user_id' => $userId,
                'home_directory' => $homeDirectory,
            ]);

            if ($request->isHtmx()) {
                return new Response($this->successAlert('FTP user created successfully! Redirecting...'));
            }

            return $this->redirect('/ftp');
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
            $ftpUser = App::ftpUsers()->find($id);
            if (!$ftpUser) {
                throw new \Exception('FTP user not found');
            }

            $this->authorizeOwnedUserId((int) $ftpUser->userId);

            AuditLogger::logDeleted('ftp_user', $ftpUser->username, [
                'ftp_user_id' => $id,
                'home_directory' => $ftpUser->homeDirectory,
            ]);

            Ftp::getInstance()->deleteUser($ftpUser);
            App::ftpUsers()->delete($id);

            return $this->redirect('/ftp');
        } catch (ForbiddenException $e) {
            return $this->json(['error' => $e->getMessage()], 403);
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }
}
