<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;
use App\Repositories\CronJobRepository;
use App\Repositories\UserRepository;
use App\Services\AddCronJobService;
use App\Facades\Cron;

class CronController extends Controller
{
    public function index(Request $request): Response
    {
        $cronRepo = new CronJobRepository();
        $userRepo = new UserRepository();
        $cronJobs = $cronRepo->all();
        
        // Load owner information for each cron job
        foreach ($cronJobs as $cronJob) {
            $user = $userRepo->find($cronJob->userId);
            $cronJob->ownerUsername = $user ? $user->username : 'Unknown';
        }
        
        return $this->view('pages/cron/index', [
            'title' => 'Cron Jobs',
            'cronJobs' => $cronJobs
        ]);
    }

    public function create(Request $request): Response
    {
        $userRepo = new UserRepository();
        $users = $userRepo->all();
        
        return $this->view('pages/cron/create', [
            'title' => 'Add Cron Job',
            'users' => $users
        ]);
    }

    public function store(Request $request): Response
    {
        try {
            $userId = (int) $request->post('user_id');
            $schedule = $request->post('schedule');
            $command = $request->post('command');
            $enabled = (bool) $request->post('enabled', true);
            
            $service = new AddCronJobService(
                new CronJobRepository(),
                new UserRepository(),
                Cron::getInstance()
            );
            
            $cronJob = $service->execute(
                userId: $userId,
                schedule: $schedule,
                command: $command,
                enabled: $enabled
            );
            
            // Check if this is an HTMX request
            if ($request->isHtmx()) {
                return new Response($this->successAlert('Cron job added successfully! Redirecting...'));
            }
            
            return $this->redirect('/cron');
            
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
            $cronRepo = new CronJobRepository();
            $userRepo = new UserRepository();
            
            $cronJob = $cronRepo->find($id);
            
            if (!$cronJob) {
                throw new \Exception('Cron job not found');
            }
            
            // Get user for cron manager
            $user = $userRepo->find($cronJob->userId);
            if (!$user) {
                throw new \Exception('User not found');
            }
            
            // Delete from infrastructure
            Cron::getInstance()->deleteJob($user, $cronJob);
            
            // Delete from panel database
            $cronRepo->delete($id);
            
            return $this->redirect('/cron');
            
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }
}
