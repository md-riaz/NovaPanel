<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;
use App\Facades\App;
use App\Facades\Cron;
use App\Support\AuditLogger;

class CronController extends Controller
{
    public function index(Request $request): Response
    {
        $cronJobs = App::cronJobs()->all();
        
        // Load owner information for each cron job
        foreach ($cronJobs as $cronJob) {
            $user = App::users()->find($cronJob->userId);
            $cronJob->ownerUsername = $user ? $user->username : 'Unknown';
        }
        
        return $this->view('pages/cron/index', [
            'title' => 'Cron Jobs',
            'cronJobs' => $cronJobs
        ]);
    }

    public function create(Request $request): Response
    {
        $users = App::users()->all();
        
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
            
            // Use App facade to get service with all dependencies injected
            $service = App::addCronJobService();
            
            $cronJob = $service->execute(
                userId: $userId,
                schedule: $schedule,
                command: $command,
                enabled: $enabled
            );
            
            // Log audit event
            AuditLogger::logCreated('cron_job', $command, [
                'user_id' => $userId,
                'schedule' => $schedule,
                'enabled' => $enabled
            ]);
            
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
            $cronJob = App::cronJobs()->find($id);
            
            if (!$cronJob) {
                throw new \Exception('Cron job not found');
            }
            
            // Get user for cron manager
            $user = App::users()->find($cronJob->userId);
            if (!$user) {
                throw new \Exception('User not found');
            }
            
            // Log audit event before deletion
            AuditLogger::logDeleted('cron_job', $cronJob->command, [
                'cron_job_id' => $id,
                'schedule' => $cronJob->schedule
            ]);
            
            // Delete from infrastructure
            Cron::getInstance()->deleteJob($user, $cronJob);
            
            // Delete from panel database
            App::cronJobs()->delete($id);
            
            return $this->redirect('/cron');
            
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }
}
