<?php
$systemStatus = $systemStatus ?? [];
$health = $systemStatus['health'] ?? ['healthy' => 0, 'installed' => 0, 'total' => 0, 'summary' => 'Unavailable'];
$disk = $systemStatus['disk'] ?? [];
$memory = $systemStatus['memory'] ?? [];
$load = $systemStatus['load'] ?? [];
$services = $systemStatus['services'] ?? [];
?>
<div class="row g-3">
    <div class="col-lg-4">
        <div class="card h-100 border-0 shadow-sm">
            <div class="card-body">
                <div class="d-flex justify-content-between align-items-center mb-2">
                    <h5 class="card-title mb-0">Service health</h5>
                    <span class="badge bg-primary"><?= htmlspecialchars((string) $health['healthy']) ?>/<?= htmlspecialchars((string) $health['installed']) ?></span>
                </div>
                <p class="text-muted small mb-3"><?= htmlspecialchars((string) $health['summary']) ?></p>
                <ul class="list-group list-group-flush">
                    <?php foreach ($services as $service): ?>
                        <li class="list-group-item d-flex justify-content-between align-items-start px-0">
                            <div>
                                <div class="fw-semibold"><?= htmlspecialchars($service['label']) ?></div>
                                <div class="small text-muted"><?= htmlspecialchars($service['details']) ?></div>
                            </div>
                            <span class="badge bg-<?= htmlspecialchars($service['badge']) ?> text-uppercase"><?= htmlspecialchars($service['state']) ?></span>
                        </li>
                    <?php endforeach; ?>
                </ul>
            </div>
        </div>
    </div>
    <div class="col-lg-4">
        <div class="card h-100 border-0 shadow-sm">
            <div class="card-body">
                <h5 class="card-title">Disk usage</h5>
                <div class="display-6 mb-1"><?= htmlspecialchars((string) ($disk['percent'] ?? '0')) ?>%</div>
                <p class="text-muted"><?= htmlspecialchars((string) ($disk['used_human'] ?? '0 B')) ?> used of <?= htmlspecialchars((string) ($disk['total_human'] ?? '0 B')) ?></p>
                <div class="progress mb-3" role="progressbar" aria-label="Disk usage" aria-valuenow="<?= htmlspecialchars((string) ($disk['percent'] ?? 0)) ?>" aria-valuemin="0" aria-valuemax="100">
                    <div class="progress-bar" style="width: <?= htmlspecialchars((string) ($disk['percent'] ?? 0)) ?>%"></div>
                </div>
                <div class="small text-muted">Free space: <?= htmlspecialchars((string) ($disk['free_human'] ?? '0 B')) ?></div>
            </div>
        </div>
    </div>
    <div class="col-lg-4">
        <div class="card h-100 border-0 shadow-sm">
            <div class="card-body">
                <h5 class="card-title">Memory & load</h5>
                <div class="mb-3">
                    <div class="d-flex justify-content-between">
                        <span>Memory</span>
                        <strong><?= htmlspecialchars((string) ($memory['percent'] ?? '0')) ?>%</strong>
                    </div>
                    <div class="progress mt-2" role="progressbar" aria-label="Memory usage" aria-valuenow="<?= htmlspecialchars((string) ($memory['percent'] ?? 0)) ?>" aria-valuemin="0" aria-valuemax="100">
                        <div class="progress-bar bg-info" style="width: <?= htmlspecialchars((string) ($memory['percent'] ?? 0)) ?>%"></div>
                    </div>
                    <div class="small text-muted mt-2"><?= htmlspecialchars((string) ($memory['used_human'] ?? '0 B')) ?> used of <?= htmlspecialchars((string) ($memory['total_human'] ?? '0 B')) ?></div>
                </div>
                <dl class="row mb-0 small">
                    <dt class="col-4">1 min</dt>
                    <dd class="col-8"><?= htmlspecialchars(number_format((float) ($load['one'] ?? 0), 2)) ?></dd>
                    <dt class="col-4">5 min</dt>
                    <dd class="col-8"><?= htmlspecialchars(number_format((float) ($load['five'] ?? 0), 2)) ?></dd>
                    <dt class="col-4">15 min</dt>
                    <dd class="col-8"><?= htmlspecialchars(number_format((float) ($load['fifteen'] ?? 0), 2)) ?></dd>
                    <dt class="col-4">CPU cores</dt>
                    <dd class="col-8"><?= htmlspecialchars((string) ($load['cpu_cores'] ?? 1)) ?></dd>
                </dl>
            </div>
        </div>
    </div>
</div>
