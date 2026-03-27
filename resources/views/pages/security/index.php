<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <div>
        <h1 class="h2 mb-1">Security</h1>
        <p class="text-muted mb-0">Read-only status first, with narrow operational controls when the host supports them.</p>
    </div>
</div>

<?php if (!empty($message)): ?>
    <div class="alert alert-success"><i class="bi bi-check-circle"></i> <?= htmlspecialchars($message) ?></div>
<?php endif; ?>

<?php if (!empty($error)): ?>
    <div class="alert alert-danger"><i class="bi bi-exclamation-triangle"></i> <?= htmlspecialchars($error) ?></div>
<?php endif; ?>

<div class="alert alert-info border-0 shadow-sm">
    <i class="bi bi-info-circle"></i>
    Controlled actions are only enabled when NovaPanel can run passwordless sudo on the host.
</div>

<div class="row g-4">
    <?php foreach ($overview['components'] as $component): ?>
        <div class="col-lg-6">
            <div class="card border-0 shadow-sm h-100">
                <div class="card-body d-flex flex-column">
                    <div class="d-flex justify-content-between align-items-start mb-3">
                        <div>
                            <h5 class="card-title mb-1"><?= htmlspecialchars($component['label']) ?></h5>
                            <p class="text-muted small mb-0">
                                <?= $component['installed'] ? 'Installed on this host.' : 'Not installed on this host.' ?>
                            </p>
                        </div>
                        <span class="badge bg-<?= htmlspecialchars($component['status']['badge']) ?> text-uppercase"><?= htmlspecialchars($component['status']['state']) ?></span>
                    </div>
                    <pre class="bg-light border rounded p-3 flex-grow-1 small"><?= htmlspecialchars($component['status']['details']) ?></pre>
                    <div class="mt-3 d-flex flex-wrap gap-2">
                        <?php if ($component['actions'] === []): ?>
                            <span class="text-muted small">No controlled actions for this component yet.</span>
                        <?php else: ?>
                            <?php foreach ($component['actions'] as $action): ?>
                                <form method="POST" action="/security/actions" class="d-inline">
                                    <input type="hidden" name="_csrf_token" value="<?= htmlspecialchars((string) ($csrfToken ?? '')) ?>">
                                    <input type="hidden" name="action" value="<?= htmlspecialchars($action['key']) ?>">
                                    <button type="submit" class="btn btn-sm btn-outline-primary" <?= $overview['can_manage'] ? '' : 'disabled' ?>>
                                        <?= htmlspecialchars($action['label']) ?>
                                    </button>
                                </form>
                            <?php endforeach; ?>
                        <?php endif; ?>
                    </div>
                </div>
            </div>
        </div>
    <?php endforeach; ?>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
