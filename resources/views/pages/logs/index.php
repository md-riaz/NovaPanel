<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <div>
        <h1 class="h2 mb-1">Log Viewer</h1>
        <p class="text-muted mb-0">Review service and panel logs without dropping into the terminal.</p>
    </div>
</div>

<form method="GET" action="/logs" class="card border-0 shadow-sm mb-4">
    <div class="card-body row g-3 align-items-end">
        <div class="col-md-6">
            <label for="source" class="form-label">Log source</label>
            <select class="form-select" id="source" name="source">
                <?php foreach ($sources as $source): ?>
                    <option value="<?= htmlspecialchars($source['key']) ?>" <?= $selected === $source['key'] ? 'selected' : '' ?>>
                        <?= htmlspecialchars($source['label']) ?>
                    </option>
                <?php endforeach; ?>
            </select>
        </div>
        <div class="col-md-3">
            <label for="lines" class="form-label">Tail lines</label>
            <select class="form-select" id="lines" name="lines">
                <?php foreach ([100, 200, 300, 500] as $option): ?>
                    <option value="<?= $option ?>" <?= (int) $lines === $option ? 'selected' : '' ?>>Last <?= $option ?></option>
                <?php endforeach; ?>
            </select>
        </div>
        <div class="col-md-3">
            <button type="submit" class="btn btn-primary w-100">
                <i class="bi bi-search"></i> Load log
            </button>
        </div>
    </div>
</form>

<div class="row g-4">
    <div class="col-lg-4">
        <div class="card border-0 shadow-sm h-100">
            <div class="card-body">
                <h5 class="card-title">Available sources</h5>
                <div class="list-group list-group-flush">
                    <?php foreach ($sources as $source): ?>
                        <div class="list-group-item px-0">
                            <div class="fw-semibold"><?= htmlspecialchars($source['label']) ?></div>
                            <div class="small text-muted mb-1"><?= htmlspecialchars($source['description']) ?></div>
                            <code class="small"><?= htmlspecialchars((string) ($source['path'] ?? 'Unavailable')) ?></code>
                        </div>
                    <?php endforeach; ?>
                </div>
            </div>
        </div>
    </div>
    <div class="col-lg-8">
        <div class="card border-0 shadow-sm h-100">
            <div class="card-body">
                <div class="d-flex justify-content-between flex-wrap gap-2 align-items-start mb-3">
                    <div>
                        <h5 class="card-title mb-1"><?= htmlspecialchars($log['source']['label']) ?></h5>
                        <div class="small text-muted"><?= htmlspecialchars($log['source']['description']) ?></div>
                    </div>
                    <div class="text-end small text-muted">
                        <div><?= htmlspecialchars((string) ($log['source']['path'] ?? 'Unavailable')) ?></div>
                        <?php if (isset($log['updated_at'])): ?>
                            <div>Updated <?= htmlspecialchars($log['updated_at']) ?> · <?= htmlspecialchars($log['size_human']) ?></div>
                        <?php endif; ?>
                    </div>
                </div>
                <pre class="bg-dark text-light p-3 rounded" style="min-height: 540px; max-height: 70vh; overflow: auto;"><?= htmlspecialchars($log['content']) ?></pre>
            </div>
        </div>
    </div>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
