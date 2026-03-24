<?php

namespace App\Http\Controllers;

use App\Domain\Entities\User;
use App\Facades\App;
use App\Http\Request;
use App\Http\Response;
use App\Support\AuditLogger;

class UserController extends Controller
{
    public function index(Request $request): Response
    {
        $userRepo = App::users();
        $roleRepo = App::roles();
        $users = $this->isAdmin()
            ? $userRepo->all()
            : array_filter([$userRepo->find($this->currentUserId())]);

        foreach ($users as $user) {
            $user->roles = $roleRepo->getUserRoles($user->id);
        }

        return $this->view('pages/users/index', [
            'title' => 'Panel Users',
            'users' => $users,
            'canCreateUsers' => $this->isAdmin(),
            'canDeleteUsers' => $this->isAdmin(),
        ]);
    }

    public function create(Request $request): Response
    {
        return $this->view('pages/users/create', [
            'title' => 'Create Panel User',
            'roles' => App::roles()->all(),
        ]);
    }

    public function store(Request $request): Response
    {
        try {
            $username = $request->post('username');
            $email = $request->post('email');
            $password = $request->post('password');
            $passwordConfirm = $request->post('password_confirm');
            $roleIds = (array) $request->post('roles', []);

            if (empty($username) || empty($email) || empty($password)) {
                throw new \Exception('Username, email, and password are required');
            }

            if (!preg_match('/^[a-z][a-z0-9_-]{2,31}$/i', $username)) {
                throw new \Exception('Username must start with a letter, be 3-32 characters long, and contain only letters, numbers, hyphens, and underscores');
            }

            if (!filter_var($email, FILTER_VALIDATE_EMAIL)) {
                throw new \Exception('Invalid email address');
            }

            if (strlen($password) < 8) {
                throw new \Exception('Password must be at least 8 characters long');
            }

            if ($password !== $passwordConfirm) {
                throw new \Exception('Password and password confirmation do not match');
            }

            $userRepo = App::users();
            $roleRepo = App::roles();

            if ($userRepo->findByUsername($username)) {
                throw new \Exception('Username already exists');
            }

            if ($userRepo->findByEmail($email)) {
                throw new \Exception('Email already exists');
            }

            $user = $userRepo->create(new User(
                username: $username,
                email: $email,
                password: password_hash($password, PASSWORD_DEFAULT)
            ));

            foreach ($roleIds as $roleId) {
                $roleRepo->assignRoleToUser($user->id, (int) $roleId);
            }

            AuditLogger::logCreated('user', $username, [
                'email' => $email,
                'roles' => $roleIds,
            ]);

            if ($request->isHtmx()) {
                return new Response($this->successAlert('Panel user created successfully! Redirecting...'));
            }

            return $this->redirect('/users');
        } catch (\Exception $e) {
            if ($request->isHtmx()) {
                return new Response($this->errorAlert($e->getMessage()), 400);
            }

            return $this->json(['error' => $e->getMessage()], 400);
        }
    }

    public function edit(Request $request, int $id): Response
    {
        if (!$this->isAdmin() && $id !== $this->currentUserId()) {
            return new Response('You do not have access to this user.', 403);
        }

        $user = App::users()->find($id);
        if (!$user) {
            return new Response('User not found', 404);
        }

        $user->roles = App::roles()->getUserRoles($user->id);

        return $this->view('pages/users/edit', [
            'title' => 'Edit Panel User',
            'user' => $user,
            'roles' => App::roles()->all(),
            'canManageRoles' => $this->isAdmin(),
        ]);
    }

    public function update(Request $request, int $id): Response
    {
        try {
            if (!$this->isAdmin() && $id !== $this->currentUserId()) {
                throw new \Exception('You do not have access to this user.');
            }

            $userRepo = App::users();
            $roleRepo = App::roles();
            $user = $userRepo->find($id);
            if (!$user) {
                throw new \Exception('User not found');
            }

            $username = $request->post('username');
            $email = $request->post('email');
            $password = $request->post('password');
            $passwordConfirm = $request->post('password_confirm');
            $roleIds = (array) $request->post('roles', []);

            if (empty($username) || empty($email)) {
                throw new \Exception('Username and email are required');
            }

            if (!preg_match('/^[a-z][a-z0-9_-]{2,31}$/i', $username)) {
                throw new \Exception('Username must start with a letter, be 3-32 characters long, and contain only letters, numbers, hyphens, and underscores');
            }

            if (!filter_var($email, FILTER_VALIDATE_EMAIL)) {
                throw new \Exception('Invalid email address');
            }

            $existingUser = $userRepo->findByUsername($username);
            if ($existingUser && $existingUser->id !== $id) {
                throw new \Exception('Username already exists');
            }

            $existingUser = $userRepo->findByEmail($email);
            if ($existingUser && $existingUser->id !== $id) {
                throw new \Exception('Email already exists');
            }

            $user->username = $username;
            $user->email = $email;

            if (!empty($password)) {
                if (strlen($password) < 8) {
                    throw new \Exception('Password must be at least 8 characters long');
                }

                if ($password !== $passwordConfirm) {
                    throw new \Exception('Password and password confirmation do not match');
                }

                $user->password = password_hash($password, PASSWORD_DEFAULT);
            }

            $userRepo->update($user);

            if ($this->isAdmin()) {
                foreach ($roleRepo->getUserRoles($user->id) as $role) {
                    $roleRepo->removeRoleFromUser($user->id, $role->id);
                }

                foreach ($roleIds as $roleId) {
                    $roleRepo->assignRoleToUser($user->id, (int) $roleId);
                }
            }

            AuditLogger::logUpdated('user', $username, [
                'email' => $email,
                'roles' => $this->isAdmin() ? $roleIds : 'unchanged',
                'password_changed' => !empty($password),
            ]);

            return $this->redirect('/users');
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }

    public function delete(Request $request, int $id): Response
    {
        try {
            if (!$this->isAdmin()) {
                throw new \Exception('You do not have permission to delete users.');
            }

            $user = App::users()->find($id);
            if (!$user) {
                throw new \Exception('User not found');
            }

            AuditLogger::logDeleted('user', $user->username, [
                'user_id' => $id,
                'email' => $user->email,
            ]);

            App::users()->delete($id);

            return $this->redirect('/users');
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }
}
