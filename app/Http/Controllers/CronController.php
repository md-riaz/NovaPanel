<?php

namespace App\Http\Controllers;

use App\Facades\App;
use App\Facades\Cron;
use App\Http\Request;
use App\Http\Response;
use App\Support\AuditLogger;

class CronController extends Controller
{
    public function index(Request $request): Response
    {
        $cronJobs = $this->isAdmin()
            ? App::cronJobs()->all()
            : App::cronJobs()->findByUserId($this->currentUserId());

        foreach ($cronJobs as $cronJob) {
            $user = App::users()->find($cronJob->userId);
            $cronJob->ownerUsername = $user ? $user->username : 'Unknown';
        }

        return $this->view('pages/cron/index', [
            'title' => 'Cron Jobs',
            'cronJobs' => $cronJobs,
        ]);
    }

    public function create(Request $request): Response
    {
        return $this->view('pages/cron/create', [
            'title' => 'Add Cron Job',
            'users' => $this->scopedUsers(),
        ]);
    }

    public function store(Request $request): Response
    {
        try {
            $userId = $this->resolveOwnedUserId((int) $request->post('user_id'));
            $schedule = $request->post('schedule');
            $command = $request->post('command');
            $enabled = (bool) $request->post('enabled', true);

            App::addCronJobService()->execute(
                userId: $userId,
                schedule: $schedule,
                command: $command,
                enabled: $enabled
            );

            AuditLogger::logCreated('cron_job', $command, [
                'user_id' => $userId,
                'schedule' => $schedule,
                'enabled' => $enabled,
            ]);

            if ($request->isHtmx()) {
                return new Response($this->successAlert('Cron job added successfully! Redirecting...'));
            }

            return $this->redirect('/cron');
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
            $cronJob = App::cronJobs()->find($id);
            if (!$cronJob) {
                throw new \Exception('Cron job not found');
            }

            $this->authorizeOwnedUserId((int) $cronJob->userId);

            $user = App::users()->find($cronJob->userId);
            if (!$user) {
                throw new \Exception('User not found');
            }

            AuditLogger::logDeleted('cron_job', $cronJob->command, [
                'cron_job_id' => $id,
                'schedule' => $cronJob->schedule,
            ]);

            Cron::getInstance()->deleteJob($user, $cronJob);
            App::cronJobs()->delete($id);

            return $this->redirect('/cron');
        } catch (\Exception $e) {
            return $this->json(['error' => $e->getMessage()], 400);
        }
    }
}
