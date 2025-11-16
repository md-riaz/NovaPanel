<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;
use App\Repositories\UserRepository;
use App\Repositories\RoleRepository;
use App\Domain\Entities\User;

class UserController extends Controller
{
    public function index(Request $request): Response
    {
        $userRepo = new UserRepository();
        $roleRepo = new RoleRepository();
        $users = $userRepo->all();
        
        // Get roles for each user
        foreach ($users as $user) {
            $user->roles = $roleRepo->getUserRoles($user->id);
        }
        
        return $this->view('pages/users/index', [
            'title' => 'Panel Users',
            'users' => $users
        ]);
    }

    public function create(Request $request): Response
    {
        $roleRepo = new RoleRepository();
        $roles = $roleRepo->all();
        
        return $this->view('pages/users/create', [
            'title' => 'Create Panel User',
            'roles' => $roles
        ]);
    }

    public function store(Request $request): Response
    {
        try {
            $username = $request->post('username');
            $email = $request->post('email');
            $password = $request->post('password');
            $passwordConfirm = $request->post('password_confirm');
            $roleIds = $request->post('roles', []);
            
            // Validate input
            if (empty($username) || empty($email) || empty($password)) {
                throw new \Exception('Username, email, and password are required');
            }
            
            // SECURITY: Validate username format to prevent path traversal attacks
            // Username must start with a letter and contain only lowercase letters, numbers, hyphens, and underscores
            // This prevents attacks like: ../../tmp or ../../../etc/passwd
            if (!preg_match('/^[a-z][a-z0-9_-]{2,31}$/i', $username)) {
                throw new \Exception('Username must start with a letter, be 3-32 characters long, and contain only letters, numbers, hyphens, and underscores');
            }
            
            if (!filter_var($email, FILTER_VALIDATE_EMAIL)) {
                throw new \Exception('Invalid email address');
            }
            
            if (strlen($password) < 8) {
                throw new \Exception('Password must be at least 8 characters long');
            }
            
            // SECURITY: Server-side password confirmation validation
            // This prevents bypassing client-side validation to create users with mismatched passwords
            if ($password !== $passwordConfirm) {
                throw new \Exception('Password and password confirmation do not match');
            }
            
            $userRepo = new UserRepository();
            $roleRepo = new RoleRepository();
            
            // Check if username or email already exists
            if ($userRepo->findByUsername($username)) {
                throw new \Exception('Username already exists');
            }
            
            if ($userRepo->findByEmail($email)) {
                throw new \Exception('Email already exists');
            }
            
            // Create user
            $user = new User(
                username: $username,
                email: $email,
                password: password_hash($password, PASSWORD_DEFAULT)
            );
            
            $user = $userRepo->create($user);
            
            // Assign roles
            if (!empty($roleIds)) {
                foreach ($roleIds as $roleId) {
                    $roleRepo->assignRoleToUser($user->id, (int) $roleId);
                }
            }
            
            // Check if this is an HTMX request
            if ($request->isHtmx()) {
                return new Response($this->successAlert('Panel user created successfully! Redirecting...'));
            }
            
            return $this->redirect('/users');
            
        } catch (\Exception $e) {
            // Check if this is an HTMX request
            if ($request->isHtmx()) {
                return new Response($this->errorAlert($e->getMessage()), 400);
            }
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }

    public function edit(Request $request, int $id): Response
    {
        $userRepo = new UserRepository();
        $roleRepo = new RoleRepository();
        
        $user = $userRepo->find($id);
        if (!$user) {
            return new Response('User not found', 404);
        }
        
        $user->roles = $roleRepo->getUserRoles($user->id);
        $allRoles = $roleRepo->all();
        
        return $this->view('pages/users/edit', [
            'title' => 'Edit Panel User',
            'user' => $user,
            'roles' => $allRoles
        ]);
    }

    public function update(Request $request, int $id): Response
    {
        try {
            $userRepo = new UserRepository();
            $roleRepo = new RoleRepository();
            
            $user = $userRepo->find($id);
            if (!$user) {
                throw new \Exception('User not found');
            }
            
            $username = $request->post('username');
            $email = $request->post('email');
            $password = $request->post('password');
            $passwordConfirm = $request->post('password_confirm');
            $roleIds = $request->post('roles', []);
            
            // Validate input
            if (empty($username) || empty($email)) {
                throw new \Exception('Username and email are required');
            }
            
            // SECURITY: Validate username format to prevent path traversal attacks
            // Username must start with a letter and contain only lowercase letters, numbers, hyphens, and underscores
            // This prevents attacks like: ../../tmp or ../../../etc/passwd
            if (!preg_match('/^[a-z][a-z0-9_-]{2,31}$/i', $username)) {
                throw new \Exception('Username must start with a letter, be 3-32 characters long, and contain only letters, numbers, hyphens, and underscores');
            }
            
            if (!filter_var($email, FILTER_VALIDATE_EMAIL)) {
                throw new \Exception('Invalid email address');
            }
            
            // Check if username or email already exists for other users
            $existingUser = $userRepo->findByUsername($username);
            if ($existingUser && $existingUser->id !== $id) {
                throw new \Exception('Username already exists');
            }
            
            $existingUser = $userRepo->findByEmail($email);
            if ($existingUser && $existingUser->id !== $id) {
                throw new \Exception('Email already exists');
            }
            
            // Update user
            $user->username = $username;
            $user->email = $email;
            
            // Update password only if provided
            if (!empty($password)) {
                if (strlen($password) < 8) {
                    throw new \Exception('Password must be at least 8 characters long');
                }
                
                // SECURITY: Server-side password confirmation validation
                // This prevents bypassing client-side validation to update users with mismatched passwords
                if ($password !== $passwordConfirm) {
                    throw new \Exception('Password and password confirmation do not match');
                }
                
                $user->password = password_hash($password, PASSWORD_DEFAULT);
            }
            
            $userRepo->update($user);
            
            // Update roles - remove all existing roles and add new ones
            $currentRoles = $roleRepo->getUserRoles($user->id);
            foreach ($currentRoles as $role) {
                $roleRepo->removeRoleFromUser($user->id, $role->id);
            }
            
            if (!empty($roleIds)) {
                foreach ($roleIds as $roleId) {
                    $roleRepo->assignRoleToUser($user->id, (int) $roleId);
                }
            }
            
            return $this->redirect('/users');
            
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }

    public function delete(Request $request, int $id): Response
    {
        try {
            $userRepo = new UserRepository();
            $user = $userRepo->find($id);
            
            if (!$user) {
                throw new \Exception('User not found');
            }
            
            $userRepo->delete($id);
            
            return $this->redirect('/users');
            
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }
}
