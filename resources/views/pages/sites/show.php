<?php ob_start(); ?>

<div class="d-flex justify-content-between flex-wrap flex-md-nowrap align-items-center pb-2 mb-3 border-bottom">
    <div>
        <h1 class="h2 mb-0"><?= htmlspecialchars($site->domain) ?></h1>
        <div class="text-muted">Owner: <?= htmlspecialchars($site->ownerUsername ?? ('User #' . $site->userId)) ?></div>
    </div>
    <a href="/sites" class="btn btn-outline-secondary">Back to Sites</a>
</div>

<div class="row g-4">
    <div class="col-lg-7">
        <div class="card mb-4">
            <div class="card-header">Site Configuration</div>
            <div class="card-body">
                <dl class="row mb-0">
                    <dt class="col-sm-4">Document Root</dt>
                    <dd class="col-sm-8"><code><?= htmlspecialchars($site->documentRoot) ?></code></dd>

                    <dt class="col-sm-4">PHP Version</dt>
                    <dd class="col-sm-8">PHP <?= htmlspecialchars($site->phpVersion) ?></dd>

                    <dt class="col-sm-4">Certificate Provider</dt>
                    <dd class="col-sm-8"><?= htmlspecialchars($site->certificateProvider ?? 'Not configured') ?></dd>

                    <dt class="col-sm-4">Validation Method</dt>
                    <dd class="col-sm-8"><?= htmlspecialchars(strtoupper($site->certificateValidationMethod ?? 'webroot')) ?></dd>

                    <dt class="col-sm-4">Status</dt>
                    <dd class="col-sm-8">
                        <span class="badge bg-<?= match ($site->certificateStatus) {
                            'active' => 'success',
                            'pending', 'renewing' => 'warning text-dark',
                            'failed', 'revoked' => 'danger',
                            default => 'secondary',
                        } ?>"><?= htmlspecialchars(ucfirst($site->certificateStatus ?? 'unissued')) ?></span>
                    </dd>

                    <dt class="col-sm-4">Expires At</dt>
                    <dd class="col-sm-8"><?= htmlspecialchars($site->certificateExpiresAt ?: 'Not installed') ?></dd>

                    <dt class="col-sm-4">Certificate Path</dt>
                    <dd class="col-sm-8"><code><?= htmlspecialchars($site->certificatePath ?: 'Unavailable') ?></code></dd>

                    <dt class="col-sm-4">Private Key Path</dt>
                    <dd class="col-sm-8"><code><?= htmlspecialchars($site->certificateKeyPath ?: 'Unavailable') ?></code></dd>

                    <dt class="col-sm-4">Auto Renew</dt>
                    <dd class="col-sm-8"><?= $site->certificateAutoRenew ? 'Enabled' : 'Disabled' ?></dd>

                    <dt class="col-sm-4">HTTP Redirect</dt>
                    <dd class="col-sm-8"><?= $site->forceHttps ? 'Force HTTPS' : 'Allow HTTP and HTTPS' ?></dd>
                </dl>
            </div>
        </div>

        <?php if (!empty($site->lastCertificateError)): ?>
            <div class="alert alert-danger">
                <strong>Last renewal/request error:</strong><br>
                <code><?= htmlspecialchars($site->lastCertificateError) ?></code>
            </div>
        <?php endif; ?>
    </div>

    <div class="col-lg-5">
        <div class="card mb-4">
            <div class="card-header">Request or Reissue Certificate</div>
            <div class="card-body">
                <form method="POST" action="/sites/<?= $site->id ?>/certificate" class="vstack gap-3">
                    <div>
                        <label class="form-label" for="certificate_provider">Provider</label>
                        <select class="form-select" id="certificate_provider" name="certificate_provider">
                            <option value="letsencrypt" <?= ($site->certificateProvider ?? 'letsencrypt') === 'letsencrypt' ? 'selected' : '' ?>>Let's Encrypt</option>
                        </select>
                    </div>
                    <div>
                        <label class="form-label" for="certificate_validation_method">Validation Method</label>
                        <select class="form-select" id="certificate_validation_method" name="certificate_validation_method">
                            <option value="webroot" <?= ($site->certificateValidationMethod ?? 'webroot') === 'webroot' ? 'selected' : '' ?>>HTTP Webroot</option>
                            <option value="dns" <?= ($site->certificateValidationMethod ?? 'webroot') === 'dns' ? 'selected' : '' ?>>DNS Hook</option>
                        </select>
                    </div>
                    <div class="form-check">
                        <input class="form-check-input" type="checkbox" id="certificate_auto_renew" name="certificate_auto_renew" <?= $site->certificateAutoRenew ? 'checked' : '' ?>>
                        <label class="form-check-label" for="certificate_auto_renew">Enable auto-renew</label>
                    </div>
                    <div class="form-check">
                        <input class="form-check-input" type="checkbox" id="force_https_request" name="force_https" <?= $site->forceHttps ? 'checked' : '' ?>>
                        <label class="form-check-label" for="force_https_request">Force HTTPS after issuance</label>
                    </div>
                    <button type="submit" class="btn btn-primary">Request / Reissue Certificate</button>
                </form>
            </div>
        </div>

        <div class="card mb-4">
            <div class="card-header">Lifecycle Actions</div>
            <div class="card-body d-grid gap-2">
                <form method="POST" action="/sites/<?= $site->id ?>/certificate/renew">
                    <button type="submit" class="btn btn-success w-100">Renew Certificate</button>
                </form>
                <form method="POST" action="/sites/<?= $site->id ?>/certificate/reinstall">
                    <button type="submit" class="btn btn-outline-primary w-100">Reinstall Certificate Paths</button>
                </form>
                <form method="POST" action="/sites/<?= $site->id ?>/certificate/revoke" onsubmit="return confirm('Revoke and remove this certificate from the site?');">
                    <button type="submit" class="btn btn-outline-danger w-100">Revoke Certificate</button>
                </form>
            </div>
        </div>

        <div class="card">
            <div class="card-header">HTTPS Policy</div>
            <div class="card-body">
                <form method="POST" action="/sites/<?= $site->id ?>/https" class="vstack gap-3">
                    <div class="form-check form-switch">
                        <input class="form-check-input" type="checkbox" role="switch" id="force_https_policy" name="force_https" <?= $site->forceHttps ? 'checked' : '' ?>>
                        <label class="form-check-label" for="force_https_policy">Redirect HTTP traffic to HTTPS</label>
                    </div>
                    <button type="submit" class="btn btn-outline-secondary">Save HTTPS Policy</button>
                    <div class="form-text">NovaPanel always keeps the ACME challenge path on port 80 so renewals can continue when using webroot validation.</div>
                </form>
            </div>
        </div>
    </div>
</div>

<?php $content = ob_get_clean(); ?>
<?php include __DIR__ . '/../../layouts/app.php'; ?>
