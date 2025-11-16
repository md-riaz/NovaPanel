<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;
use App\Repositories\AccountRepository;
use App\Repositories\UserRepository;
use App\Services\CreateAccountService;
use App\Infrastructure\Shell\Shell;

class AccountController extends Controller
{
    public function index(Request $request): Response
    {
        $accountRepo = new AccountRepository();
        $accounts = $accountRepo->all();
        
        return $this->view('pages/accounts/index', [
            'title' => 'Accounts',
            'accounts' => $accounts
        ]);
    }

    public function create(Request $request): Response
    {
        $userRepo = new UserRepository();
        $users = $userRepo->all();
        
        return $this->view('pages/accounts/create', [
            'title' => 'Create Account',
            'users' => $users
        ]);
    }

    public function store(Request $request): Response
    {
        try {
            $username = $request->post('username');
            $userId = (int) $request->post('user_id');
            $homeDirectory = $request->post('home_directory') ?: null;
            
            $service = new CreateAccountService(
                new AccountRepository(),
                new Shell()
            );
            
            $account = $service->execute($userId, $username, $homeDirectory);
            
            // In a real app, you'd use sessions for flash messages
            return $this->redirect('/accounts');
            
        } catch (\Exception $e) {
            // In a real app, handle errors properly
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }
}
