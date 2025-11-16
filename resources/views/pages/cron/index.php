<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">Cron Jobs</h1>
    <a href="/cron/create" class="btn btn-primary">
        <i class="bi bi-plus-circle"></i> Create Cron Job
    </a>
</div>

<div class="mb-3">
    <label for="account-select" class="form-label">Select Account</label>
    <select class="form-select" id="account-select">
        <option value="">-- Select an account --</option>
    </select>
</div>

<div class="table-responsive">
    <table class="table table-striped table-hover">
        <thead>
            <tr>
                <th>Schedule</th>
                <th>Command</th>
                <th>Status</th>
                <th>Created</th>
                <th>Actions</th>
            </tr>
        </thead>
        <tbody>
            <tr>
                <td colspan="5" class="text-center text-muted">Select an account to view cron jobs</td>
            </tr>
        </tbody>
    </table>
</div>

<div class="alert alert-info mt-3">
    <strong>Cron Schedule Format:</strong><br>
    <code>* * * * *</code> = minute hour day month weekday<br>
    Example: <code>0 2 * * *</code> = Daily at 2:00 AM<br>
    Example: <code>*/5 * * * *</code> = Every 5 minutes
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
