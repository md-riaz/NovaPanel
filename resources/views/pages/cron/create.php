<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">Add Cron Job</h1>
</div>

<div class="row">
    <div class="col-md-8">
        <div id="alert-container"></div>
        
        <form method="POST" action="/cron" 
              hx-post="/cron" 
              hx-target="#alert-container"
              hx-on::after-request="if(event.detail.successful) { setTimeout(() => window.location.href='/cron', 1500); }">
            
            <div class="mb-3">
                <label for="user_id" class="form-label">Panel User (Owner)</label>
                <select class="form-select" id="user_id" name="user_id" required>
                    <option value="">Select a panel user</option>
                    <?php foreach ($users ?? [] as $user): ?>
                        <option value="<?= $user->id ?>"><?= htmlspecialchars($user->username) ?> (<?= htmlspecialchars($user->email) ?>)</option>
                    <?php endforeach; ?>
                </select>
            </div>

            <div class="mb-3">
                <label for="schedule" class="form-label">Schedule</label>
                <input type="text" class="form-control" id="schedule" name="schedule" 
                       placeholder="* * * * *" required>
                <div class="form-text">Cron format: minute hour day month weekday (e.g., "0 2 * * *" for daily at 2 AM)</div>
            </div>

            <div class="mb-3">
                <label for="command" class="form-label">Command</label>
                <input type="text" class="form-control" id="command" name="command" required>
                <div class="form-text">Full command to execute</div>
            </div>

            <div class="mb-3 form-check">
                <input type="checkbox" class="form-check-input" id="enabled" name="enabled" value="1" checked>
                <label class="form-check-label" for="enabled">
                    Enabled
                </label>
            </div>

            <div class="mb-3">
                <button type="submit" class="btn btn-primary">
                    <span class="htmx-indicator spinner-border spinner-border-sm" role="status"></span>
                    Add Cron Job
                </button>
                <a href="/cron" class="btn btn-secondary">Cancel</a>
            </div>
        </form>
    </div>

    <div class="col-md-4">
        <div class="card">
            <div class="card-header">
                <h5 class="card-title mb-0">Common Schedules</h5>
            </div>
            <div class="card-body">
                <dl>
                    <dt>Every minute</dt>
                    <dd><code>* * * * *</code></dd>
                    
                    <dt>Every hour</dt>
                    <dd><code>0 * * * *</code></dd>
                    
                    <dt>Daily at midnight</dt>
                    <dd><code>0 0 * * *</code></dd>
                    
                    <dt>Daily at 2 AM</dt>
                    <dd><code>0 2 * * *</code></dd>
                    
                    <dt>Weekly on Sunday</dt>
                    <dd><code>0 0 * * 0</code></dd>
                    
                    <dt>Monthly</dt>
                    <dd><code>0 0 1 * *</code></dd>
                </dl>
            </div>
        </div>
    </div>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
