<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <div>
        <h1 class="h2 mb-1">Application Templates</h1>
        <p class="text-muted mb-0">Ship starter blueprints independently so site onboarding improves incrementally.</p>
    </div>
    <a href="/sites/create" class="btn btn-primary"><i class="bi bi-plus-circle"></i> Create site</a>
</div>

<div class="row g-4">
    <?php foreach ($templates as $template): ?>
        <div class="col-md-6 col-xl-4">
            <div class="card border-0 shadow-sm h-100">
                <div class="card-body d-flex flex-column">
                    <span class="badge bg-light text-dark align-self-start mb-3"><?= htmlspecialchars($template['stack']) ?></span>
                    <h5 class="card-title"><?= htmlspecialchars($template['name']) ?></h5>
                    <p class="text-muted flex-grow-1"><?= htmlspecialchars($template['summary']) ?></p>
                    <div class="d-flex justify-content-between align-items-center mt-3">
                        <code><?= htmlspecialchars($template['key']) ?></code>
                        <a href="/sites/create?template=<?= urlencode($template['key']) ?>" class="btn btn-sm btn-outline-primary">Use template</a>
                    </div>
                </div>
            </div>
        </div>
    <?php endforeach; ?>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
