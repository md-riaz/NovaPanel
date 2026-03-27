<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <h1 class="h2">DNS Management</h1>
    <a href="/dns/create" class="btn btn-primary">
        <i class="bi bi-plus-circle"></i> Create DNS Zone
    </a>
</div>

<div class="table-responsive">
    <table class="table table-striped table-hover">
        <thead>
            <tr>
                <th>Domain</th>
                <th>Site</th>
                <th>Created</th>
                <th>Actions</th>
            </tr>
        </thead>
        <tbody>
            <?php if (empty($domains)): ?>
                <tr>
                    <td colspan="4" class="text-center text-muted">No DNS zones found</td>
                </tr>
            <?php else: ?>
                <?php foreach ($domains as $domain): ?>
                    <tr>
                        <td>
                            <strong><?= htmlspecialchars($domain->name ?? '') ?></strong>
                        </td>
                        <td><?= htmlspecialchars($domain->siteDomain ?? 'Unknown') ?></td>
                        <td><?= htmlspecialchars($domain->createdAt ?? '') ?></td>
                        <td>
                            <a href="/dns/<?= htmlspecialchars((string) ($domain->id ?? '')) ?>" class="btn btn-sm btn-outline-primary">
                                <i class="bi bi-eye"></i> View Records
                            </a>
                        </td>
                    </tr>
                <?php endforeach; ?>
            <?php endif; ?>
        </tbody>
    </table>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
